package dag

import "testing"

func TestTopoSort_Linear(t *testing.T) {
	steps := []Step{
		{ID: "a"},
		{ID: "b", Depends: []string{"a"}},
		{ID: "c", Depends: []string{"b"}},
	}
	tiers, err := TopoSort(steps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tiers) != 3 {
		t.Fatalf("tiers = %d, want 3", len(tiers))
	}
	if tiers[0][0].ID != "a" || tiers[1][0].ID != "b" || tiers[2][0].ID != "c" {
		t.Errorf("unexpected tier order: %v", tiers)
	}
}

func TestTopoSort_Parallel(t *testing.T) {
	steps := []Step{
		{ID: "a"},
		{ID: "b"},
		{ID: "c", Depends: []string{"a", "b"}},
	}
	tiers, err := TopoSort(steps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tiers) != 2 {
		t.Fatalf("tiers = %d, want 2", len(tiers))
	}
	if len(tiers[0]) != 2 {
		t.Errorf("tier 0 size = %d, want 2", len(tiers[0]))
	}
}

func TestTopoSort_Cycle(t *testing.T) {
	steps := []Step{
		{ID: "a", Depends: []string{"c"}},
		{ID: "b", Depends: []string{"a"}},
		{ID: "c", Depends: []string{"b"}},
	}
	_, err := TopoSort(steps)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
}
