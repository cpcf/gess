package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	dsl "github.com/cpcf/gess/dsl"
	rules "github.com/cpcf/gess/rules"
	sess "github.com/cpcf/gess/session"
)

type webStep struct {
	Number      int      `json:"number"`
	Title       string   `json:"title"`
	Concept     string   `json:"concept"`
	Why         string   `json:"why"`
	Example     string   `json:"example"`
	Walkthrough []string `json:"walkthrough"`
	Task        string   `json:"task"`
	Expected    string   `json:"expected"`
	Hint        string   `json:"hint"`
	Explanation string   `json:"explanation"`
}

var webSteps = []webStep{
	{
		Number:  0,
		Title:   "Overview",
		Concept: "This tutorial builds a small vulnerability management and response workflow. The Go program loads vulnerability and asset data, runs Gess rules, and prints the response lane each vulnerability should enter.",
		Why:     "Vulnerability response is policy-heavy: severity, exploitability, asset criticality, exposure, accepted risk, and asset history can all affect the response. A rules engine keeps that decision logic in a ruleset instead of spreading it through application control flow.",
		Walkthrough: []string{
			"A `vulnerability` is a finding that may need remediation. It has an id, asset id, severity, score, category, and exploitability flag.",
			"An `asset` is the affected system. The tutorial tracks criticality, exposure, and owner.",
			"An `accepted-risk` fact is an exception. It means the organization has accepted a specific vulnerability for a stated reason.",
			"A `remediation-action` is the derived response decision. It records the response target, lane, and reason. Most targets are vulnerability ids; the `forall` example uses an asset id.",
			"A response `lane` is the queue or workflow the finding should enter, such as emergency, accepted-risk, standard, or a lane used to demonstrate one conditional element.",
			"A `template` defines the fields allowed on a kind of fact. Templates give the ruleset a schema.",
			"A `rule` has conditions before `=>` and actions after `=>`. Gess matches facts against the conditions and fires the actions for each complete match.",
			"A `query` is how Go code reads selected facts after the rules run.",
			"A rules engine runs decisions that are expressed as rules instead of hard-coded control flow. You describe facts, patterns to match, and actions to take when those patterns are true.",
			"`gessc` compiles `.gess` files into Go code. The normal workflow is to author rules in `.gess`, compile them, and use the generated ruleset from Go.",
			"This tutorial starts with an empty `.gess` file, adds templates and seed facts, adds queries, then builds rules for joins, `and`, `or`, `exists`, `forall`, `not`, aggregation, and host callbacks.",
		},
	},
	{
		Number:  1,
		Title:   "Define templates",
		Concept: "A template defines the shape of a fact. Before Gess can match vulnerabilities, assets, or derived response decisions, the `.gess` file must declare the fields each fact type can contain.",
		Why:     "Templates are the schema for the working memory. They let Gess validate facts and let rules refer to fields by name instead of by position.",
		Example: `(deftemplate vulnerability
  (slot id (type STRING) (required TRUE))
  (slot asset (type STRING) (required TRUE))
  (slot severity (type STRING) (required TRUE))
  (slot score (type INTEGER) (required TRUE))
  (slot category (type STRING) (required TRUE))
  (slot exploit-available (type BOOLEAN) (required TRUE))
)

(deftemplate asset
  (slot id (type STRING) (required TRUE))
  (slot criticality (type STRING) (required TRUE))
  (slot exposure (type STRING) (required TRUE))
  (slot owner (type STRING) (required TRUE))
)

(deftemplate accepted-risk
  (slot vulnerability (type STRING) (required TRUE))
  (slot reason (type STRING) (required TRUE))
)

(deftemplate remediation-action
  (declare (duplicate-policy unique-key) (duplicate-key target))
  (slot target (type STRING) (required TRUE))
  (slot lane (type STRING) (required TRUE))
  (slot reason (type STRING) (required TRUE))
)

(deftemplate critical-vulnerability-summary
  (declare (duplicate-policy unique-key) (duplicate-key severity))
  (slot severity (type STRING) (required TRUE))
  (slot count (type INTEGER) (required TRUE))
  (slot total (type INTEGER) (required TRUE))
)`,
		Walkthrough: []string{
			"`deftemplate` starts a fact schema. The first value after it is the template name.",
			"`slot` declares a field. For example, a `vulnerability` fact has `id`, `asset`, `severity`, `score`, `category`, and `exploit-available` fields.",
			"`type` tells Gess what kind of value the field accepts. This tutorial uses strings, integers, and booleans.",
			"`required TRUE` means every fact of that template must provide that field.",
			"`remediation-action` and `critical-vulnerability-summary` are derived templates. The rules will assert these facts later.",
			"`duplicate-policy unique-key` keeps one derived fact per key, so rerunning a rule updates the same logical result instead of creating duplicates.",
		},
		Task:        "Add these templates at the top of the editor, then run checks. This step should complete before any facts or rules exist.",
		Expected:    "setup: templates",
		Hint:        "Start with the templates exactly as shown. Later steps depend on these template names and slot names.",
		Explanation: "This step introduces the schema layer: templates define the kinds of facts that can exist in a Gess session.",
	},
	{
		Number:  2,
		Title:   "Add seed facts",
		Concept: "A fact is one record in the session. Seed facts are initial records loaded when the Go program creates the session.",
		Why:     "Rules need data to match. The tutorial uses assets, an accepted-risk exception, and vulnerability findings as the initial data set.",
		Example: `(deffacts seed-vulnerabilities
  (asset (id "APP-100") (criticality "critical") (exposure "internet") (owner "payments"))
  (asset (id "APP-200") (criticality "medium") (exposure "internal") (owner "backoffice"))
  (asset (id "APP-300") (criticality "low") (exposure "internal") (owner "analytics"))
  (accepted-risk (vulnerability "VULN-200") (reason "compensating-control"))
  (vulnerability
    (id "VULN-100")
    (asset "APP-100")
    (severity "critical")
    (score 98)
    (category "remote-code-execution")
    (exploit-available TRUE)
  )
  (vulnerability
    (id "VULN-200")
    (asset "APP-200")
    (severity "high")
    (score 82)
    (category "dependency")
    (exploit-available FALSE)
  )
  (vulnerability
    (id "VULN-300")
    (asset "APP-300")
    (severity "medium")
    (score 55)
    (category "configuration")
    (exploit-available FALSE)
  )
  (vulnerability
    (id "VULN-400")
    (asset "APP-100")
    (severity "critical")
    (score 97)
    (category "privilege-escalation")
    (exploit-available FALSE)
  )
  (vulnerability
    (id "VULN-500")
    (asset "APP-100")
    (severity "low")
    (score 35)
    (category "dependency")
    (exploit-available TRUE)
  )
  (vulnerability
    (id "VULN-600")
    (asset "APP-100")
    (severity "informational")
    (score 10)
    (category "posture")
    (exploit-available FALSE)
  )
  (vulnerability
    (id "VULN-700")
    (asset "APP-300")
    (severity "low")
    (score 20)
    (category "routine")
    (exploit-available FALSE)
  )
)`,
		Walkthrough: []string{
			"`deffacts` names a group of initial facts.",
			"`asset` facts describe systems that can be affected by vulnerabilities.",
			"`accepted-risk` marks `VULN-200` as an exception with a compensating-control reason.",
			"`vulnerability` facts are the findings the response rules will inspect.",
			"`VULN-100` is a critical exploitable finding on an internet-facing critical asset. Later, that should become an emergency response.",
			"`VULN-300` is a medium finding on a low-criticality internal asset. Later, that should become a standard remediation action.",
			"`VULN-400`, `VULN-500`, `VULN-600`, and `VULN-700` give the conditional-element examples separate findings so their outputs do not overwrite each other.",
		},
		Task:        "Add this `deffacts` block after the templates, then run checks. The source still will not print response output because there are no rules yet.",
		Expected:    "setup: facts",
		Hint:        "Facts must use template names and slot names that already exist. If a fact fails to load, compare its slots with the templates from step 1.",
		Explanation: "This step introduces working-memory data: rules match facts that are already in the session or facts that earlier rules assert.",
	},
	{
		Number:  3,
		Title:   "Add queries",
		Concept: "A query is a named read model. Go code calls a query to read facts from the session after rules have run.",
		Why:     "The tutorial program does not inspect all working memory directly. It asks for remediation actions by lane and for critical vulnerability summaries through explicit queries.",
		Example: `(defquery actions-by-lane
  (declare (variables ?lane))
  ?action <- (remediation-action
    (lane ?lane)
    (target ?target)
    (reason ?reason)
  )
  (return
    (target ?target)
    (reason ?reason)
  )
)

(defquery critical-summaries ?summary <-
  (critical-vulnerability-summary (severity ?severity) (count ?count) (total ?total))
  (return
    (severity ?severity)
    (count ?count)
    (total ?total)
  )
)`,
		Walkthrough: []string{
			"`defquery` names something Go can call with `QueryAll`.",
			"`(declare (variables ?lane))` makes `?lane` an input parameter for the `actions-by-lane` query.",
			"`?action <-` binds the matching `remediation-action` fact so the query can return fields from it.",
			"`target` is the thing the action applies to. Most rules return a vulnerability id; the `forall` rule returns an asset id.",
			"`return` names the fields that Go will see in each query row.",
			"`critical-summaries` has no input variables. It returns every `critical-vulnerability-summary` fact.",
			"Queries do not create facts. They only read facts that are already in the session.",
		},
		Task:        "Add these queries after the facts. Keep them near the bottom of the file; the following steps will insert rules before the queries.",
		Expected:    "setup: queries",
		Hint:        "If a query refers to a template that does not exist, go back to step 1 and check the derived templates.",
		Explanation: "This step connects the ruleset to Go code: rules derive facts, and queries expose selected facts to the host application.",
	},
	{
		Number:  4,
		Title:   "Add an emergency rule",
		Concept: "Rules derive new facts from existing facts. The conditions before => describe the facts Gess must match. The actions after => describe what Gess should do for each match.",
		Why:     "The file now has vulnerability and asset facts. This rule teaches the core flow: match a finding, join it to an asset, and assert a remediation action that Go can query.",
		Example: `(defrule route-emergency-critical-exploitable
  (vulnerability
    (id ?vulnerability-id)
    (asset ?asset)
    (severity "critical")
    (exploit-available TRUE)
  )
  (asset (id ?asset) (criticality "critical") (exposure "internet"))
  =>
  (assert (remediation-action
    (target ?vulnerability-id)
    (lane "emergency")
    (reason "critical-exploitable-internet")
  )
  )
)`,
		Walkthrough: []string{
			"`defrule` names the rule. Rule names are useful in generated code, diagnostics, and tests.",
			"`(vulnerability ...)` matches one finding and binds the vulnerability id and asset id.",
			"`(severity \"critical\")` and `(exploit-available TRUE)` are literal constraints.",
			"`(asset (id ?asset) ...)` is a join. It reuses the asset id from the vulnerability, so the rule only continues for the matching asset fact.",
			"`assert` adds a derived `remediation-action` fact to the session. The target is the vulnerability id because this response applies to one finding.",
		},
		Task:        "Insert this rule before the query definitions, run checks, and confirm that the emergency action appears. You can type it yourself or use Insert example.",
		Expected:    "emergency: VULN-100 critical-exploitable-internet",
		Hint:        "Start with the vulnerability condition from the example. The asset variable you bind there is reused by the asset condition.",
		Explanation: "This step introduces template matching, field variables, joins across templates, numeric tests, and assert actions.",
	},
	{
		Number:  5,
		Title:   "Route accepted risk",
		Concept: "Variables make conditions relate to each other. Once a variable is bound by an earlier condition, later conditions must use the same value.",
		Why:     "An accepted-risk record should route the finding to the accepted-risk lane regardless of its severity or asset.",
		Example: `(defrule route-accepted-risk
  (vulnerability (id ?vulnerability-id))
  (accepted-risk (vulnerability ?vulnerability-id) (reason ?reason))
  =>
  (assert (remediation-action
    (target ?vulnerability-id)
    (lane "accepted-risk")
    (reason ?reason)
  )
  )
)`,
		Walkthrough: []string{
			"`(vulnerability ...)` binds the finding id.",
			"`(accepted-risk (vulnerability ?vulnerability-id) ...)` reuses that id. This is the join.",
			"`?reason` captures the exception reason from the accepted-risk fact.",
			"The asserted remediation action uses the vulnerability id as its target and copies the reason from the exception.",
		},
		Task:        "Add this rule before the queries, then run checks. The accepted-risk lane should appear without changing any Go code.",
		Expected:    "accepted-risk: VULN-200 compensating-control",
		Hint:        "The reason in the derived remediation action should come from `(accepted-risk ... (reason ?reason))`.",
		Explanation: "This step shows how derived facts can preserve context from the source facts that caused the rule to fire.",
	},
	{
		Number:  6,
		Title:   "Use and",
		Concept: "`and` groups conditions that must all be true. Top-level rule conditions already behave like an implicit `and`, but the explicit form is useful when you need to group conditions inside larger expressions.",
		Why:     "This rule routes a critical vulnerability on a critical asset when there is no known exploit. It uses `and` to make the grouping visible before the next step introduces alternatives with `or`.",
		Example: `(defrule route-critical-nonexploited
  (and
    (vulnerability
      (id ?vulnerability-id)
      (asset ?asset)
      (severity "critical")
      (score ?score)
      (exploit-available FALSE)
    )
    (asset (id ?asset) (criticality "critical"))
    (test (< ?score 98))
  )
  =>
  (assert (remediation-action
    (target ?vulnerability-id)
    (lane "and")
    (reason "critical-nonexploited")
  )
  )
)`,
		Walkthrough: []string{
			"`and` contains child conditions. Every child must match for the rule to fire.",
			"The vulnerability condition binds `?vulnerability-id`, `?asset`, and `?score`.",
			"The asset condition joins the vulnerability to a critical asset.",
			"The test keeps the rule focused on scores below the emergency example.",
			"`VULN-400` matches because it is a critical non-exploited finding on `APP-100` with score 97.",
		},
		Task:        "Add this rule before the queries, then run checks. The output should include one `and` lane action for VULN-400.",
		Expected:    "and: VULN-400 critical-nonexploited",
		Hint:        "The explicit `and` wraps the same kinds of conditions you have already used: fact patterns and a test.",
		Explanation: "This step teaches explicit conjunction. Most simple rules can omit `and`, but it becomes useful when rules contain nested conditional elements.",
	},
	{
		Number:  7,
		Title:   "Use or",
		Concept: "`or` creates alternatives. A rule can fire when any branch inside the `or` matches.",
		Why:     "Some response policy has more than one acceptable shape. This rule catches low-score dependency findings or exposure findings with one rule.",
		Example: `(defrule route-watchlist-dependency-or-exposure
  (vulnerability
    (id ?vulnerability-id)
    (category ?category)
    (score ?score)
  )
  (or
    (and
      (test (= ?category "dependency"))
      (test (< ?score 40))
    )
    (test (= ?category "exposure"))
  )
  =>
  (assert (remediation-action
    (target ?vulnerability-id)
    (lane "or")
    (reason "dependency-or-exposure-watch")
  )
  )
)`,
		Walkthrough: []string{
			"The vulnerability condition binds `?vulnerability-id`, `?category`, and `?score` before the `or` runs.",
			"`or` has two branches. The first branch is an `and`; the second branch is a single test.",
			"The first branch matches dependency findings below score 40.",
			"The second branch matches exposure findings.",
			"The action can use `?vulnerability-id` because it was bound before the `or` condition.",
			"`VULN-500` matches the first branch because it is a dependency finding with score 35.",
		},
		Task:        "Add this rule and run checks. The output should include one `or` lane action for VULN-500.",
		Expected:    "or: VULN-500 dependency-or-exposure-watch",
		Hint:        "Bind variables you need in the action before the `or`, then use `or` for the alternative checks.",
		Explanation: "This step teaches alternatives. Use `or` when several different fact shapes should lead to the same action.",
	},
	{
		Number:  8,
		Title:   "Use exists",
		Concept: "`exists` succeeds when at least one fact matches its child condition. It checks for presence without producing one activation per matching fact.",
		Why:     "This rule routes an asset when at least one critical vulnerability exists for that asset. The output is one asset-level action, not one action per critical vulnerability.",
		Example: `(defrule route-asset-with-critical-history
  (asset (id ?asset) (criticality "critical"))
  (exists
    (vulnerability
      (asset ?asset)
      (severity "critical")
    )
  )
  =>
  (assert (remediation-action
    (target ?asset)
    (lane "exists")
    (reason "asset-has-critical")
  )
  )
)`,
		Walkthrough: []string{
			"The first condition chooses a critical asset and binds its id as `?asset`.",
			"`exists` checks for at least one critical vulnerability on that same asset.",
			"The critical vulnerability inside `exists` is evidence. It proves the asset has critical vulnerability history, but it does not become the output target.",
			"`APP-100` matches because it has critical vulnerabilities.",
			"The rule asserts `target ?asset`, so the result is one action for `APP-100`.",
		},
		Task:        "Add this rule and run checks. The output should include one `exists` lane action for APP-100.",
		Expected:    "exists: APP-100 asset-has-critical",
		Hint:        "Bind the asset first, use that asset as the `exists` filter, and assert the asset id as the target.",
		Explanation: "This step teaches presence checks. Use `exists` when one or more matching facts should count as a single condition.",
	},
	{
		Number:  9,
		Title:   "Use forall",
		Concept: "`forall` succeeds when every fact in a domain also satisfies a requirement.",
		Why:     "This rule routes an asset only when every vulnerability on that asset is below a score limit. The output is one asset-level action, not one action per vulnerability.",
		Example: `(defrule route-asset-all-vulns-under-limit
  (asset (id ?asset) (criticality "low"))
  (forall
    (vulnerability
      (asset ?asset)
      (score ?score)
    )
    (test (< ?score 70))
  )
  =>
  (assert (remediation-action
    (target ?asset)
    (lane "forall")
    (reason "asset-under-limit")
  )
  )
)`,
		Walkthrough: []string{
			"The first condition chooses a low-criticality asset and binds its id as `?asset`.",
			"The `forall` domain is every vulnerability for the same asset, with each matching score bound as `?score`.",
			"The requirement tests each domain vulnerability's score.",
			"The condition succeeds only if every matching vulnerability has a score below 70.",
			"`APP-300` matches because both vulnerabilities on that asset are below that limit.",
			"The rule asserts `target ?asset`, so the result is one action for `APP-300`.",
		},
		Task:        "Add this rule and run checks. The output should include one `forall` lane action for APP-300.",
		Expected:    "forall: APP-300 asset-under-limit",
		Hint:        "Bind the asset first, use that asset as the `forall` domain filter, and assert the asset id as the target.",
		Explanation: "This step teaches universal checks. Use `forall` when a decision depends on all matching facts meeting a condition.",
	},
	{
		Number:  10,
		Title:   "Use negation",
		Concept: "A `not` condition succeeds when no matching fact exists. It is how a rule says that something must be absent.",
		Why:     "A low-criticality internal asset can use the standard remediation lane unless the finding already has accepted risk.",
		Example: `(defrule route-standard-remediation
  (vulnerability (id ?vulnerability-id) (asset ?asset) (category "configuration"))
  (asset (id ?asset) (criticality "low"))
  (not (accepted-risk (vulnerability ?vulnerability-id)))
  =>
  (assert (remediation-action
    (target ?vulnerability-id)
    (lane "standard")
    (reason "normal-remediation")
  )
  )
)`,
		Walkthrough: []string{
			"The first condition binds a configuration vulnerability, and the second proves that its asset has low criticality.",
			"`(not (accepted-risk (vulnerability ?vulnerability-id)))` checks for the absence of an accepted-risk exception for that finding.",
			"The `not` condition uses `?vulnerability-id`, so it must appear after another condition has bound that variable.",
			"`VULN-300` passes because it is a configuration finding on low-criticality asset `APP-300` and has no accepted-risk exception.",
		},
		Task:        "Add this rule and run checks. The standard lane should include VULN-300.",
		Expected:    "standard: VULN-300 normal-remediation",
		Hint:        "Put the not condition after `?vulnerability-id` is already bound by an earlier condition.",
		Explanation: "This step introduces absence checks and shows why condition order matters for readable rules.",
	},
	{
		Number:  11,
		Title:   "Add an aggregate",
		Concept: "`accumulate` turns many matching facts into aggregate values such as count and sum.",
		Why:     "Some response data is a summary, not one output per input fact. This rule derives one critical vulnerability summary from all critical findings in the session.",
		Example: `(defrule summarize-critical-vulnerabilities
  (accumulate
    (vulnerability (severity "critical") (score ?score))
    (bind ?count (count))
    (bind ?total (sum ?score))
  )
  =>
  (assert (critical-vulnerability-summary
    (severity "critical")
    (count ?count)
    (total ?total)
  )
  )
)`,
		Walkthrough: []string{
			"`accumulate` starts with an input pattern. Here it scans vulnerability facts where severity is critical.",
			"`?score` is bound for each matching vulnerability.",
			"`(bind ?count (count))` stores the number of matching facts.",
			"`(bind ?total (sum ?score))` stores the sum of all matched scores.",
			"The rule asserts one `critical-vulnerability-summary` fact, which the Go program reads through `critical-summaries`.",
		},
		Task:        "Add this aggregate rule and run checks. The summary should count two critical findings and total their scores.",
		Expected:    "summary: critical count=2 total=195",
		Hint:        "The aggregate input should be `(vulnerability (severity \"critical\") (score ?score))`.",
		Explanation: "This step shows how rules can derive summary facts, not only one output per input match.",
	},
	{
		Number:  12,
		Title:   "Call host code",
		Concept: "Rules can call Go functions registered by the host application. Use this for integration behavior that belongs outside the ruleset.",
		Why:     "The ruleset decides that an emergency response exists. The Go app records that decision through a callback named `record-emergency`.",
		Example: `(defrule notify-emergency-response
  (remediation-action (lane "emergency") (target ?target) (reason ?reason))
  =>
  (call record-emergency ?target ?reason)
)`,
		Walkthrough: []string{
			"The condition matches the derived `remediation-action` fact from checkpoint 4.",
			"`(lane \"emergency\")` limits the callback to emergency responses.",
			"`?target` and `?reason` capture values from the action fact.",
			"`call` invokes the Go function registered as `record-emergency` in the tutorial runner.",
			"The callback is an effect; the rule decision still stays in `.gess`.",
		},
		Task:        "Add this final rule, run checks, and then save the completed rules.gess if you want the file updated on disk.",
		Expected:    "recorded: VULN-100/critical-exploitable-internet",
		Hint:        "The callback is already registered by the tutorial app. The rule only needs `(call record-emergency ?target ?reason)`.",
		Explanation: "This step separates rule decisions from application side effects.",
	},
}

type tutorialStateResponse struct {
	Current  string    `json:"current"`
	Starter  string    `json:"starter"`
	Solution string    `json:"solution"`
	Steps    []webStep `json:"steps"`
}

type runRequest struct {
	Source string `json:"source"`
}

type runResponse struct {
	OK        bool   `json:"ok"`
	Output    string `json:"output"`
	Error     string `json:"error,omitempty"`
	Complete  []int  `json:"complete"`
	Next      int    `json:"next,omitempty"`
	Generated bool   `json:"generated"`
}

type saveResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (a app) serve(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/api/tutorial", a.handleTutorial)
	mux.HandleFunc("/api/run", a.handleRun)
	mux.HandleFunc("/api/save", a.handleSave)
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	fmt.Fprintf(a.out, "Gess tutorial web UI: http://%s\n", addr)
	fmt.Fprintln(a.out, "Press Ctrl+C to stop the server.")
	err := server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	_ = ctx
	return err
}

func (a app) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, tutorialHTML)
}

func (a app) handleTutorial(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	current, err := os.ReadFile(filepath.Join(a.root, "tutorial/vulnerability_response/rules.gess"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, saveResponse{Error: err.Error()})
		return
	}
	starter, err := os.ReadFile(filepath.Join(a.root, "tutorial/vulnerability_response/starter/rules.gess"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, saveResponse{Error: err.Error()})
		return
	}
	solution, err := os.ReadFile(filepath.Join(a.root, "tutorial/vulnerability_response/solution/rules.gess"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, saveResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, tutorialStateResponse{
		Current:  string(current),
		Starter:  string(starter),
		Solution: string(solution),
		Steps:    webSteps,
	})
}

func (a app) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req runRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, runResponse{Error: err.Error()})
		return
	}
	output, err := runTutorialSource(r.Context(), []byte(req.Source))
	if err != nil {
		writeJSON(w, http.StatusOK, runResponse{OK: false, Error: err.Error()})
		return
	}
	progress := evaluateProgress(output, checkpoints)
	complete := make([]int, 0, len(progress.Complete))
	for _, checkpoint := range progress.Complete {
		complete = append(complete, checkpoint.Number)
	}
	next := 0
	if progress.Next != nil {
		next = progress.Next.Number
	}
	writeJSON(w, http.StatusOK, runResponse{
		OK:       true,
		Output:   output,
		Complete: complete,
		Next:     next,
	})
}

func (a app) handleSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req runRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, saveResponse{Error: err.Error()})
		return
	}
	if _, err := runTutorialSource(r.Context(), []byte(req.Source)); err != nil {
		writeJSON(w, http.StatusOK, saveResponse{OK: false, Error: err.Error()})
		return
	}
	if err := os.WriteFile(filepath.Join(a.root, "tutorial/vulnerability_response/rules.gess"), []byte(req.Source), 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, saveResponse{OK: false, Error: err.Error()})
		return
	}
	cmd := exec.CommandContext(r.Context(), "go", "generate", exercisePackage)
	cmd.Dir = a.root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		writeJSON(w, http.StatusOK, saveResponse{OK: false, Error: strings.TrimSpace(stderr.String())})
		return
	}
	writeJSON(w, http.StatusOK, saveResponse{OK: true})
}

func runTutorialSource(ctx context.Context, source []byte) (string, error) {
	doc, err := dsl.Parse("tutorial-editor.gess", source)
	if err != nil {
		return "", err
	}
	recorded := make([]string, 0, 1)
	workspace := sess.NewWorkspace()
	if err := dsl.Load(ctx, workspace, doc, dsl.Registry{
		Calls: map[string]dsl.CallFunc{
			"record-emergency": func(_ rules.ActionContext, args []rules.Value) error {
				if len(args) != 2 {
					return fmt.Errorf("record-emergency: got %d args, want 2", len(args))
				}
				target, ok := args[0].AsString()
				if !ok {
					return fmt.Errorf("record-emergency: target arg is not a string")
				}
				reason, ok := args[1].AsString()
				if !ok {
					return fmt.Errorf("record-emergency: reason arg is not a string")
				}
				recorded = append(recorded, target+"/"+reason)
				return nil
			},
		},
	}); err != nil {
		return "", err
	}
	ruleset, err := workspace.Compile(ctx)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	writeSetupProgress(&out, ruleset, doc)
	session, err := sess.New(ruleset, sess.WithInitialFacts(dsl.InitialFacts(doc)...))
	if err != nil {
		return "", err
	}
	defer session.Close()
	if _, err := session.Run(ctx); err != nil {
		return "", err
	}
	for _, lane := range []string{"emergency", "accepted-risk", "and", "or", "exists", "forall", "standard"} {
		rows, err := session.QueryAll(ctx, "actions-by-lane", sess.QueryArgs{"lane": lane})
		if errors.Is(err, sess.ErrQueryNotFound) {
			continue
		}
		if err != nil {
			return "", err
		}
		for _, row := range rows {
			fmt.Fprintf(&out, "%s: %s %s\n", lane, queryString(row, "target"), queryString(row, "reason"))
		}
	}
	summaries, err := session.QueryAll(ctx, "critical-summaries", nil)
	if errors.Is(err, sess.ErrQueryNotFound) {
		summaries = nil
		err = nil
	}
	if err != nil {
		return "", err
	}
	for _, row := range summaries {
		fmt.Fprintf(&out, "summary: %s count=%d total=%d\n", queryString(row, "severity"), queryInt(row, "count"), queryInt(row, "total"))
	}
	for _, entry := range recorded {
		fmt.Fprintf(&out, "recorded: %s\n", entry)
	}
	return out.String(), nil
}

func writeSetupProgress(out *bytes.Buffer, ruleset *rules.Ruleset, doc *dsl.Document) {
	if hasTemplates(ruleset, "vulnerability", "asset", "accepted-risk", "remediation-action", "critical-vulnerability-summary") {
		fmt.Fprintln(out, "setup: templates")
	}
	if len(dsl.InitialFacts(doc)) >= 11 {
		fmt.Fprintln(out, "setup: facts")
	}
	if hasQueries(ruleset, "actions-by-lane", "critical-summaries") {
		fmt.Fprintln(out, "setup: queries")
	}
}

func hasTemplates(ruleset *rules.Ruleset, names ...string) bool {
	if ruleset == nil {
		return false
	}
	for _, name := range names {
		if _, ok := ruleset.Template(name); !ok {
			return false
		}
	}
	return true
}

func hasQueries(ruleset *rules.Ruleset, names ...string) bool {
	if ruleset == nil {
		return false
	}
	for _, name := range names {
		if _, ok := ruleset.Query(name); !ok {
			return false
		}
	}
	return true
}

func queryString(row sess.QueryRow, alias string) string {
	value, _ := row.Value(alias)
	out, _ := value.AsString()
	return out
}

func queryInt(row sess.QueryRow, alias string) int64 {
	value, _ := row.Value(alias)
	out, _ := value.AsInt64()
	return out
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

const tutorialHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Gess tutorial</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f7f8fa;
      --panel: #ffffff;
      --text: #1f2328;
      --muted: #667085;
      --line: #d7dce2;
      --accent: #0b6bcb;
      --accent-strong: #064f9e;
      --ok: #157347;
      --warn: #9a6700;
      --bad: #b42318;
      --code: #101828;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      color: var(--text);
      background: var(--bg);
    }
    .shell {
      display: grid;
      grid-template-columns: 280px minmax(420px, 1.05fr) minmax(500px, 1.2fr);
      min-height: 100vh;
    }
    aside, main, section.editor {
      min-width: 0;
      border-right: 1px solid var(--line);
    }
    aside {
      background: #eef2f6;
      padding: 18px 14px;
    }
    main {
      background: var(--panel);
      padding: 22px;
      overflow: auto;
    }
    section.editor {
      display: grid;
      grid-template-rows: auto 1fr auto 220px;
      background: #fbfcfd;
      border-right: 0;
      min-height: 100vh;
    }
    h1, h2, h3, p { margin-top: 0; }
    h1 { font-size: 20px; margin-bottom: 4px; }
    h2 { font-size: 22px; margin-bottom: 10px; }
    h3 { font-size: 14px; margin-bottom: 8px; }
    p { line-height: 1.5; }
    .muted { color: var(--muted); font-size: 13px; }
    .steps {
      display: grid;
      gap: 8px;
      margin-top: 18px;
    }
    .step {
      width: 100%;
      text-align: left;
      border: 1px solid var(--line);
      background: #fff;
      color: var(--text);
      border-radius: 8px;
      padding: 10px;
      cursor: pointer;
      font: inherit;
    }
    .step:hover, .step.active { border-color: var(--accent); }
    .step.done { border-color: #8fc5a8; background: #f0faf4; }
    .step .label { display: block; font-size: 12px; color: var(--muted); }
    .step .title { display: block; font-weight: 650; margin-top: 2px; }
    .toolbar {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 8px;
      padding: 12px;
      border-bottom: 1px solid var(--line);
      background: #fff;
    }
    .lesson-nav {
      display: flex;
      gap: 8px;
      margin: 16px 0 0;
    }
    button {
      border: 1px solid var(--line);
      background: #fff;
      color: var(--text);
      border-radius: 8px;
      padding: 8px 10px;
      font: inherit;
      cursor: pointer;
    }
    button.primary {
      background: var(--accent);
      border-color: var(--accent);
      color: #fff;
    }
    button.primary:hover { background: var(--accent-strong); }
    button:hover { border-color: var(--accent); }
    .editor-wrap {
      min-height: 0;
      padding: 12px;
    }
    textarea {
      width: 100%;
      height: 100%;
      min-height: 420px;
      resize: none;
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 14px;
      font: 13px/1.45 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      color: var(--code);
      background: #fff;
      tab-size: 2;
    }
    .status {
      display: flex;
      gap: 12px;
      align-items: center;
      padding: 0 12px 12px;
      color: var(--muted);
      font-size: 13px;
    }
    .dot {
      width: 10px;
      height: 10px;
      border-radius: 50%;
      background: var(--warn);
      display: inline-block;
    }
    .dot.ok { background: var(--ok); }
    .dot.bad { background: var(--bad); }
    pre {
      margin: 0;
      white-space: pre-wrap;
      overflow: auto;
      border-top: 1px solid var(--line);
      padding: 12px;
      font: 13px/1.45 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      background: #111827;
      color: #e5e7eb;
    }
    pre.example {
      border: 1px solid var(--line);
      border-radius: 8px;
      margin: 10px 0 14px;
      max-height: 360px;
      background: #0f172a;
    }
    ol.walkthrough {
      padding-left: 22px;
      line-height: 1.55;
    }
    ol.walkthrough li {
      margin-bottom: 8px;
    }
    code {
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      font-size: 0.95em;
      background: #eef2f6;
      border: 1px solid #dce2e8;
      border-radius: 4px;
      padding: 0 3px;
    }
    .callout {
      border-left: 4px solid var(--accent);
      background: #f2f7fd;
      padding: 12px;
      margin: 14px 0;
    }
    .expected {
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 10px;
      background: #fafafa;
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      font-size: 13px;
    }
    .hidden { display: none; }
    @media (max-width: 1100px) {
      .shell { grid-template-columns: 240px 1fr; }
      section.editor { grid-column: 1 / -1; min-height: 640px; }
    }
    @media (max-width: 760px) {
      .shell { display: block; }
      aside, main, section.editor { border-right: 0; border-bottom: 1px solid var(--line); }
      section.editor { min-height: 720px; }
    }
  </style>
</head>
<body>
  <div class="shell">
    <aside>
      <h1>Gess tutorial</h1>
      <p class="muted">Write rules in the editor, run them, and complete each checkpoint.</p>
      <div id="progress" class="muted">Loading...</div>
      <div id="steps" class="steps"></div>
    </aside>
    <main>
      <h2 id="step-title"></h2>
      <p id="step-concept"></p>
      <div class="callout">
        <h3>Why this matters</h3>
        <p id="step-why"></p>
      </div>
      <div id="example-section">
        <h3>Example</h3>
        <pre id="step-example" class="example"></pre>
      </div>
      <h3>Walkthrough</h3>
      <ol id="step-walkthrough" class="walkthrough"></ol>
      <div id="task-section">
        <h3>Try it</h3>
        <p id="step-task"></p>
      </div>
      <div id="expected-section">
        <h3>Expected output for this checkpoint</h3>
        <div id="step-expected" class="expected"></div>
      </div>
      <div id="hint-section" class="callout">
        <h3>Hint</h3>
        <p id="step-hint"></p>
      </div>
      <div id="learning-section">
        <h3>What you are learning</h3>
        <p id="step-explanation"></p>
      </div>
      <div class="lesson-nav">
        <button id="previous-step">Previous</button>
        <button id="insert-step" class="primary">Insert example</button>
        <button id="next-step">Next</button>
      </div>
    </main>
    <section class="editor">
      <div class="toolbar">
        <button id="run" class="primary">Run checks</button>
        <button id="save">Save to rules.gess</button>
        <button id="starter">Load starter</button>
        <button id="solution">Load solution</button>
      </div>
      <div class="editor-wrap">
        <textarea id="source" spellcheck="false"></textarea>
      </div>
      <div class="status"><span id="dot" class="dot"></span><span id="message">Not run yet.</span></div>
      <pre id="output"></pre>
    </section>
  </div>
  <script>
    const state = { steps: [], source: "", starter: "", solution: "", complete: new Set(), active: 0 };
    const el = (id) => document.getElementById(id);

    async function api(path, options) {
      const res = await fetch(path, options);
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || res.statusText);
      return data;
    }

    function renderSteps() {
      const steps = el("steps");
      steps.innerHTML = "";
      state.steps.forEach((step) => {
        const button = document.createElement("button");
        button.className = "step" + (state.complete.has(step.number) ? " done" : "") + (state.active === step.number ? " active" : "");
        const label = step.number === 0 ? "Start" : "Checkpoint " + step.number;
        button.innerHTML = "<span class=\"label\">" + label + "</span><span class=\"title\">" + step.title + "</span>";
        button.addEventListener("click", () => {
          state.active = step.number;
          render();
        });
        steps.appendChild(button);
      });
    }

    function renderDetail() {
      const step = state.steps.find((item) => item.number === state.active) || state.steps[0];
      if (!step) return;
      el("step-title").textContent = step.number === 0 ? step.title : "Checkpoint " + step.number + ": " + step.title;
      el("step-concept").textContent = step.concept;
      el("step-why").textContent = step.why;
      el("step-example").textContent = step.example;
      el("example-section").className = step.example ? "" : "hidden";
      const walkthrough = el("step-walkthrough");
      walkthrough.innerHTML = "";
      step.walkthrough.forEach((item) => {
        const li = document.createElement("li");
        li.innerHTML = renderInlineCode(item);
        walkthrough.appendChild(li);
      });
      el("step-task").textContent = step.task;
      el("task-section").className = step.task ? "" : "hidden";
      el("step-expected").textContent = step.expected;
      el("expected-section").className = step.expected ? "" : "hidden";
      el("step-hint").textContent = step.hint;
      el("hint-section").className = step.hint ? "callout" : "hidden";
      el("step-explanation").textContent = step.explanation;
      el("learning-section").className = step.explanation ? "" : "hidden";
      el("insert-step").hidden = !step.example;
    }

    function renderInlineCode(text) {
      const tick = String.fromCharCode(96);
      const pattern = new RegExp(tick + "([^" + tick + "]+)" + tick, "g");
      return text.replace(pattern, (_, code) => "<code>" + escapeHTML(code) + "</code>");
    }

    function escapeHTML(text) {
      return text
        .replaceAll("&", "&amp;")
        .replaceAll("<", "&lt;")
        .replaceAll(">", "&gt;")
        .replaceAll("\"", "&quot;");
    }

    function renderProgress() {
      el("progress").textContent = state.complete.size + "/" + checkpointCount() + " checkpoints complete";
    }

    function checkpointCount() {
      return state.steps.filter((step) => step.number > 0).length;
    }

    function render() {
      renderSteps();
      renderDetail();
      renderProgress();
    }

    function setMessage(kind, text) {
      const dot = el("dot");
      dot.className = "dot" + (kind === "ok" ? " ok" : kind === "bad" ? " bad" : "");
      el("message").textContent = text;
    }

    function activeStep() {
      return state.steps.find((item) => item.number === state.active) || state.steps[0];
    }

    function setSource(value) {
      el("source").value = value;
      localStorage.setItem("gessTutorialSource", value);
    }

    function insertCurrentStep() {
      const step = activeStep();
      if (!step || !step.example) return;
      const source = el("source").value;
      const key = topLevelKey(step.example);
      if (key && source.includes(key)) {
        setMessage("", "This example is already in the editor.");
        return;
      }
      const marker = "\n(defquery ";
      const index = source.indexOf(marker);
      const insertion = "\n\n" + step.example + "\n";
      const shouldInsertBeforeQueries = step.example.startsWith("(defrule ") && index >= 0;
      const next = shouldInsertBeforeQueries
        ? source.slice(0, index) + insertion + source.slice(index)
        : source.trimEnd() + insertion + "\n";
      setSource(next);
      setMessage("", "Inserted checkpoint " + step.number + ". Run checks when ready.");
    }

    function topLevelKey(example) {
      const match = example.match(/^\((def(?:template|facts|query|rule))\s+([^\s)]+)/);
      if (!match) return "";
      return "(" + match[1] + " " + match[2];
    }

    function moveStep(delta) {
      const index = state.steps.findIndex((step) => step.number === state.active);
      const nextIndex = Math.min(state.steps.length - 1, Math.max(0, index + delta));
      state.active = state.steps[nextIndex].number;
      render();
    }

    async function runChecks(options = { advance: true }) {
      setMessage("", "Running...");
      const data = await api("/api/run", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ source: el("source").value })
      });
      if (!data.ok) {
        el("output").textContent = data.error;
        setMessage("bad", "Rules did not compile or run.");
        return;
      }
      state.complete = new Set(data.complete);
      if (options.advance && data.next) state.active = data.next;
      el("output").textContent = data.output || "<empty output>";
      setMessage(data.next ? "" : "ok", data.next ? "Next checkpoint: " + data.next : "All checkpoints complete.");
      render();
    }

    async function saveSource() {
      setMessage("", "Saving...");
      const data = await api("/api/save", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ source: el("source").value })
      });
      if (!data.ok) {
        el("output").textContent = data.error;
        setMessage("bad", "Save failed.");
        return;
      }
      setMessage("ok", "Saved to tutorial/vulnerability_response/rules.gess.");
    }

    async function init() {
      const data = await api("/api/tutorial");
      state.steps = data.steps;
      state.source = data.current;
      state.starter = data.starter;
      state.solution = data.solution;
      el("source").value = localStorage.getItem("gessTutorialSource") || data.current;
      el("source").addEventListener("input", () => localStorage.setItem("gessTutorialSource", el("source").value));
      el("run").addEventListener("click", () => runChecks().catch((err) => setMessage("bad", err.message)));
      el("save").addEventListener("click", () => saveSource().catch((err) => setMessage("bad", err.message)));
      el("insert-step").addEventListener("click", insertCurrentStep);
      el("previous-step").addEventListener("click", () => moveStep(-1));
      el("next-step").addEventListener("click", () => moveStep(1));
      el("starter").addEventListener("click", () => {
        setSource(state.starter);
        state.complete = new Set();
        state.active = 0;
        el("output").textContent = "";
        setMessage("", "Starter loaded in editor.");
        render();
      });
      el("solution").addEventListener("click", () => {
        setSource(state.solution);
        el("output").textContent = "";
        setMessage("", "Solution loaded in editor. Run checks to validate it.");
      });
      render();
      await runChecks({ advance: false });
    }

    init().catch((err) => {
      el("output").textContent = err.message;
      setMessage("bad", "Failed to load tutorial.");
    });
  </script>
</body>
</html>`
