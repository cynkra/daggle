package dag

import "fmt"

// TopoSort performs a topological sort using Kahn's algorithm.
// It returns tiers: each tier is a group of steps that can run in parallel.
// Returns an error if the graph contains a cycle.
func TopoSort(steps []Step) ([][]Step, error) {
	stepMap := make(map[string]Step, len(steps))
	inDegree := make(map[string]int, len(steps))
	dependents := make(map[string][]string) // step -> steps that depend on it

	for _, s := range steps {
		stepMap[s.ID] = s
		inDegree[s.ID] = len(s.Depends)
		for _, dep := range s.Depends {
			dependents[dep] = append(dependents[dep], s.ID)
		}
	}

	// Seed with steps that have no dependencies
	var queue []string
	for _, s := range steps {
		if inDegree[s.ID] == 0 {
			queue = append(queue, s.ID)
		}
	}

	var tiers [][]Step
	processed := 0

	for len(queue) > 0 {
		// All items in the current queue form one tier
		tier := make([]Step, 0, len(queue))
		for _, id := range queue {
			tier = append(tier, stepMap[id])
		}
		tiers = append(tiers, tier)

		var nextQueue []string
		for _, id := range queue {
			processed++
			for _, depID := range dependents[id] {
				inDegree[depID]--
				if inDegree[depID] == 0 {
					nextQueue = append(nextQueue, depID)
				}
			}
		}
		queue = nextQueue
	}

	if processed != len(steps) {
		return nil, fmt.Errorf("cycle detected in DAG: %d steps could not be scheduled", len(steps)-processed)
	}

	return tiers, nil
}
