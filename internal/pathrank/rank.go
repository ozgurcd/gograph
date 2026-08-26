// Package pathrank defines the shared ordering used by repository and
// workspace relationship-path queries.
package pathrank

import (
	"container/heap"
	"strconv"
	"strings"
)

// Certainty is the worst resolution status encountered along a path.
// Lower values rank ahead of higher values.
type Certainty uint8

const (
	Exact Certainty = iota
	Ambiguous
	Possible
)

// Rank is compared lexicographically in field order. Tie is a canonical,
// transport-independent edge sequence used only to make equivalent paths
// deterministic.
type Rank struct {
	Certainty                Certainty
	Length                   int
	IncludesTests            bool
	IncludesHeuristics       bool
	CrossRepositoryEdgeCount int
	TraversalLength          int
	Tie                      string
}

// Extend returns the rank after traversing one relationship edge.
func (r Rank) Extend(certainty Certainty, visible, test, heuristic, crossRepository bool, tie string) Rank {
	r.TraversalLength++
	if certainty > r.Certainty {
		r.Certainty = certainty
	}
	if visible {
		r.Length++
	}
	r.IncludesTests = r.IncludesTests || test
	r.IncludesHeuristics = r.IncludesHeuristics || heuristic
	if crossRepository {
		r.CrossRepositoryEdgeCount++
	}
	if tie != "" {
		if r.Tie != "" {
			r.Tie += "\x1e"
		}
		r.Tie += tie
	}
	return r
}

// Less reports whether r ranks ahead of other.
func (r Rank) Less(other Rank) bool {
	if r.Certainty != other.Certainty {
		return r.Certainty < other.Certainty
	}
	if r.Length != other.Length {
		return r.Length < other.Length
	}
	if r.IncludesTests != other.IncludesTests {
		return !r.IncludesTests
	}
	if r.IncludesHeuristics != other.IncludesHeuristics {
		return !r.IncludesHeuristics
	}
	if r.CrossRepositoryEdgeCount != other.CrossRepositoryEdgeCount {
		return r.CrossRepositoryEdgeCount < other.CrossRepositoryEdgeCount
	}
	if r.TraversalLength != other.TraversalLength {
		return r.TraversalLength < other.TraversalLength
	}
	return r.Tie < other.Tie
}

// ClassKey identifies the categorical state that may be changed by later
// traversal. Keeping separate best paths for each class preserves optimality
// when a later possible, test, or heuristic edge makes two prefixes equivalent.
func (r Rank) ClassKey() string {
	return strconv.Itoa(int(r.Certainty)) + ":" + strconv.FormatBool(r.IncludesTests) + ":" + strconv.FormatBool(r.IncludesHeuristics)
}

// IsTestPath applies the same source classification to both path engines.
func IsTestPath(path string) bool {
	if strings.HasSuffix(path, "_test.go") {
		return true
	}
	lower := strings.ToLower(path)
	if strings.Contains(lower, "mock") || strings.Contains(lower, "fake") {
		return true
	}
	for _, part := range strings.Split(path, "/") {
		if part == "testdata" || part == "test" || part == "tests" {
			return true
		}
	}
	return false
}

type item[T any] struct {
	value T
	rank  Rank
}

type items[T any] []item[T]

func (q items[T]) Len() int           { return len(q) }
func (q items[T]) Less(i, j int) bool { return q[i].rank.Less(q[j].rank) }
func (q items[T]) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *items[T]) Push(value any)    { *q = append(*q, value.(item[T])) }
func (q *items[T]) Pop() any {
	old := *q
	last := old[len(old)-1]
	*q = old[:len(old)-1]
	return last
}

// Queue is a minimum priority queue ordered by Rank.
type Queue[T any] struct {
	items items[T]
}

// Push adds a ranked value.
func (q *Queue[T]) Push(value T, rank Rank) {
	heap.Push(&q.items, item[T]{value: value, rank: rank})
}

// Pop removes the highest-ranked value.
func (q *Queue[T]) Pop() (T, Rank, bool) {
	if len(q.items) == 0 {
		var zero T
		return zero, Rank{}, false
	}
	next := heap.Pop(&q.items).(item[T])
	return next.value, next.rank, true
}
