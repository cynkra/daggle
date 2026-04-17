package api

import (
	"testing"

	"github.com/cynkra/daggle/dag"
)

func TestMatchesDAGFilters(t *testing.T) {
	d := &dag.DAG{
		Owner: "alice",
		Team:  "data",
		Tags:  []string{"etl", "daily"},
	}

	cases := []struct {
		name                  string
		tag, team, owner      string
		want                  bool
	}{
		{"no filters", "", "", "", true},
		{"matching tag", "etl", "", "", true},
		{"non-matching tag", "weekly", "", "", false},
		{"matching team", "", "data", "", true},
		{"non-matching team", "", "ml", "", false},
		{"matching owner", "", "", "alice", true},
		{"non-matching owner", "", "", "bob", false},
		{"all match", "daily", "data", "alice", true},
		{"one filter fails", "etl", "ml", "alice", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchesDAGFilters(d, tc.tag, tc.team, tc.owner)
			if got != tc.want {
				t.Errorf("matchesDAGFilters(tag=%q, team=%q, owner=%q) = %v, want %v",
					tc.tag, tc.team, tc.owner, got, tc.want)
			}
		})
	}
}
