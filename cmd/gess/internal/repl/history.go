package repl

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

const defaultHistoryLimit = 1000

type replHistory struct {
	entries []string
	limit   int
}

func newReplHistory(limit int) *replHistory {
	if limit <= 0 {
		limit = defaultHistoryLimit
	}
	return &replHistory{limit: limit}
}

func loadReplHistory(path string, limit int) *replHistory {
	h := newReplHistory(limit)
	if path == "" {
		return h
	}
	file, err := os.Open(path)
	if err != nil {
		return h
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		h.add(scanner.Text())
	}
	return h
}

func saveReplHistory(path string, h *replHistory) {
	if path == "" || h == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data := strings.Join(h.entries, "\n")
	if data != "" {
		data += "\n"
	}
	_ = os.WriteFile(path, []byte(data), 0o600)
}

func defaultReplHistoryPath() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "gess", "repl_history")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state", "gess", "repl_history")
}

func (h *replHistory) add(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if len(h.entries) > 0 && h.entries[len(h.entries)-1] == line {
		return
	}
	h.entries = append(h.entries, line)
	if len(h.entries) > h.limit {
		copy(h.entries, h.entries[len(h.entries)-h.limit:])
		h.entries = h.entries[:h.limit]
	}
}

func (h *replHistory) snapshot() []string {
	if h == nil || len(h.entries) == 0 {
		return nil
	}
	out := make([]string, len(h.entries))
	copy(out, h.entries)
	return out
}

type historyNavigator struct {
	history *replHistory
	index   int
	draft   string
}

func newHistoryNavigator(history *replHistory) historyNavigator {
	n := historyNavigator{history: history}
	n.reset("")
	return n
}

func (n *historyNavigator) reset(draft string) {
	n.draft = draft
	if n.history == nil {
		n.index = 0
		return
	}
	n.index = len(n.history.entries)
}

func (n *historyNavigator) previous(current string) (string, bool) {
	if n.history == nil || len(n.history.entries) == 0 {
		return "", false
	}
	if n.index == len(n.history.entries) {
		n.draft = current
	}
	if n.index <= 0 {
		return n.history.entries[0], true
	}
	n.index--
	return n.history.entries[n.index], true
}

func (n *historyNavigator) next() (string, bool) {
	if n.history == nil || len(n.history.entries) == 0 || n.index >= len(n.history.entries) {
		return "", false
	}
	n.index++
	if n.index == len(n.history.entries) {
		return n.draft, true
	}
	return n.history.entries[n.index], true
}
