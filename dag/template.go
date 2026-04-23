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

	env, err := expandEnvMap(d.Env, ctx, "env")
	if err != nil {
		return nil, err
	}
	expanded.Env = env

	for i, s := range d.Steps {
		es, err := expandStep(s, ctx)
		if err != nil {
			return nil, err
		}
		expanded.Steps[i] = es
	}

	return &expanded, nil
}

// expandStep produces a template-expanded deep copy of a single step. All
// mutable nested structs (Depends, Retry, When, OnSuccess, OnFailure, Connect,
// Env) are cloned so the caller can mutate the result without touching the
// original DAG.
func expandStep(s Step, ctx TemplateContext) (Step, error) {
	es := s
	cloneStepPointers(&es, s)
	es.Args = make([]string, len(s.Args))

	for j, arg := range s.Args {
		v, err := expandString(arg, ctx, fmt.Sprintf("step %q args[%d]", s.ID, j))
		if err != nil {
			return Step{}, err
		}
		es.Args[j] = v
	}

	fields := []struct {
		val  *string
		name string
	}{
		{&es.Script, "script"}, {&es.RExpr, "r_expr"},
		{&es.Command, "command"}, {&es.Quarto, "quarto"},
		{&es.Test, "test"}, {&es.Check, "check"}, {&es.Document, "document"},
		{&es.Lint, "lint"}, {&es.Style, "style"},
		{&es.Rmd, "rmd"}, {&es.RenvRestore, "renv_restore"},
		{&es.Coverage, "coverage"}, {&es.Validate, "validate"},
	}
	for _, f := range fields {
		if err := expandOptional(f.val, ctx, fmt.Sprintf("step %q %s", s.ID, f.name)); err != nil {
			return Step{}, err
		}
	}

	if s.Connect != nil {
		ec, err := expandConnect(s.Connect, ctx, s.ID)
		if err != nil {
			return Step{}, err
		}
		es.Connect = ec
	}

	if s.Email != nil {
		ee, err := expandEmail(s.Email, ctx, s.ID)
		if err != nil {
			return Step{}, err
		}
		es.Email = ee
	}

	if len(s.Env) > 0 {
		env, err := expandEnvMap(s.Env, ctx, fmt.Sprintf("step %q env", s.ID))
		if err != nil {
			return Step{}, err
		}
		es.Env = env
	}

	return es, nil
}

// cloneStepPointers deep-copies the nested structs on a step so that mutations
// to the expanded copy don't leak back into the source DAG.
func cloneStepPointers(dst *Step, src Step) {
	if len(src.Depends) > 0 {
		dst.Depends = make([]string, len(src.Depends))
		copy(dst.Depends, src.Depends)
	}
	if src.Retry != nil {
		r := *src.Retry
		dst.Retry = &r
	}
	if src.When != nil {
		w := *src.When
		dst.When = &w
	}
	if src.OnSuccess != nil {
		h := *src.OnSuccess
		dst.OnSuccess = &h
	}
	if src.OnFailure != nil {
		h := *src.OnFailure
		dst.OnFailure = &h
	}
}

// expandEnvMap returns a template-expanded deep copy of an EnvMap. locationBase
// is prefixed to per-key error messages ("env[KEY]" or `step "X" env[KEY]`).
func expandEnvMap(src EnvMap, ctx TemplateContext, locationBase string) (EnvMap, error) {
	out := make(EnvMap, len(src))
	for k, v := range src {
		ev, err := expandString(v.Value, ctx, fmt.Sprintf("%s[%s]", locationBase, k))
		if err != nil {
			return nil, err
		}
		out[k] = EnvVar{Value: ev, Secret: v.Secret}
	}
	return out, nil
}

// expandOptional runs template expansion on *field in place, but only if the
// current value is non-empty. Empty-string fields are left untouched so callers
// don't need to pre-check each one.
func expandOptional(field *string, ctx TemplateContext, location string) error {
	if *field == "" {
		return nil
	}
	v, err := expandString(*field, ctx, location)
	if err != nil {
		return err
	}
	*field = v
	return nil
}

// expandEmail returns a deep copy of e with subject, body, body_file, and
// attachment paths template-expanded. Caller has already verified e != nil.
func expandEmail(e *EmailStep, ctx TemplateContext, stepID string) (*EmailStep, error) {
	ee := *e
	if err := expandOptional(&ee.Subject, ctx, fmt.Sprintf("step %q email.subject", stepID)); err != nil {
		return nil, err
	}
	if err := expandOptional(&ee.Body, ctx, fmt.Sprintf("step %q email.body", stepID)); err != nil {
		return nil, err
	}
	if err := expandOptional(&ee.BodyFile, ctx, fmt.Sprintf("step %q email.body_file", stepID)); err != nil {
		return nil, err
	}
	if len(e.Attach) > 0 {
		ee.Attach = make([]string, len(e.Attach))
		for i, a := range e.Attach {
			v, err := expandString(a, ctx, fmt.Sprintf("step %q email.attach[%d]", stepID, i))
			if err != nil {
				return nil, err
			}
			ee.Attach[i] = v
		}
	}
	return &ee, nil
}

// expandConnect returns a deep copy of c with Path and Name template-expanded.
// Caller has already verified c != nil.
func expandConnect(c *ConnectDeploy, ctx TemplateContext, stepID string) (*ConnectDeploy, error) {
	ec := *c
	if err := expandOptional(&ec.Path, ctx, fmt.Sprintf("step %q connect.path", stepID)); err != nil {
		return nil, err
	}
	if err := expandOptional(&ec.Name, ctx, fmt.Sprintf("step %q connect.name", stepID)); err != nil {
		return nil, err
	}
	return &ec, nil
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
