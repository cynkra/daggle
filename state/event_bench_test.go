package state

import "testing"

// BenchmarkEventWriter measures the cost of writing many events through a
// single EventWriter. The file handle is opened on the first Write and
// reused for the rest, so we're measuring the steady-state write cost.
func BenchmarkEventWriter(b *testing.B) {
	runDir := b.TempDir()
	w := NewEventWriter(runDir)
	defer func() { _ = w.Close() }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := w.Write(Event{Type: EventStepCompleted, StepID: "step"}); err != nil {
			b.Fatal(err)
		}
	}
}
