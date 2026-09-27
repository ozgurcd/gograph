package search

import (
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// Prepared forms preserve MatchSymbol's exact transformations, including its
// substring and Unicode behavior. This resolver is local to one immutable
// impact-index build; it neither changes public enumeration nor retains graphs.
type preparedSymbol struct {
	id, name, pkg, receiverName, fullReceiverName, receiver string
	hasReceiver                                             bool
}

type symbolQuery struct {
	lower, replaced string
	parts           []string
	dotted          bool
}

func prepareSymbolQuery(query string) symbolQuery {
	q := symbolQuery{lower: strings.ToLower(query), dotted: strings.Contains(query, ".")}
	if q.dotted {
		q.replaced = strings.ReplaceAll(q.lower, ".", "::")
		norm := strings.ReplaceAll(q.lower, "(", "")
		norm = strings.ReplaceAll(norm, ")", "")
		norm = strings.ReplaceAll(norm, "*", "")
		q.parts = strings.Split(norm, ".")
	}
	return q
}

func prepareSymbol(s graph.SymbolNode) preparedSymbol {
	p := preparedSymbol{id: strings.ToLower(s.ID), name: strings.ToLower(s.Name), pkg: strings.ToLower(s.PackageName), hasReceiver: s.Receiver != ""}
	if p.hasReceiver {
		p.receiverName = strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(s.Receiver, "*"), "(") + "." + s.Name)
		p.fullReceiverName = strings.ToLower("(" + s.Receiver + ")." + s.Name)
		p.receiver = strings.ReplaceAll(strings.ToLower(s.Receiver), "*", "")
	}
	return p
}

func (s preparedSymbol) matches(q symbolQuery) bool {
	if s.id == q.lower || s.name == q.lower {
		return true
	}
	if s.hasReceiver && (s.receiverName == q.lower || s.fullReceiverName == q.lower) {
		return true
	}
	if !q.dotted {
		return false
	}
	if strings.Contains(s.id, q.replaced) {
		return true
	}
	if len(q.parts) == 2 {
		return (s.pkg == q.parts[0] || (s.hasReceiver && s.receiver == q.parts[0])) && s.name == q.parts[1]
	}
	return len(q.parts) == 3 && s.hasReceiver && s.pkg == q.parts[0] && s.receiver == q.parts[1] && s.name == q.parts[2]
}

type symbolResolver struct {
	symbols  []graph.SymbolNode
	prepared []preparedSymbol
}

func newSymbolResolver(symbols []graph.SymbolNode) symbolResolver {
	r := symbolResolver{symbols: symbols, prepared: make([]preparedSymbol, len(symbols))}
	for i, symbol := range symbols {
		r.prepared[i] = prepareSymbol(symbol)
	}
	return r
}

func (r symbolResolver) unique(query string) (string, bool) {
	q := prepareSymbolQuery(query)
	index := -1
	for i, symbol := range r.prepared {
		if symbol.matches(q) {
			if index >= 0 {
				return "", false
			}
			index = i
		}
	}
	if index < 0 {
		return "", false
	}
	return r.symbols[index].ID, true
}
