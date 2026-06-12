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
		existingKey := TemplateKey(strings.TrimSpace(string(existing.Key)))
		if existingKey == "" {
			existingKey = TemplateKey(strings.TrimSpace(existing.Name))
		}
		if existingKey == template.key {
			return &ValidationError{
				TemplateName: template.name,
				Reason:       "duplicate template key",
			}
		}
	}

	w.templates = append(w.templates, template.spec())
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
	templatesByKey := make(map[TemplateKey]Template, len(compiled))
	order := make([]string, 0, len(compiled))
	for _, template := range compiled {
		if _, exists := templates[template.name]; exists {
			return nil, &ValidationError{
				TemplateName: template.name,
				Reason:       "duplicate template",
			}
		}
		if _, exists := templatesByKey[template.key]; exists {
			return nil, &ValidationError{
				TemplateName: template.name,
				Reason:       "duplicate template key",
			}
		}
		templates[template.name] = template.clone()
		templatesByKey[template.key] = template.clone()
		order = append(order, template.name)
	}

	return &Ruleset{
		id:             rulesetID(compiled),
		templates:      templates,
		templatesByKey: templatesByKey,
		templateOrder:  order,
	}, nil
}

type Ruleset struct {
	id             RulesetID
	templates      map[string]Template
	templatesByKey map[TemplateKey]Template
	templateOrder  []string
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

func (r *Ruleset) TemplateByKey(key TemplateKey) (Template, bool) {
	if r == nil {
		return Template{}, false
	}
	template, ok := r.templatesByKey[key]
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
		sum.Write(fmt.Appendf(nil, "template:%s:%s:%s:%d:%t\n", template.name, template.key, template.compatibilityKey, template.duplicatePolicy, template.closed))
		sum.Write(fmt.Appendf(nil, "dup:%d:", template.duplicatePolicy))
		sum.Write(fmt.Appendf(nil, "%d\n", len(template.duplicateKeyNames)))
		for _, fieldName := range template.duplicateKeyNames {
			sum.Write(fmt.Appendf(nil, "dupkey:%s\n", fieldName))
		}
		for _, field := range template.fields {
			sum.Write(fmt.Appendf(nil, "field:%s:%s:%t", field.Name, field.Kind, field.Required))
			if fieldDefault, hasDefault := template.fieldDefaults[field.Name]; hasDefault {
				sum.Write(fmt.Appendf(nil, ":default:%s", fieldDefault.canonicalKey()))
			}
			sum.Write([]byte("\n"))
			if allowed, hasAllowed := template.fieldAllowed[field.Name]; hasAllowed {
				sum.Write(fmt.Appendf(nil, "allowed:%s:", field.Name))
				for _, allowedValue := range allowed {
					sum.Write([]byte(allowedValue.canonicalKey()))
					sum.Write([]byte(","))
				}
				sum.Write([]byte("\n"))
			}
		}
	}
	return RulesetID("sha256:" + hex.EncodeToString(sum.Sum(nil)))
}
