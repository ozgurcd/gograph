package search

import (
	"sort"
	"strings"
	"sync"

	"github.com/ozgurcd/gograph/internal/graph"
)

// Snapshot borrows an immutable graph and owns its lazily computed indexes.
// Callers must create a new Snapshot when graph content changes. There is no
// global graph-pointer cache and no memo table that grows with query strings.
// Returned pages own their slices; they cannot mutate cached classification.
type Snapshot struct {
	callsOnce          sync.Once
	callSymbols        map[string]graph.SymbolNode
	callOutgoing       map[string][]graph.CallEdge
	g                  *graph.Graph
	fingerprintOnce    sync.Once
	fingerprint        string
	fingerprintErr     error
	routesOnce         sync.Once
	routes             []Result
	sqlOnce            sync.Once
	sql                []SQLResult
	impactOnce         sync.Once
	impact             impactIndex
	attributionOnce    sync.Once
	attribution        attributionGraphData
	reverseAttribution map[string][]attributionLink
	reachabilityOnce   sync.Once
	reachability       map[string]symbolTestReach
}

func NewSnapshot(g *graph.Graph) *Snapshot { return &Snapshot{g: g} }

func (s *Snapshot) Fingerprint() (string, error) {
	s.fingerprintOnce.Do(func() { s.fingerprint, s.fingerprintErr = ResultSnapshotFingerprint(s.g) })
	return s.fingerprint, s.fingerprintErr
}

func (s *Snapshot) binding(schema string, selection any) (string, error) {
	fingerprint, err := s.Fingerprint()
	if err != nil {
		return "", err
	}
	return selectionBinding(fingerprint, schema, selection)
}

func (s *Snapshot) routeRows() []Result {
	s.routesOnce.Do(func() { s.routes = Routes(s.g) })
	return s.routes
}

func (s *Snapshot) sqlRows() []SQLResult {
	s.sqlOnce.Do(func() {
		if s.g == nil {
			return
		}
		s.sql = make([]SQLResult, 0, len(s.g.SQLs))
		for _, edge := range s.g.SQLs {
			s.sql = append(s.sql, classifySQLResult(edge))
		}
		sort.SliceStable(s.sql, func(i, j int) bool {
			left, right := s.sql[i], s.sql[j]
			leftQuery, rightQuery := strings.ToLower(left.Query), strings.ToLower(right.Query)
			if leftQuery != rightQuery {
				return leftQuery < rightQuery
			}
			if left.File != right.File {
				return left.File < right.File
			}
			if left.Line != right.Line {
				return left.Line < right.Line
			}
			return left.Function < right.Function
		})
	})
	return s.sql
}

func (s *Snapshot) attributionIndex() (attributionGraphData, map[string][]attributionLink) {
	s.attributionOnce.Do(func() {
		s.attribution = buildAttributionGraph(s.g)
		s.reverseAttribution = make(map[string][]attributionLink)
		for _, link := range s.attribution.links {
			s.reverseAttribution[link.to] = append(s.reverseAttribution[link.to], link)
		}
		for _, links := range s.reverseAttribution {
			sort.Slice(links, func(i, j int) bool {
				if links[i].from != links[j].from {
					return links[i].from < links[j].from
				}
				return links[i].resolution < links[j].resolution
			})
		}
	})
	return s.attribution, s.reverseAttribution
}

func (s *Snapshot) testReachability() map[string]symbolTestReach {
	s.reachabilityOnce.Do(func() { s.reachability = s.computeTestReachability() })
	return s.reachability
}

func (s *Snapshot) callIndex() (map[string]graph.SymbolNode, map[string][]graph.CallEdge) {
	s.callsOnce.Do(func() {
		s.callSymbols = make(map[string]graph.SymbolNode, len(s.g.Symbols))
		for _, symbol := range s.g.Symbols {
			s.callSymbols[symbol.ID] = symbol
		}
		s.callOutgoing = make(map[string][]graph.CallEdge)
		for _, call := range s.g.Calls {
			s.callOutgoing[call.CallerSymbolID] = append(s.callOutgoing[call.CallerSymbolID], call)
		}
	})
	return s.callSymbols, s.callOutgoing
}
