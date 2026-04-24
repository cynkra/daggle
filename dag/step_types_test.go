package dag

import "testing"

func TestStepType(t *testing.T) {
	tests := []struct {
		step Step
		want string
	}{
		{Step{Script: "foo.R"}, "script"},
		{Step{RExpr: "1+1"}, "r_expr"},
		{Step{Command: "echo hi"}, "command"},
		{Step{Quarto: "report.qmd"}, "quarto"},
		{Step{Test: "."}, "test"},
		{Step{Check: "."}, "check"},
		{Step{Document: "."}, "document"},
		{Step{Lint: "."}, "lint"},
		{Step{Style: "."}, "style"},
		{Step{Connect: &ConnectDeploy{Type: "shiny", Path: "app/"}}, "connect"},
		{Step{}, ""},
	}
	for _, tt := range tests {
		if got := StepType(tt.step); got != tt.want {
			t.Errorf("StepType(%+v) = %q, want %q", tt.step, got, tt.want)
		}
	}
}

func TestStepType_NewTypes(t *testing.T) {
	tests := []struct {
		step Step
		want string
	}{
		{Step{Rmd: "report.Rmd"}, "rmd"},
		{Step{RenvRestore: "."}, "renv_restore"},
		{Step{Coverage: "."}, "coverage"},
		{Step{Validate: "rules.R"}, "validate"},
	}
	for _, tt := range tests {
		if got := StepType(tt.step); got != tt.want {
			t.Errorf("StepType(%+v) = %q, want %q", tt.step, got, tt.want)
		}
	}
}

func TestMaxAttempts(t *testing.T) {
	s1 := Step{}
	if got := s1.MaxAttempts(); got != 1 {
		t.Errorf("no retry: MaxAttempts = %d, want 1", got)
	}

	s2 := Step{Retry: &Retry{Limit: 3}}
	if got := s2.MaxAttempts(); got != 4 {
		t.Errorf("retry 3: MaxAttempts = %d, want 4", got)
	}
}
