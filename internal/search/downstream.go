package search

// downstream retains the existing CallerName/CalleeRaw equality semantics.
// Its ordered adjacency is immutable; visited sets belong to each request.
func (snapshot *Snapshot) downstream(names []string) map[string]bool {
	visited := make(map[string]bool)
	if len(names) == 0 {
		return visited
	}
	snapshot.downstreamOnce.Do(func() {
		snapshot.downstreamCalls = make(map[string][]string)
		for _, call := range snapshot.g.Calls {
			snapshot.downstreamCalls[call.CallerName] = append(snapshot.downstreamCalls[call.CallerName], call.CalleeRaw)
		}
	})
	queue := append([]string(nil), names...)
	for _, name := range names {
		visited[name] = true
	}
	for index := 0; index < len(queue); index++ {
		for _, callee := range snapshot.downstreamCalls[queue[index]] {
			if !visited[callee] {
				visited[callee] = true
				queue = append(queue, callee)
			}
		}
	}
	return visited
}
