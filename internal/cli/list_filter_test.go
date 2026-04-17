package cli

import (
	"testing"

	"github.com/cynkra/daggle/dag"
)

func TestMatchesFilters(t *testing.T) {
	d := &dag.DAG{
		Owner: "alice",
		Team:  "data",
		Tags:  []string{"etl", "daily"},
	}

	cases := []struct {
		name              string
		tag, team, owner  string
		want              bool
	}{
		{"no filters", "", "", "", true},
		{"matching tag", "etl", "", "", true},
		{"non-matching tag", "weekly", "", "", false},
		{"matching team", "", "data", "", true},
		{"non-matching team", "", "ml", "", false},
		{"matching owner", "", "", "alice", true},
		{"non-matching owner", "", "", "bob", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			listTagFilter = tc.tag
			listTeamFilter = tc.team
			listOwnerFilter = tc.owner
			t.Cleanup(func() {
				listTagFilter = ""
				listTeamFilter = ""
				listOwnerFilter = ""
			})
			got := matchesFilters(d)
			if got != tc.want {
				t.Errorf("matchesFilters = %v, want %v", got, tc.want)
			}
		})
	}
}
