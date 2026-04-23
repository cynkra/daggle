package scheduler

import "context"

// dagCompletionEvent is emitted when a DAG run finishes.
type dagCompletionEvent struct {
	DAGName string
	Status  string // "completed" or "failed"
}

// onDAGListener tracks a DAG that should be triggered when another DAG completes.
type onDAGListener struct {
	dagPath string // path to the downstream DAG file
	status  string // "completed", "failed", or "any"
}

// dispatchCompletions listens for DAG completion events and triggers on_dag listeners.
func (s *Scheduler) dispatchCompletions(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-s.completions:
			s.mu.Lock()
			listeners := s.onDAGListeners[event.DAGName]
			s.mu.Unlock()

			for _, listener := range listeners {
				if listener.status == "any" || listener.status == event.Status || (listener.status == "" && event.Status == "completed") {
					s.logger.Info("on_dag trigger fired", "upstream", event.DAGName, "status", event.Status, "downstream", listener.dagPath)
					s.triggerRun(listener.dagPath, "on_dag")
				}
			}
		}
	}
}
