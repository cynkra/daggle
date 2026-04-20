package dag

import "testing"

func TestFilters_Match(t *testing.T) {
	d := &DAG{
		Owner: "alice",
		Team:  "data",
		Tags:  []string{"etl", "daily"},
	}

	cases := []struct {
		name             string
		tag, team, owner string
		want             bool
	}{
		{"no filters", "", "", "", true},
		{"matching tag", "etl", "", "", true},
		{"non-matching tag", "weekly", "", "", false},
		{"matching team", "", "data", "", true},
		{"non-matching team", "", "ml", "", false},
		{"matching owner", "", "", "alice", true},
		{"non-matching owner", "", "", "bob", false},
		{"all three match", "etl", "data", "alice", true},
		{"one mismatch fails", "etl", "data", "bob", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := Filters{Tag: tc.tag, Team: tc.team, Owner: tc.owner}
			if got := f.Match(d); got != tc.want {
				t.Errorf("Match = %v, want %v", got, tc.want)
			}
		})
	}
}
