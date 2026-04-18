package cli

import "testing"

func TestDashIfEmpty(t *testing.T) {
	cases := map[string]string{
		"":      "-",
		"alice": "alice",
		"  ":    "  ", // whitespace is not considered empty
	}
	for in, want := range cases {
		if got := dashIfEmpty(in); got != want {
			t.Errorf("dashIfEmpty(%q) = %q, want %q", in, got, want)
		}
	}
}
