package dag

import (
	"strings"
	"testing"
)

func TestValidate_NewStepTypes(t *testing.T) {
	// Valid quarto step
	d := &DAG{Name: "t", Steps: []Step{{ID: "a", Quarto: "report.qmd"}}}
	if err := Validate(d); err != nil {
		t.Errorf("quarto step should be valid: %v", err)
	}

	// Valid connect step
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Connect: &ConnectDeploy{Type: "shiny", Path: "app/"}}}}
	if err := Validate(d); err != nil {
		t.Errorf("connect step should be valid: %v", err)
	}

	// Connect missing type
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Connect: &ConnectDeploy{Path: "app/"}}}}
	if err := Validate(d); err == nil {
		t.Error("connect without type should fail")
	}

	// Connect invalid type
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Connect: &ConnectDeploy{Type: "invalid", Path: "app/"}}}}
	if err := Validate(d); err == nil {
		t.Error("connect with invalid type should fail")
	}

	// Connect missing path
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Connect: &ConnectDeploy{Type: "shiny"}}}}
	if err := Validate(d); err == nil {
		t.Error("connect without path should fail")
	}

	// Multiple types should fail
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Script: "foo.R", Quarto: "bar.qmd"}}}
	if err := Validate(d); err == nil {
		t.Error("multiple types should fail")
	}
}

func TestValidate_RetryBackoff(t *testing.T) {
	// Valid exponential
	d := &DAG{Name: "t", Steps: []Step{{ID: "a", Command: "echo", Retry: &Retry{Limit: 2, Backoff: "exponential"}}}}
	if err := Validate(d); err != nil {
		t.Errorf("exponential backoff should be valid: %v", err)
	}

	// Valid with max_delay
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Command: "echo", Retry: &Retry{Limit: 2, Backoff: "exponential", MaxDelay: "30s"}}}}
	if err := Validate(d); err != nil {
		t.Errorf("max_delay should be valid: %v", err)
	}

	// Invalid backoff
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Command: "echo", Retry: &Retry{Limit: 2, Backoff: "quadratic"}}}}
	if err := Validate(d); err == nil {
		t.Error("invalid backoff should fail")
	}

	// Invalid max_delay
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Command: "echo", Retry: &Retry{Limit: 2, MaxDelay: "not-a-duration"}}}}
	if err := Validate(d); err == nil {
		t.Error("invalid max_delay should fail")
	}
}

func TestValidateChannels(t *testing.T) {
	cases := []struct {
		name     string
		hook     *Hook
		channels map[string]bool
		wantErr  string
	}{
		{"no hook", nil, map[string]bool{"slack": true}, ""},
		{"notify matches channel", &Hook{Notify: "slack"}, map[string]bool{"slack": true}, ""},
		{"notify unknown channel", &Hook{Notify: "typo"}, map[string]bool{"slack": true}, `notify channel "typo"`},
		{"notify unknown with empty channels", &Hook{Notify: "x"}, map[string]bool{}, `notify channel "x"`},
		{"command hook (no notify) is not checked", &Hook{Command: "echo"}, map[string]bool{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &DAG{Name: "t", Steps: []Step{{ID: "a", Command: "echo"}}, OnFailure: tc.hook}
			err := ValidateChannels(d, tc.channels)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want contains %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateWithChannels_NilSkipsCheck(t *testing.T) {
	// Passing nil channels must behave like Validate (no channel check).
	d := &DAG{Name: "t", Steps: []Step{{ID: "a", Command: "echo"}}, OnFailure: &Hook{Notify: "unknown"}}
	if err := ValidateWithChannels(d, nil); err != nil {
		t.Errorf("nil channels must skip check, got error: %v", err)
	}
	if err := Validate(d); err != nil {
		t.Errorf("Validate must match nil-channels behavior, got: %v", err)
	}
}

func TestValidate_ApproveTimeout(t *testing.T) {
	cases := []struct {
		name    string
		timeout string
		wantErr string
	}{
		{"empty is valid (no timeout)", "", ""},
		{"valid duration", "24h", ""},
		{"valid short duration", "30m", ""},
		{"invalid English", "5minutes", "approve.timeout"},
		{"invalid with space", "1 hour", "approve.timeout"},
		{"invalid word", "forever", "approve.timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &DAG{Name: "t", Steps: []Step{{ID: "a", Approve: &ApproveStep{Timeout: tc.timeout}}}}
			err := Validate(d)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want contains %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidate_ErrorOn(t *testing.T) {
	// Valid error_on values
	for _, val := range []string{"error", "warning", "message"} {
		d := &DAG{Name: "t", Steps: []Step{{ID: "a", Command: "echo", ErrorOn: val}}}
		if err := Validate(d); err != nil {
			t.Errorf("error_on=%q should be valid: %v", val, err)
		}
	}

	// Invalid error_on
	d := &DAG{Name: "t", Steps: []Step{{ID: "a", Command: "echo", ErrorOn: "panic"}}}
	if err := Validate(d); err == nil {
		t.Error("error_on=panic should fail validation")
	}
}

func TestValidate_RVersion(t *testing.T) {
	// Valid constraints
	for _, v := range []string{">=4.1.0", ">=4.1", "==4.4.1"} {
		d := &DAG{Name: "t", RVersion: v, Steps: []Step{{ID: "a", Command: "echo"}}}
		if err := Validate(d); err != nil {
			t.Errorf("r_version=%q should be valid: %v", v, err)
		}
	}

	// Invalid constraints
	for _, v := range []string{"4.1.0", ">4.1.0", ">=abc"} {
		d := &DAG{Name: "t", RVersion: v, Steps: []Step{{ID: "a", Command: "echo"}}}
		if err := Validate(d); err == nil {
			t.Errorf("r_version=%q should fail validation", v)
		}
	}
}

func TestCheckRVersion(t *testing.T) {
	tests := []struct {
		constraint string
		actual     string
		wantOK     bool
	}{
		{">=4.1.0", "4.4.1", true},
		{">=4.1.0", "4.1.0", true},
		{">=4.1.0", "4.0.5", false},
		{">=4.1", "4.1.0", true},
		{"==4.4.1", "4.4.1", true},
		{"==4.4.1", "4.4.0", false},
		{"", "4.4.1", true},   // empty constraint always passes
		{">=4.1.0", "", true}, // no R version always passes
	}
	for _, tt := range tests {
		_, ok := CheckRVersion(tt.constraint, tt.actual)
		if ok != tt.wantOK {
			t.Errorf("CheckRVersion(%q, %q) = %v, want %v", tt.constraint, tt.actual, ok, tt.wantOK)
		}
	}
}

func TestValidate_Artifacts(t *testing.T) {
	// Valid artifacts
	d := &DAG{Name: "t", Steps: []Step{{
		ID:      "a",
		Command: "echo",
		Artifacts: []Artifact{
			{Name: "output_data", Path: "out/data.csv"},
			{Name: "plot", Path: "out/plot.png", Format: "png"},
		},
	}}}
	if err := Validate(d); err != nil {
		t.Errorf("valid artifacts should pass: %v", err)
	}

	// Invalid: empty name
	d = &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Artifacts: []Artifact{{Name: "", Path: "out/data.csv"}},
	}}}
	if err := Validate(d); err == nil {
		t.Error("empty artifact name should fail")
	}

	// Invalid: bad name pattern
	d = &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Artifacts: []Artifact{{Name: "123bad", Path: "out/data.csv"}},
	}}}
	if err := Validate(d); err == nil {
		t.Error("artifact name starting with digit should fail")
	}

	// Invalid: duplicate name
	d = &DAG{Name: "t", Steps: []Step{{
		ID:      "a",
		Command: "echo",
		Artifacts: []Artifact{
			{Name: "data", Path: "out/a.csv"},
			{Name: "data", Path: "out/b.csv"},
		},
	}}}
	if err := Validate(d); err == nil {
		t.Error("duplicate artifact name should fail")
	}

	// Invalid: empty path
	d = &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Artifacts: []Artifact{{Name: "data", Path: ""}},
	}}}
	if err := Validate(d); err == nil {
		t.Error("empty artifact path should fail")
	}
}

func TestValidate_Freshness(t *testing.T) {
	// Valid freshness entries
	d := &DAG{Name: "t", Steps: []Step{{
		ID:      "a",
		Command: "echo",
		Freshness: []FreshnessCheck{
			{Path: "data/raw.csv", MaxAge: "6h"},
			{Path: "data/api.json", MaxAge: "30m", OnStale: "warn"},
			{Path: "data/other.csv", MaxAge: "1h", OnStale: "fail"},
		},
	}}}
	if err := Validate(d); err != nil {
		t.Errorf("valid freshness should pass: %v", err)
	}

	// Invalid: empty path
	d = &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Freshness: []FreshnessCheck{{Path: "", MaxAge: "6h"}},
	}}}
	if err := Validate(d); err == nil {
		t.Error("empty freshness path should fail")
	}

	// Invalid: empty max_age
	d = &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Freshness: []FreshnessCheck{{Path: "data/raw.csv", MaxAge: ""}},
	}}}
	if err := Validate(d); err == nil {
		t.Error("empty freshness max_age should fail")
	}

	// Invalid: bad max_age
	d = &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Freshness: []FreshnessCheck{{Path: "data/raw.csv", MaxAge: "not-a-duration"}},
	}}}
	if err := Validate(d); err == nil {
		t.Error("invalid freshness max_age should fail")
	}

	// Invalid: bad on_stale
	d = &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Freshness: []FreshnessCheck{{Path: "data/raw.csv", MaxAge: "6h", OnStale: "ignore"}},
	}}}
	if err := Validate(d); err == nil {
		t.Error("invalid freshness on_stale should fail")
	}
}

func TestValidate_ArtifactAbsolutePath(t *testing.T) {
	d := &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Artifacts: []Artifact{{Name: "data", Path: "/tmp/data.csv"}},
	}}}
	err := Validate(d)
	if err == nil {
		t.Fatal("absolute artifact path should fail validation")
	}
	if !strings.Contains(err.Error(), "must be relative") {
		t.Errorf("error should mention 'must be relative', got: %s", err.Error())
	}
}

func TestValidate_CacheIncompatibleWithApprove(t *testing.T) {
	d := &DAG{Name: "t", Steps: []Step{{
		ID:      "a",
		Approve: &ApproveStep{Message: "ok?"},
		Cache:   true,
	}}}
	err := Validate(d)
	if err == nil {
		t.Fatal("cache + approve should fail validation")
	}
	if !strings.Contains(err.Error(), "incompatible with approve") {
		t.Errorf("error should mention incompatibility, got: %s", err.Error())
	}
}

func TestValidate_CacheIncompatibleWithCall(t *testing.T) {
	d := &DAG{Name: "t", Steps: []Step{{
		ID:    "a",
		Call:  &CallStep{DAG: "other"},
		Cache: true,
	}}}
	err := Validate(d)
	if err == nil {
		t.Fatal("cache + call should fail validation")
	}
	if !strings.Contains(err.Error(), "incompatible with call") {
		t.Errorf("error should mention incompatibility, got: %s", err.Error())
	}
}

func TestValidate_CacheWithScriptIsValid(t *testing.T) {
	d := &DAG{Name: "t", Steps: []Step{{
		ID:     "a",
		Script: "run.R",
		Cache:  true,
	}}}
	if err := Validate(d); err != nil {
		t.Errorf("cache + script should be valid: %v", err)
	}
}

func TestValidate_MaxParallel(t *testing.T) {
	t.Run("zero is valid (unbounded)", func(t *testing.T) {
		d := &DAG{Name: "t", MaxParallel: 0, Steps: []Step{{ID: "a", Command: "echo"}}}
		if err := Validate(d); err != nil {
			t.Errorf("max_parallel: 0 should be valid: %v", err)
		}
	})
	t.Run("positive is valid", func(t *testing.T) {
		d := &DAG{Name: "t", MaxParallel: 4, Steps: []Step{{ID: "a", Command: "echo"}}}
		if err := Validate(d); err != nil {
			t.Errorf("max_parallel: 4 should be valid: %v", err)
		}
	})
	t.Run("negative is rejected", func(t *testing.T) {
		d := &DAG{Name: "t", MaxParallel: -1, Steps: []Step{{ID: "a", Command: "echo"}}}
		err := Validate(d)
		if err == nil {
			t.Fatal("negative max_parallel should fail validation")
		}
		if !strings.Contains(err.Error(), "max_parallel") {
			t.Errorf("error should mention max_parallel, got: %s", err.Error())
		}
	})
}
