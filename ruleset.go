package gess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

type Workspace struct {
	templates []TemplateSpec
}

func NewWorkspace() *Workspace {
	return &Workspace{}
}

func (w *Workspace) AddTemplate(spec TemplateSpec) error {
	template, err := compileTemplateSpec(spec)
	if err != nil {
		return err
	}

	for _, existing := range w.templates {
		if strings.TrimSpace(existing.Name) == template.name {
			return &ValidationError{
				TemplateName: template.name,
				Reason:       "duplicate template",
			}
		}
	}

	w.templates = append(w.templates, spec.clone())
	return nil
}

func (w *Workspace) Compile(ctx context.Context) (*Ruleset, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	compiled := make([]Template, 0, len(w.templates))
	for _, spec := range w.templates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		template, err := compileTemplateSpec(spec)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, template)
	}

	sort.Slice(compiled, func(i, j int) bool {
		return compiled[i].name < compiled[j].name
	})

	templates := make(map[string]Template, len(compiled))
	order := make([]string, 0, len(compiled))
	for _, template := range compiled {
		if _, exists := templates[template.name]; exists {
			return nil, &ValidationError{
				TemplateName: template.name,
				Reason:       "duplicate template",
			}
		}
		templates[template.name] = template.clone()
		order = append(order, template.name)
	}

	return &Ruleset{
		id:            rulesetID(compiled),
		templates:     templates,
		templateOrder: order,
	}, nil
}

type Ruleset struct {
	id            RulesetID
	templates     map[string]Template
	templateOrder []string
}

func (r *Ruleset) ID() RulesetID {
	if r == nil {
		return ""
	}
	return r.id
}

func (r *Ruleset) Template(name string) (Template, bool) {
	if r == nil {
		return Template{}, false
	}
	template, ok := r.templates[name]
	if !ok {
		return Template{}, false
	}
	return template.clone(), true
}

func (r *Ruleset) Templates() []Template {
	if r == nil {
		return nil
	}
	out := make([]Template, 0, len(r.templateOrder))
	for _, name := range r.templateOrder {
		out = append(out, r.templates[name].clone())
	}
	return out
}

func rulesetID(templates []Template) RulesetID {
	sum := sha256.New()
	sum.Write([]byte("gess/ruleset/v1\n"))
	for _, template := range templates {
		sum.Write(fmt.Appendf(nil, "template:%s:%s:%d\n", template.name, template.key, template.duplicatePolicy))
		for _, field := range template.fields {
			sum.Write(fmt.Appendf(nil, "field:%s:%s:%t\n", field.Name, field.Kind, field.Required))
		}
	}
	return RulesetID("sha256:" + hex.EncodeToString(sum.Sum(nil)))
}
