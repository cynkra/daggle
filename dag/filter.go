package dag

import "slices"

// Filters selects DAGs by tag, team, and owner. An empty field means
// "no constraint on this dimension". Tag matches when any entry in
// d.Tags equals Tag.
type Filters struct {
	Tag   string
	Team  string
	Owner string
}

// Match reports whether d passes every non-empty filter.
func (f Filters) Match(d *DAG) bool {
	if f.Owner != "" && d.Owner != f.Owner {
		return false
	}
	if f.Team != "" && d.Team != f.Team {
		return false
	}
	if f.Tag != "" && !slices.Contains(d.Tags, f.Tag) {
		return false
	}
	return true
}
