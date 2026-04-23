package dag

import "fmt"

// stepTypeEntry describes one step-type variant: its YAML key (for error
// messages), a predicate that returns true when a Step has that type set,
// and a factory that returns a minimal Step whose only set type-field
// corresponds to this entry (used by tests).
type stepTypeEntry struct {
	name string
	set  func(s Step) bool
	zero func() Step
}

// stepTypeRegistry is the single source of truth for step-type names. Order
// defines StepType's tie-break (first match wins) and the order in which error
// messages list the types. Adding a step type means adding one entry here, one
// field on Step, and one case in executor.New.
var stepTypeRegistry = []stepTypeEntry{
	{"script", func(s Step) bool { return s.Script != "" }, func() Step { return Step{Script: "foo.R"} }},
	{"r_expr", func(s Step) bool { return s.RExpr != "" }, func() Step { return Step{RExpr: "1+1"} }},
	{"command", func(s Step) bool { return s.Command != "" }, func() Step { return Step{Command: "echo"} }},
	{"quarto", func(s Step) bool { return s.Quarto != "" }, func() Step { return Step{Quarto: "report.qmd"} }},
	{"test", func(s Step) bool { return s.Test != "" }, func() Step { return Step{Test: "."} }},
	{"check", func(s Step) bool { return s.Check != "" }, func() Step { return Step{Check: "."} }},
	{"document", func(s Step) bool { return s.Document != "" }, func() Step { return Step{Document: "."} }},
	{"lint", func(s Step) bool { return s.Lint != "" }, func() Step { return Step{Lint: "."} }},
	{"style", func(s Step) bool { return s.Style != "" }, func() Step { return Step{Style: "."} }},
	{"rmd", func(s Step) bool { return s.Rmd != "" }, func() Step { return Step{Rmd: "report.Rmd"} }},
	{"renv_restore", func(s Step) bool { return s.RenvRestore != "" }, func() Step { return Step{RenvRestore: "."} }},
	{"coverage", func(s Step) bool { return s.Coverage != "" }, func() Step { return Step{Coverage: "."} }},
	{"validate", func(s Step) bool { return s.Validate != "" }, func() Step { return Step{Validate: "check.R"} }},
	{"approve", func(s Step) bool { return s.Approve != nil }, func() Step { return Step{Approve: &ApproveStep{Message: "review"}} }},
	{"call", func(s Step) bool { return s.Call != nil }, func() Step { return Step{Call: &CallStep{DAG: "other"}} }},
	{"pin", func(s Step) bool { return s.Pin != nil }, func() Step { return Step{Pin: &PinDeploy{Board: "local", Name: "n", Object: "o"}} }},
	{"vetiver", func(s Step) bool { return s.Vetiver != nil }, func() Step { return Step{Vetiver: &VetiverDeploy{Action: "pin", Name: "m"}} }},
	{"shinytest", func(s Step) bool { return s.Shinytest != "" }, func() Step { return Step{Shinytest: "."} }},
	{"pkgdown", func(s Step) bool { return s.Pkgdown != "" }, func() Step { return Step{Pkgdown: "."} }},
	{"install", func(s Step) bool { return s.Install != "" }, func() Step { return Step{Install: "."} }},
	{"targets", func(s Step) bool { return s.Targets != "" }, func() Step { return Step{Targets: "."} }},
	{"benchmark", func(s Step) bool { return s.Benchmark != "" }, func() Step { return Step{Benchmark: "bench/"} }},
	{"revdepcheck", func(s Step) bool { return s.Revdepcheck != "" }, func() Step { return Step{Revdepcheck: "."} }},
	{"connect", func(s Step) bool { return s.Connect != nil }, func() Step { return Step{Connect: &ConnectDeploy{Type: "shiny", Path: "app/"}} }},
	{"database", func(s Step) bool { return s.Database != nil }, func() Step { return Step{Database: &DatabaseStep{Driver: "sqlite", Query: "SELECT 1", Output: "out.csv"}} }},
	{"email", func(s Step) bool { return s.Email != nil }, func() Step { return Step{Email: &EmailStep{Channel: "smtp", Subject: "s", Body: "b"}} }},
	{"docker", func(s Step) bool { return s.Docker != nil }, func() Step { return Step{Docker: &DockerStep{Image: "alpine", Command: "echo"}} }},
}

// AllStepTypes returns every valid step-type name in registry order.
func AllStepTypes() []string {
	names := make([]string, len(stepTypeRegistry))
	for i, e := range stepTypeRegistry {
		names[i] = e.name
	}
	return names
}

// CountStepTypes reports how many of the mutually-exclusive step-type fields
// are set on s. Used by validation to enforce "exactly one type per step".
func CountStepTypes(s Step) int {
	n := 0
	for _, e := range stepTypeRegistry {
		if e.set(s) {
			n++
		}
	}
	return n
}

// MinimalStepOfType returns a Step whose only set type-field corresponds to
// name. Panics if name is not a registered step type. Intended for tests that
// want to iterate every step type without hard-coding a parallel table.
func MinimalStepOfType(name string) Step {
	for _, e := range stepTypeRegistry {
		if e.name == name {
			return e.zero()
		}
	}
	panic(fmt.Sprintf("dag.MinimalStepOfType: unknown step type %q", name))
}
