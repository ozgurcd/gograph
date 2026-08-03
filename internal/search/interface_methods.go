package search

import (
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// resolveInterfaceMethodTargets resolves a structured Interface.Method query
// to the concrete method symbol IDs proven by Graph.Implements. Interface
// methods live in SymbolNode.InterfaceMethods rather than as standalone method
// symbols, so the regular FindSymbols path cannot resolve these queries.
//
// The boolean reports whether the query named an indexed interface method. It
// is deliberately distinct from len(targets) > 0: an interface method with no
// indexed implementation is still a valid, specifically-qualified query and
// must not fall back to fuzzy matching against unrelated same-named methods.
func resolveInterfaceMethodTargets(g *graph.Graph, query string) (map[string]bool, bool) {
	separator := strings.LastIndex(query, ".")
	if separator <= 0 || separator == len(query)-1 {
		return nil, false
	}

	interfaceQuery := query[:separator]
	methodQuery := query[separator+1:]
	targets := make(map[string]bool)
	matchedInterfaceMethod := false

	for _, iface := range g.Symbols {
		if iface.Kind != graph.KindInterface || !MatchSymbol(iface, interfaceQuery) {
			continue
		}

		methodName, methodSignature, ok := findInterfaceMethod(iface, methodQuery)
		if !ok {
			continue
		}
		matchedInterfaceMethod = true

		for _, implementation := range g.Implements {
			if !implementsInterface(implementation, iface) {
				continue
			}
			addImplementationMethodIDs(targets, implementation, methodName)
			for _, symbol := range g.Symbols {
				if isImplementedMethod(symbol, implementation, methodName, methodSignature) {
					targets[symbol.ID] = true
				}
			}
		}
	}

	return targets, matchedInterfaceMethod
}

// addImplementationMethodIDs covers promoted methods and synthetic SSA method
// wrappers that have a valid call-edge ID but no standalone SymbolNode. Adding
// both receiver forms is safe: only IDs that actually occur on CallEdges match
// a caller query.
func addImplementationMethodIDs(targets map[string]bool, implementation graph.ImplementsEdge, methodName string) {
	packagePath, concreteFromID, ok := strings.Cut(implementation.ConcreteID, "::")
	if !ok || packagePath == "" {
		return
	}
	concrete := implementation.Concrete
	if concrete == "" {
		concrete = concreteFromID
	}
	concrete = normalizeReceiverType(concrete)
	if concrete == "" {
		return
	}
	targets[packagePath+"::("+concrete+")."+methodName] = true
	targets[packagePath+"::(*"+concrete+")."+methodName] = true
}

func findInterfaceMethod(iface graph.SymbolNode, query string) (name, signature string, ok bool) {
	// Prefer Go's exact, case-sensitive identity. The fallback preserves the
	// CLI's case-insensitive discovery UX only when it is unambiguous; an
	// interface may legally contain both Delete and delete, so selecting either
	// one from map iteration would make caller results nondeterministic.
	if methodSignature, exists := iface.InterfaceMethods[query]; exists {
		return query, methodSignature, true
	}
	matchedName := ""
	matchedSignature := ""
	for methodName, methodSignature := range iface.InterfaceMethods {
		if strings.EqualFold(methodName, query) {
			if matchedName != "" {
				return "", "", false
			}
			matchedName = methodName
			matchedSignature = methodSignature
		}
	}
	return matchedName, matchedSignature, matchedName != ""
}

func implementsInterface(implementation graph.ImplementsEdge, iface graph.SymbolNode) bool {
	if implementation.InterfaceID != "" {
		return implementation.InterfaceID == iface.ID
	}
	return implementation.Interface == iface.Name
}

func isImplementedMethod(symbol graph.SymbolNode, implementation graph.ImplementsEdge, methodName, methodSignature string) bool {
	if symbol.Kind != graph.KindMethod || symbol.Name != methodName {
		return false
	}
	if methodSignature != "" && symbol.MethodSignature != "" && symbol.MethodSignature != methodSignature {
		return false
	}

	receiver := normalizeReceiverType(symbol.Receiver)
	if receiver == "" || receiver != implementation.Concrete {
		return false
	}
	if implementation.ConcreteID == "" {
		return true
	}

	packageID, _, ok := strings.Cut(symbol.ID, "::")
	if !ok {
		return false
	}
	return packageID+"::"+receiver == implementation.ConcreteID
}

func normalizeReceiverType(receiver string) string {
	receiver = strings.TrimSpace(receiver)
	receiver = strings.TrimPrefix(receiver, "(")
	receiver = strings.TrimSuffix(receiver, ")")
	receiver = strings.TrimSpace(receiver)
	receiver = strings.TrimPrefix(receiver, "*")
	return strings.TrimSpace(receiver)
}

// resolvedCalleeMatchesQuery lets concrete dot-notation address synthetic SSA
// method wrappers that have a stable CalleeSymbolID but no parser-emitted
// SymbolNode (for example, a method promoted from an embedded field).
func resolvedCalleeMatchesQuery(calleeID, query string) bool {
	if calleeID == "" {
		return false
	}
	symbol, ok := symbolFromID(calleeID)
	return ok && MatchSymbol(symbol, query)
}

func resolvedCallTargetIDs(g *graph.Graph, query string) (map[string]bool, bool) {
	targets, interfaceMethodQuery := resolveInterfaceMethodTargets(g, query)
	resolved := make(map[string]bool, len(targets)+1)
	for id := range targets {
		resolved[id] = true
	}
	if interfaceMethodQuery {
		return expandForwardingTargets(g, resolved), true
	}
	for _, symbol := range FindSymbols(g, query) {
		if symbol.ID != "" {
			resolved[symbol.ID] = true
		}
	}
	if isFullyQualifiedID(query) {
		resolved[query] = true
	}
	for _, call := range g.Calls {
		if resolvedCalleeMatchesQuery(call.CalleeSymbolID, query) {
			resolved[call.CalleeSymbolID] = true
		}
	}
	return expandForwardingTargets(g, resolved), false
}

// expandForwardingTargets walks synthetic wrapper edges backwards. A query for
// the declared embedded method must also match each concrete wrapper that can
// forward to it, while the wrapper edge itself stays hidden from call-site
// presentation.
func expandForwardingTargets(g *graph.Graph, seeds map[string]bool) map[string]bool {
	expanded := make(map[string]bool, len(seeds))
	for id := range seeds {
		expanded[id] = true
	}
	for changed := true; changed; {
		changed = false
		for _, call := range g.Calls {
			if !call.Synthetic || call.CallerSymbolID == "" || call.CalleeSymbolID == "" || !expanded[call.CalleeSymbolID] || expanded[call.CallerSymbolID] {
				continue
			}
			expanded[call.CallerSymbolID] = true
			changed = true
		}
	}
	return expanded
}

func expandForwardingCallees(g *graph.Graph, seeds map[string]bool) map[string]bool {
	expanded := make(map[string]bool, len(seeds))
	for id := range seeds {
		expanded[id] = true
	}
	for changed := true; changed; {
		changed = false
		for _, call := range g.Calls {
			if !call.Synthetic || call.CallerSymbolID == "" || call.CalleeSymbolID == "" || !expanded[call.CallerSymbolID] || expanded[call.CalleeSymbolID] {
				continue
			}
			expanded[call.CalleeSymbolID] = true
			changed = true
		}
	}
	return expanded
}

// expandTransparentCallerTargets returns every identity that represents the
// same executable method across synthetic forwarding wrappers. Reverse
// expansion lets a query for the declared embedded method match calls to its
// promoted wrapper; forward expansion lets a query for the wrapper reach the
// source method that actually executes. Synthetic forwarding consumes no
// user-visible call depth.
func expandTransparentCallerTargets(g *graph.Graph, seeds map[string]bool) map[string]bool {
	return expandForwardingCallees(g, expandForwardingTargets(g, seeds))
}

func symbolFromID(id string) (graph.SymbolNode, bool) {
	separator := strings.LastIndex(id, "::")
	if separator <= 0 || separator+2 >= len(id) {
		return graph.SymbolNode{}, false
	}
	packagePath := id[:separator]
	symbolPart := id[separator+2:]
	packageName := packagePath
	if slash := strings.LastIndex(packageName, "/"); slash >= 0 {
		packageName = packageName[slash+1:]
	}

	symbol := graph.SymbolNode{
		ID:          id,
		Kind:        graph.KindFunction,
		Name:        symbolPart,
		PackageName: packageName,
	}
	if strings.HasPrefix(symbolPart, "(") {
		closeReceiver := strings.Index(symbolPart, ").")
		if closeReceiver <= 1 || closeReceiver+2 >= len(symbolPart) {
			return graph.SymbolNode{}, false
		}
		symbol.Kind = graph.KindMethod
		symbol.Receiver = symbolPart[1:closeReceiver]
		symbol.Name = symbolPart[closeReceiver+2:]
	}
	return symbol, symbol.Name != ""
}
