package envutil

import (
	"slices"
	"strings"
	"testing"
)

func TestWithUTF8Locale(t *testing.T) {
	tests := []struct {
		name       string
		in         []string
		wantLang   string
		wantLCAll  string
		wantOthers []string
	}{
		{
			name:      "empty env adds both",
			in:        nil,
			wantLang:  "C.UTF-8",
			wantLCAll: "C.UTF-8",
		},
		{
			name:      "LANG=C is replaced",
			in:        []string{"LANG=C"},
			wantLang:  "C.UTF-8",
			wantLCAll: "C.UTF-8",
		},
		{
			name:      "LANG=POSIX is replaced",
			in:        []string{"LANG=POSIX"},
			wantLang:  "C.UTF-8",
			wantLCAll: "C.UTF-8",
		},
		{
			name:      "empty LANG= is replaced",
			in:        []string{"LANG="},
			wantLang:  "C.UTF-8",
			wantLCAll: "C.UTF-8",
		},
		{
			name:      "LANG en_US.UTF-8 is preserved, LC_ALL added",
			in:        []string{"LANG=en_US.UTF-8"},
			wantLang:  "en_US.UTF-8",
			wantLCAll: "C.UTF-8",
		},
		{
			name:      "LC_ALL en_US.UTF-8 is preserved, LANG added",
			in:        []string{"LC_ALL=en_US.UTF-8"},
			wantLang:  "C.UTF-8",
			wantLCAll: "en_US.UTF-8",
		},
		{
			name:      "both UTF-8 are preserved",
			in:        []string{"LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8"},
			wantLang:  "en_US.UTF-8",
			wantLCAll: "en_US.UTF-8",
		},
		{
			name:      "LANG UTF-8 preserved, LC_ALL=C is replaced",
			in:        []string{"LANG=en_US.UTF-8", "LC_ALL=C"},
			wantLang:  "en_US.UTF-8",
			wantLCAll: "C.UTF-8",
		},
		{
			name:       "non-locale entries pass through",
			in:         []string{"LANG=de_DE.UTF-8", "OTHER=x", "PATH=/usr/bin"},
			wantLang:   "de_DE.UTF-8",
			wantLCAll:  "C.UTF-8",
			wantOthers: []string{"OTHER=x", "PATH=/usr/bin"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WithUTF8Locale(tt.in)
			if v := lookup(got, "LANG"); v != tt.wantLang {
				t.Errorf("LANG = %q, want %q", v, tt.wantLang)
			}
			if v := lookup(got, "LC_ALL"); v != tt.wantLCAll {
				t.Errorf("LC_ALL = %q, want %q", v, tt.wantLCAll)
			}
			for _, want := range tt.wantOthers {
				if !slices.Contains(got, want) {
					t.Errorf("expected %q to be preserved in output, got %v", want, got)
				}
			}
		})
	}
}

func TestWithUTF8Locale_DoesNotMutateInput(t *testing.T) {
	in := []string{"LANG=C", "OTHER=x"}
	want := slices.Clone(in)
	_ = WithUTF8Locale(in)
	if !slices.Equal(in, want) {
		t.Errorf("input was mutated: %v, want %v", in, want)
	}
}

func TestWithUTF8Locale_DuplicateEntries(t *testing.T) {
	// Real os.Environ() should not have duplicates, but the helper should
	// behave deterministically if it ever does.
	got := WithUTF8Locale([]string{"LANG=C", "LANG=en_US.UTF-8"})
	// Both LANG entries get processed; first becomes C.UTF-8 (replaced),
	// second stays. Last-wins semantics in env mean LANG=en_US.UTF-8 wins.
	if v := lookup(got, "LANG"); v != "en_US.UTF-8" {
		t.Errorf("with duplicates, LANG (last wins) = %q, want %q", v, "en_US.UTF-8")
	}
}

// lookup returns the value of the last KEY=... entry in env, or "" if none.
// Mirrors POSIX last-wins semantics so tests reflect what the child sees.
func lookup(env []string, key string) string {
	prefix := key + "="
	val := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			val = kv[len(prefix):]
		}
	}
	return val
}
