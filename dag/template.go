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
		env[k] = v.Value
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
	expandedEnv := make(EnvMap, len(d.Env))
	for k, v := range d.Env {
		ev, err := expandString(v.Value, ctx, fmt.Sprintf("env[%s]", k))
		if err != nil {
			return nil, err
		}
		expandedEnv[k] = EnvVar{Value: ev, Secret: v.Secret}
	}
	expanded.Env = expandedEnv

	for i, s := range d.Steps {
		es := s
		// Deep copy mutable fields to avoid sharing with original
		if len(s.Depends) > 0 {
			es.Depends = make([]string, len(s.Depends))
			copy(es.Depends, s.Depends)
		}
		if s.Retry != nil {
			r := *s.Retry
			es.Retry = &r
		}
		if s.When != nil {
			w := *s.When
			es.When = &w
		}
		if s.OnSuccess != nil {
			h := *s.OnSuccess
			es.OnSuccess = &h
		}
		if s.OnFailure != nil {
			h := *s.OnFailure
			es.OnFailure = &h
		}
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

		// Expand quarto path
		if s.Quarto != "" {
			v, err := expandString(s.Quarto, ctx, fmt.Sprintf("step %q quarto", s.ID))
			if err != nil {
				return nil, err
			}
			es.Quarto = v
		}

		// Expand R package dev step paths
		for _, field := range []struct {
			val  *string
			name string
		}{
			{&es.Test, "test"}, {&es.Check, "check"}, {&es.Document, "document"},
			{&es.Lint, "lint"}, {&es.Style, "style"},
			{&es.Rmd, "rmd"}, {&es.RenvRestore, "renv_restore"},
			{&es.Coverage, "coverage"}, {&es.Validate, "validate"},
		} {
			src := *field.val
			if src != "" {
				v, err := expandString(src, ctx, fmt.Sprintf("step %q %s", s.ID, field.name))
				if err != nil {
					return nil, err
				}
				*field.val = v
			}
		}

		// Expand connect fields
		if s.Connect != nil {
			ec := *s.Connect
			if ec.Path != "" {
				v, err := expandString(ec.Path, ctx, fmt.Sprintf("step %q connect.path", s.ID))
				if err != nil {
					return nil, err
				}
				ec.Path = v
			}
			if ec.Name != "" {
				v, err := expandString(ec.Name, ctx, fmt.Sprintf("step %q connect.name", s.ID))
				if err != nil {
					return nil, err
				}
				ec.Name = v
			}
			es.Connect = &ec
		}

		// Expand step-level env
		if len(s.Env) > 0 {
			es.Env = make(EnvMap, len(s.Env))
			for k, v := range s.Env {
				ev, err := expandString(v.Value, ctx, fmt.Sprintf("step %q env[%s]", s.ID, k))
				if err != nil {
					return nil, err
				}
				es.Env[k] = EnvVar{Value: ev, Secret: v.Secret}
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
