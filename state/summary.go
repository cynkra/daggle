package state

import "time"

// StepState holds the accumulated state for a single step built from events.
type StepState struct {
	StepID      string
	Status      string
	Duration    time.Duration
	Attempts    int
	Error       string
	ErrorDetail string
	Message     string
	Cached      bool
	CacheKey    string
	PeakRSSKB   int64
	UserCPUSec  float64
	SysCPUSec   float64
}

// BuildStepSummaries walks events and accumulates per-step state, preserving
// first-seen order. Steps with no StepID are skipped.
func BuildStepSummaries(events []Event) []StepState {
	steps := make(map[string]*StepState)
	var order []string

	for _, e := range events {
		if e.StepID == "" {
			continue
		}
		ss, ok := steps[e.StepID]
		if !ok {
			ss = &StepState{StepID: e.StepID}
			steps[e.StepID] = ss
			order = append(order, e.StepID)
		}
		switch e.Type {
		case EventStepStarted:
			ss.Status = "running"
			ss.Attempts = e.Attempt
		case EventStepCompleted:
			ss.Status = "completed"
			if d, err := time.ParseDuration(e.Duration); err == nil {
				ss.Duration = d
			}
			ss.Attempts = e.Attempt
			ss.PeakRSSKB = e.PeakRSSKB
			ss.UserCPUSec = e.UserCPUSec
			ss.SysCPUSec = e.SysCPUSec
		case EventStepFailed:
			ss.Status = "failed"
			if d, err := time.ParseDuration(e.Duration); err == nil {
				ss.Duration = d
			}
			ss.Attempts = e.Attempt
			ss.Error = e.Error
			ss.ErrorDetail = e.ErrorDetail
		case EventStepRetrying:
			ss.Status = "retrying"
		case EventStepWaitApproval:
			ss.Status = "waiting"
			ss.Message = e.Message
		case EventStepApproved:
			ss.Status = "approved"
		case EventStepRejected:
			ss.Status = "rejected"
		case "step_skipped":
			ss.Status = "skipped"
		case EventStepCached:
			ss.Status = "cached"
			ss.Cached = true
			if e.CacheInfo != nil {
				ss.CacheKey = e.CacheKey
			}
		}
	}

	result := make([]StepState, 0, len(order))
	for _, id := range order {
		result = append(result, *steps[id])
	}
	return result
}
