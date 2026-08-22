package workspace

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

type QueryResult struct {
	Node   NodeRef `json:"node"`
	Name   string  `json:"name"`
	File   string  `json:"file,omitempty"`
	Line   int     `json:"line,omitempty"`
	Detail string  `json:"detail,omitempty"`
}

type QueryResponse struct {
	SchemaVersion string        `json:"schema_version"`
	WorkspaceName string        `json:"workspace_name"`
	Scope         string        `json:"scope"`
	Query         string        `json:"query"`
	Results       []QueryResult `json:"results"`
}

type VirtualEdge struct {
	From             NodeRef          `json:"from"`
	To               NodeRef          `json:"to"`
	Kind             string           `json:"kind"`
	File             string           `json:"file,omitempty"`
	Line             int              `json:"line,omitempty"`
	ResolutionStatus ResolutionStatus `json:"resolution_status"`
	EvidenceOrigin   EvidenceOrigin   `json:"evidence_origin"`
}

type PathResponse struct {
	SchemaVersion string        `json:"schema_version"`
	WorkspaceName string        `json:"workspace_name"`
	Scope         string        `json:"scope"`
	From          NodeRef       `json:"from"`
	To            NodeRef       `json:"to"`
	Found         bool          `json:"found"`
	Steps         []VirtualEdge `json:"steps"`
}

type ImpactResponse struct {
	SchemaVersion string        `json:"schema_version"`
	WorkspaceName string        `json:"workspace_name"`
	Scope         string        `json:"scope"`
	Target        NodeRef       `json:"target"`
	Affected      []QueryResult `json:"affected"`
}

func SelectScope(workspace *LoadedWorkspace, requested string) (ScopeOverlay, error) {
	if workspace == nil || workspace.Artifact == nil {
		return ScopeOverlay{}, fmt.Errorf("workspace is not loaded")
	}
	selected := requested
	if selected == "" {
		selected = workspace.Artifact.DefaultScope
		if selected == "" && len(workspace.Artifact.Scopes) == 1 {
			selected = workspace.Artifact.Scopes[0].ID
		}
		if selected == "" {
			return ScopeOverlay{}, fmt.Errorf("workspace contains multiple scopes; --scope is required")
		}
	}
	for _, scope := range workspace.Artifact.Scopes {
		if scope.ID == selected {
			return scope, nil
		}
	}
	return ScopeOverlay{}, fmt.Errorf("unknown workspace scope %q", selected)
}

func Query(workspace *LoadedWorkspace, scope ScopeOverlay, term string) QueryResponse {
	response := QueryResponse{SchemaVersion: QuerySchemaVersion, WorkspaceName: workspace.Manifest.Name, Scope: scope.ID, Query: term, Results: []QueryResult{}}
	needle := strings.ToLower(term)
	repositories := stringSet(scope.Repositories)
	seen := make(map[string]bool)
	for _, member := range workspace.Members {
		if !repositories[member.Config.ID] {
			continue
		}
		for _, symbol := range member.Graph.Symbols {
			display := displaySymbolName(symbol)
			if !strings.Contains(strings.ToLower(symbol.ID), needle) && !strings.Contains(strings.ToLower(display), needle) && !strings.Contains(strings.ToLower(symbol.Doc), needle) {
				continue
			}
			result := QueryResult{Node: symbolRef(member, symbol), Name: display, File: symbol.File, Line: symbol.Line, Detail: string(symbol.Kind)}
			appendQueryResult(&response.Results, seen, result)
		}
		for _, pkg := range member.Graph.Packages {
			if strings.Contains(strings.ToLower(pkg.ID), needle) || strings.Contains(strings.ToLower(pkg.Name), needle) {
				appendQueryResult(&response.Results, seen, QueryResult{Node: NodeRef{RepositoryID: member.Config.ID, ModuleID: moduleForNode(member, pkg.ImportPathBestEffort), NodeID: pkg.ID, Kind: "package", Language: "go"}, Name: pkg.Name, File: pkg.Dir})
			}
		}
		for _, module := range member.Record.Modules {
			if strings.Contains(strings.ToLower(module.Path), needle) {
				ref := NodeRef{RepositoryID: member.Config.ID, ModuleID: module.ID, NodeID: module.Path, Kind: "module", Language: "go"}
				appendQueryResult(&response.Results, seen, QueryResult{Node: ref, Name: module.Path, File: module.Dir, Detail: "module"})
			}
		}
	}
	for _, contract := range scope.HTTPContracts {
		name := contract.ID.Method + " " + contract.ID.AuthorityID + contract.ID.NormalizedPath
		if strings.Contains(strings.ToLower(name), needle) {
			appendQueryResult(&response.Results, seen, QueryResult{Node: contractNode(contract.ID), Name: name, Detail: "http_contract"})
		}
	}
	sort.Slice(response.Results, func(i, j int) bool {
		return displayNode(response.Results[i].Node) < displayNode(response.Results[j].Node)
	})
	return response
}

func Path(workspace *LoadedWorkspace, scope ScopeOverlay, fromQuery, toQuery string, includePossible bool) (PathResponse, error) {
	from, err := ResolveSelector(workspace, scope, fromQuery)
	if err != nil {
		return PathResponse{}, fmt.Errorf("resolve path source: %w", err)
	}
	to, err := ResolveSelector(workspace, scope, toQuery)
	if err != nil {
		return PathResponse{}, fmt.Errorf("resolve path destination: %w", err)
	}
	response := PathResponse{SchemaVersion: QuerySchemaVersion, WorkspaceName: workspace.Manifest.Name, Scope: scope.ID, From: from, To: to, Steps: []VirtualEdge{}}
	edges := VirtualEdges(workspace, scope, includePossible)
	adjacency := make(map[string][]VirtualEdge)
	for _, edge := range edges {
		adjacency[nodeKey(edge.From)] = append(adjacency[nodeKey(edge.From)], edge)
	}
	type state struct {
		node NodeRef
		path []VirtualEdge
	}
	queue := []state{{node: from}}
	visited := map[string]bool{nodeKey(from): true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if nodeKey(current.node) == nodeKey(to) {
			response.Found = true
			response.Steps = current.path
			return response, nil
		}
		for _, edge := range adjacency[nodeKey(current.node)] {
			key := nodeKey(edge.To)
			if visited[key] {
				continue
			}
			visited[key] = true
			nextPath := append(append([]VirtualEdge(nil), current.path...), edge)
			queue = append(queue, state{node: edge.To, path: nextPath})
		}
	}
	return response, nil
}

func Impact(workspace *LoadedWorkspace, scope ScopeOverlay, targetQuery string, includePossible bool) (ImpactResponse, error) {
	target, err := ResolveSelector(workspace, scope, targetQuery)
	if err != nil {
		return ImpactResponse{}, err
	}
	response := ImpactResponse{SchemaVersion: QuerySchemaVersion, WorkspaceName: workspace.Manifest.Name, Scope: scope.ID, Target: target, Affected: []QueryResult{}}
	reverse := make(map[string][]VirtualEdge)
	for _, edge := range VirtualEdges(workspace, scope, includePossible) {
		reverse[nodeKey(edge.To)] = append(reverse[nodeKey(edge.To)], edge)
	}
	queue := []NodeRef{target}
	visited := map[string]bool{nodeKey(target): true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range reverse[nodeKey(current)] {
			key := nodeKey(edge.From)
			if visited[key] {
				continue
			}
			visited[key] = true
			queue = append(queue, edge.From)
			response.Affected = append(response.Affected, resultForNode(workspace, edge.From, "reaches "+displayNode(target)))
		}
	}
	sort.Slice(response.Affected, func(i, j int) bool {
		return displayNode(response.Affected[i].Node) < displayNode(response.Affected[j].Node)
	})
	return response, nil
}

func ResolveSelector(workspace *LoadedWorkspace, scope ScopeOverlay, selector string) (NodeRef, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return NodeRef{}, fmt.Errorf("selector is empty")
	}
	repositoryFilter := ""
	query := selector
	if prefix, rest, ok := strings.Cut(selector, ":"); ok && !strings.HasPrefix(rest, ":") && containsString(scope.Repositories, prefix) {
		repositoryFilter, query = prefix, rest
	}
	repositories := stringSet(scope.Repositories)
	var exact, fuzzy []NodeRef
	for _, member := range workspace.Members {
		if !repositories[member.Config.ID] || repositoryFilter != "" && repositoryFilter != member.Config.ID {
			continue
		}
		for _, symbol := range member.Graph.Symbols {
			ref := symbolRef(member, symbol)
			display := displaySymbolName(symbol)
			if symbol.ID == query || display == query || symbol.Name == query || selectorPathMatches(symbol.ID, query) {
				exact = append(exact, ref)
			} else if strings.Contains(strings.ToLower(symbol.ID), strings.ToLower(query)) || strings.Contains(strings.ToLower(display), strings.ToLower(query)) {
				fuzzy = append(fuzzy, ref)
			}
		}
		for _, pkg := range member.Graph.Packages {
			ref := NodeRef{RepositoryID: member.Config.ID, ModuleID: moduleForNode(member, pkg.ImportPathBestEffort), NodeID: pkg.ID, Kind: "package", Language: "go"}
			if pkg.ID == query || pkg.Name == query || pkg.ImportPathBestEffort == query {
				exact = append(exact, ref)
			} else if strings.Contains(strings.ToLower(pkg.ID), strings.ToLower(query)) || strings.Contains(strings.ToLower(pkg.ImportPathBestEffort), strings.ToLower(query)) {
				fuzzy = append(fuzzy, ref)
			}
		}
		for _, module := range member.Record.Modules {
			ref := NodeRef{RepositoryID: member.Config.ID, ModuleID: module.ID, NodeID: module.Path, Kind: "module", Language: "go"}
			if module.Path == query || module.ID == query {
				exact = append(exact, ref)
			} else if strings.Contains(strings.ToLower(module.Path), strings.ToLower(query)) {
				fuzzy = append(fuzzy, ref)
			}
		}
	}
	if repositoryFilter == "" {
		for _, contract := range scope.HTTPContracts {
			ref := contractNode(contract.ID)
			display := contract.ID.Method + " " + contract.ID.AuthorityID + contract.ID.NormalizedPath
			if ref.NodeID == query || display == query {
				exact = append(exact, ref)
			} else if strings.Contains(strings.ToLower(ref.NodeID), strings.ToLower(query)) || strings.Contains(strings.ToLower(display), strings.ToLower(query)) {
				fuzzy = append(fuzzy, ref)
			}
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return NodeRef{}, ambiguityError(selector, exact)
	}
	if len(fuzzy) == 1 {
		return fuzzy[0], nil
	}
	if len(fuzzy) > 1 {
		return NodeRef{}, ambiguityError(selector, fuzzy)
	}
	return NodeRef{}, fmt.Errorf("selector %q did not match a node in scope %q", selector, scope.ID)
}

func VirtualEdges(workspace *LoadedWorkspace, scope ScopeOverlay, includePossible bool) []VirtualEdge {
	allowed := stringSet(scope.Repositories)
	var edges []VirtualEdge
	for _, member := range workspace.Members {
		if !allowed[member.Config.ID] {
			continue
		}
		for _, call := range member.Graph.Calls {
			from := callerRef(member, call)
			if call.CalleeSymbolID == "" {
				if !includePossible || call.Synthetic {
					continue
				}
				for _, target := range localCallCandidates(member, call.CalleeRaw) {
					edges = append(edges, VirtualEdge{From: from, To: target, Kind: "calls", File: call.File, Line: call.Line, ResolutionStatus: ResolutionPossible, EvidenceOrigin: EvidenceDerived})
				}
				continue
			}
			var to NodeRef
			for _, symbol := range member.Graph.Symbols {
				if symbol.ID == call.CalleeSymbolID {
					to = symbolRef(member, symbol)
					break
				}
			}
			if to.NodeID != "" {
				status := ResolutionExact
				if call.Resolution == graph.CallResolutionCHA {
					status = ResolutionPossible
				}
				if traversable(status, includePossible) {
					edges = append(edges, VirtualEdge{From: from, To: to, Kind: "calls", File: call.File, Line: call.Line, ResolutionStatus: status, EvidenceOrigin: EvidenceStructural})
				}
			}
		}
	}
	for _, resolution := range scope.Imports {
		if traversable(resolution.ResolutionStatus, includePossible) {
			edges = append(edges, VirtualEdge{From: resolution.Source, To: resolution.Target, Kind: "imports_module", File: resolution.File, ResolutionStatus: resolution.ResolutionStatus, EvidenceOrigin: resolution.EvidenceOrigin})
		}
	}
	for _, resolution := range scope.GoCalls {
		if !traversable(resolution.ResolutionStatus, includePossible) {
			continue
		}
		for _, target := range resolution.Targets {
			edges = append(edges, VirtualEdge{From: resolution.Source, To: target, Kind: "calls", File: resolution.LocalCall.File, Line: resolution.LocalCall.Line, ResolutionStatus: resolution.ResolutionStatus, EvidenceOrigin: resolution.EvidenceOrigin})
		}
	}
	for _, relation := range scope.HTTPRelations {
		if !traversable(relation.ResolutionStatus, includePossible) {
			continue
		}
		contract := contractNode(relation.Contract)
		edge := VirtualEdge{Kind: relation.Kind, File: relation.File, Line: relation.Line, ResolutionStatus: relation.ResolutionStatus, EvidenceOrigin: relation.EvidenceOrigin}
		if relation.Kind == "calls_http" {
			edge.From, edge.To = relation.Source, contract
		} else {
			edge.From, edge.To = contract, relation.Source
		}
		edges = append(edges, edge)
	}
	edges = dedupeVirtualEdges(edges)
	sort.Slice(edges, func(i, j int) bool {
		a := nodeKey(edges[i].From) + "\x00" + edges[i].Kind + "\x00" + nodeKey(edges[i].To) + "\x00" + string(edges[i].ResolutionStatus) + "\x00" + edges[i].File
		b := nodeKey(edges[j].From) + "\x00" + edges[j].Kind + "\x00" + nodeKey(edges[j].To) + "\x00" + string(edges[j].ResolutionStatus) + "\x00" + edges[j].File
		if a != b {
			return a < b
		}
		return edges[i].Line < edges[j].Line
	})
	return edges
}

func localCallCandidates(member LoadedMember, calleeRaw string) []NodeRef {
	name := calleeRaw
	if index := strings.LastIndex(name, "."); index >= 0 {
		name = name[index+1:]
	}
	if name == "" {
		return nil
	}
	var refs []NodeRef
	for _, symbol := range member.Graph.Symbols {
		if symbol.Name == name || displaySymbolName(symbol) == calleeRaw {
			refs = append(refs, symbolRef(member, symbol))
		}
	}
	return refs
}

func traversable(status ResolutionStatus, includePossible bool) bool {
	return status == ResolutionExact || includePossible && (status == ResolutionAmbiguous || status == ResolutionPossible)
}

func contractNode(id HTTPContractID) NodeRef {
	return NodeRef{NodeID: id.AuthorityID + " " + id.Method + " " + id.NormalizedPath, Kind: "http_contract"}
}

func resultForNode(workspace *LoadedWorkspace, node NodeRef, detail string) QueryResult {
	result := QueryResult{Node: node, Name: displayNode(node), Detail: detail}
	for _, member := range workspace.Members {
		if member.Config.ID != node.RepositoryID {
			continue
		}
		for _, symbol := range member.Graph.Symbols {
			if symbol.ID == node.NodeID {
				result.Name, result.File, result.Line = displaySymbolName(symbol), symbol.File, symbol.Line
				return result
			}
		}
	}
	return result
}

func displayNode(node NodeRef) string {
	if node.RepositoryID != "" {
		return node.RepositoryID + ":" + node.NodeID
	}
	return node.Kind + ":" + node.NodeID
}

func nodeKey(node NodeRef) string {
	return node.RepositoryID + "\x00" + node.ModuleID + "\x00" + node.Kind + "\x00" + node.Language + "\x00" + node.NodeID
}

func selectorPathMatches(symbolID, query string) bool {
	index := strings.LastIndex(query, ".")
	if index < 0 {
		return false
	}
	return strings.HasSuffix(symbolID, query[:index]+"::"+query[index+1:])
}

func ambiguityError(selector string, refs []NodeRef) error {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, displayNode(ref))
	}
	sort.Strings(names)
	return fmt.Errorf("selector %q is ambiguous: %s", selector, strings.Join(names, ", "))
}

func appendQueryResult(results *[]QueryResult, seen map[string]bool, result QueryResult) {
	key := nodeKey(result.Node)
	if seen[key] {
		return
	}
	seen[key] = true
	*results = append(*results, result)
}

func dedupeVirtualEdges(edges []VirtualEdge) []VirtualEdge {
	seen := make(map[string]bool)
	result := make([]VirtualEdge, 0, len(edges))
	for _, edge := range edges {
		key := nodeKey(edge.From) + "\x00" + nodeKey(edge.To) + "\x00" + edge.Kind + "\x00" + string(edge.ResolutionStatus)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, edge)
	}
	return result
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
