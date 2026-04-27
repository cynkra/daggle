package state

import (
	"strings"
	"testing"
)

func TestValidateDAGName(t *testing.T) {
	ok := []string{"my-dag", "my_dag", "dag.name", "abc"}
	for _, n := range ok {
		if err := ValidateDAGName(n); err != nil {
			t.Errorf("ValidateDAGName(%q) = %v, want ok", n, err)
		}
	}

	bad := []struct {
		name string
		want string
	}{
		{"", "empty"},
		{".", "invalid"},
		{"..", "invalid"},
		{"../escape", "path separators"},
		{"foo/bar", "path separators"},
		{"foo\\bar", "path separators"},
		{"/abs/path", "path separators"},
	}
	for _, tt := range bad {
		err := ValidateDAGName(tt.name)
		if err == nil {
			t.Errorf("ValidateDAGName(%q) = nil, want error", tt.name)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("ValidateDAGName(%q) = %v, want contains %q", tt.name, err, tt.want)
		}
	}
}

func TestCreateRun_RejectsTraversal(t *testing.T) {
	t.Setenv("DAGGLE_DATA_DIR", t.TempDir())
	if _, err := CreateRun("../etc"); err == nil {
		t.Error("CreateRun should reject traversal in dag name")
	}
	if _, err := CreateRun(""); err == nil {
		t.Error("CreateRun should reject empty dag name")
	}
}

func TestListRuns_RejectsTraversal(t *testing.T) {
	t.Setenv("DAGGLE_DATA_DIR", t.TempDir())
	if _, err := ListRuns("../foo"); err == nil {
		t.Error("ListRuns should reject traversal in dag name")
	}
}
