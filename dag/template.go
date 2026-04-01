package dag

import (
	"bytes"
	"fmt"
	"text/template"
	"time"
)

// TemplateContext holds the variables available during template expansion.
type TemplateContext struct {
	Today  string
	Now    string
	Params map[string]string
	Env    map[string]string
}

// NewTemplateContext creates a TemplateContext from a DAG and user-provided param overrides.
func NewTemplateContext(d *DAG, paramOverrides map[string]string) TemplateContext {
	now := time.Now()

	params := make(map[string]string)
	for _, p := range d.Params {
		params[p.Name] = p.Default
	}
	for k, v := range paramOverrides {
		params[k] = v
	}

	env := make(map[string]string)
	for k, v := range d.Env {
		env[k] = v
	}

	return TemplateContext{
		Today:  now.Format("2006-01-02"),
		Now:    now.Format(time.RFC3339),
		Params: params,
		Env:    env,
	}
}

// ExpandDAG returns a deep copy of the DAG with all template expressions expanded.
func ExpandDAG(d *DAG, paramOverrides map[string]string) (*DAG, error) {
	ctx := NewTemplateContext(d, paramOverrides)

	expanded := *d
	expanded.Steps = make([]Step, len(d.Steps))

	// Expand DAG-level env
	expandedEnv := make(map[string]string, len(d.Env))
	for k, v := range d.Env {
		ev, err := expandString(v, ctx, fmt.Sprintf("env[%s]", k))
		if err != nil {
			return nil, err
		}
		expandedEnv[k] = ev
	}
	expanded.Env = expandedEnv

	for i, s := range d.Steps {
		es := s
		es.Args = make([]string, len(s.Args))

		// Expand args
		for j, arg := range s.Args {
			v, err := expandString(arg, ctx, fmt.Sprintf("step %q args[%d]", s.ID, j))
			if err != nil {
				return nil, err
			}
			es.Args[j] = v
		}

		// Expand script path
		if s.Script != "" {
			v, err := expandString(s.Script, ctx, fmt.Sprintf("step %q script", s.ID))
			if err != nil {
				return nil, err
			}
			es.Script = v
		}

		// Expand r_expr
		if s.RExpr != "" {
			v, err := expandString(s.RExpr, ctx, fmt.Sprintf("step %q r_expr", s.ID))
			if err != nil {
				return nil, err
			}
			es.RExpr = v
		}

		// Expand command
		if s.Command != "" {
			v, err := expandString(s.Command, ctx, fmt.Sprintf("step %q command", s.ID))
			if err != nil {
				return nil, err
			}
			es.Command = v
		}

		// Expand step-level env
		if len(s.Env) > 0 {
			es.Env = make(map[string]string, len(s.Env))
			for k, v := range s.Env {
				ev, err := expandString(v, ctx, fmt.Sprintf("step %q env[%s]", s.ID, k))
				if err != nil {
					return nil, err
				}
				es.Env[k] = ev
			}
		}

		expanded.Steps[i] = es
	}

	return &expanded, nil
}

func expandString(s string, ctx TemplateContext, location string) (string, error) {
	tmpl, err := template.New("").Parse(s)
	if err != nil {
		return "", fmt.Errorf("template error in %s: %w", location, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("template error in %s: %w", location, err)
	}
	return buf.String(), nil
}
