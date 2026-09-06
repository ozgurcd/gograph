package workspace

import (
	"fmt"
	pathpkg "path"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

type serviceOwner struct {
	repositoryID string
	authorityID  string
	aliases      []string
	shared       bool
}

type routeContract struct {
	contract HTTPContractID
	handler  NodeRef
	status   ResolutionStatus
	file     string
	line     int
}

func Resolve(manifest Manifest, members []LoadedMember) (*Artifact, error) {
	members = append([]LoadedMember(nil), members...)
	configured := make(map[string]RepositoryConfig, len(manifest.Repositories))
	for _, repository := range manifest.Repositories {
		if _, exists := configured[repository.ID]; exists {
			return nil, fmt.Errorf("duplicate configured repository %q", repository.ID)
		}
		configured[repository.ID] = repository
	}
	seenMembers := make(map[string]bool, len(members))
	for index := range members {
		member := &members[index]
		config, exists := configured[member.Config.ID]
		if !exists {
			return nil, fmt.Errorf("loaded member %q is not configured by the workspace", member.Config.ID)
		}
		if seenMembers[member.Config.ID] {
			return nil, fmt.Errorf("loaded member %q is duplicated", member.Config.ID)
		}
		if member.Graph == nil || member.Graph.Build == nil {
			return nil, fmt.Errorf("loaded member %q has no usable repository graph", member.Config.ID)
		}
		if member.Record.RepositoryID != member.Config.ID || config.Path != "" && member.Record.Path != config.Path {
			return nil, fmt.Errorf("loaded member %q record does not belong to its configured repository", member.Config.ID)
		}
		if !sameModuleInventory(member.Record.Modules, member.Graph.Modules) {
			return nil, fmt.Errorf("loaded member %q record module inventory differs from its repository graph", member.Config.ID)
		}
		seenMembers[member.Config.ID] = true
		member.Config = config
	}
	for repositoryID := range configured {
		if !seenMembers[repositoryID] {
			return nil, fmt.Errorf("configured repository %q has no loaded member graph", repositoryID)
		}
	}
	seenScopes := make(map[string]bool, len(manifest.Scopes))
	for _, scope := range manifest.Scopes {
		if seenScopes[scope.ID] {
			return nil, fmt.Errorf("configured scope %q is duplicated", scope.ID)
		}
		seenScopes[scope.ID] = true
		seenRepositories := make(map[string]bool, len(scope.Repositories))
		for _, repositoryID := range scope.Repositories {
			if _, exists := configured[repositoryID]; !exists {
				return nil, fmt.Errorf("scope %q references unknown repository %q", scope.ID, repositoryID)
			}
			if seenRepositories[repositoryID] {
				return nil, fmt.Errorf("scope %q repeats repository %q", scope.ID, repositoryID)
			}
			seenRepositories[repositoryID] = true
		}
	}
	if manifest.DefaultScope != "" && !seenScopes[manifest.DefaultScope] {
		return nil, fmt.Errorf("default scope %q is not configured", manifest.DefaultScope)
	}
	inputFingerprint, err := InputFingerprint(manifest, members)
	if err != nil {
		return nil, err
	}
	artifact := &Artifact{
		SchemaVersion:    ArtifactSchemaVersion,
		WorkspaceName:    manifest.Name,
		DefaultScope:     manifest.DefaultScope,
		InputFingerprint: inputFingerprint,
		ResolverVersions: copyStringMap(ResolverVersions),
	}
	memberMap := make(map[string]LoadedMember, len(members))
	for _, member := range members {
		memberMap[member.Config.ID] = member
		artifact.Members = append(artifact.Members, member.Record)
	}
	sort.Slice(artifact.Members, func(i, j int) bool { return artifact.Members[i].RepositoryID < artifact.Members[j].RepositoryID })
	for _, scope := range manifest.Scopes {
		overlay, err := resolveScope(scope, memberMap)
		if err != nil {
			return nil, fmt.Errorf("scope %q: %w", scope.ID, err)
		}
		artifact.Scopes = append(artifact.Scopes, overlay)
	}
	sort.Slice(artifact.Scopes, func(i, j int) bool { return artifact.Scopes[i].ID < artifact.Scopes[j].ID })
	return artifact, nil
}

func sameModuleInventory(left, right []graph.ModuleNode) bool {
	if len(left) != len(right) {
		return false
	}
	a := append([]graph.ModuleNode(nil), left...)
	b := append([]graph.ModuleNode(nil), right...)
	sort.Slice(a, func(i, j int) bool {
		return a[i].Path+"\x00"+a[i].Dir+"\x00"+a[i].ID < a[j].Path+"\x00"+a[j].Dir+"\x00"+a[j].ID
	})
	sort.Slice(b, func(i, j int) bool {
		return b[i].Path+"\x00"+b[i].Dir+"\x00"+b[i].ID < b[j].Path+"\x00"+b[j].Dir+"\x00"+b[j].ID
	})
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

func resolveScope(scope ScopeConfig, members map[string]LoadedMember) (ScopeOverlay, error) {
	overlay := ScopeOverlay{ID: scope.ID, Repositories: append([]string(nil), scope.Repositories...)}
	selected := make(map[string]LoadedMember, len(scope.Repositories))
	moduleOwners := make(map[string]NodeRef)
	for _, repositoryID := range scope.Repositories {
		member := members[repositoryID]
		selected[repositoryID] = member
		for _, module := range member.Record.Modules {
			owner := NodeRef{RepositoryID: repositoryID, ModuleID: module.ID, NodeID: module.Path, Kind: "module", Language: "go"}
			if previous, exists := moduleOwners[module.Path]; exists {
				return ScopeOverlay{}, fmt.Errorf("duplicate module ownership for %q by %q and %q", module.Path, previous.RepositoryID, repositoryID)
			}
			moduleOwners[module.Path] = owner
			overlay.Modules = append(overlay.Modules, ModuleOwnership{ModulePath: module.Path, Owner: owner, ResolutionStatus: ResolutionExact, EvidenceOrigin: EvidenceStructural, Resolver: ResolverVersions["go_module"]})
		}
	}
	services, aliases, err := resolveServices(selected)
	if err != nil {
		return ScopeOverlay{}, err
	}
	for _, repositoryID := range scope.Repositories {
		member := selected[repositoryID]
		resolveImports(&overlay, member, moduleOwners)
		resolveGoCalls(&overlay, member, selected, moduleOwners)
	}
	resolveHTTP(&overlay, selected, services, aliases)
	sortScope(&overlay)
	return overlay, nil
}

func resolveImports(overlay *ScopeOverlay, member LoadedMember, owners map[string]NodeRef) {
	for _, imp := range member.Graph.Imports {
		owner, ok := longestModuleOwner(imp.ImportPath, owners)
		if !ok || owner.RepositoryID == member.Config.ID {
			continue
		}
		source := packageRefForFile(member, imp.FromFile)
		overlay.Imports = append(overlay.Imports, ModuleImportResolution{
			Source: source, ImportPath: imp.ImportPath, Target: owner, File: imp.FromFile,
			ResolutionStatus: ResolutionExact, EvidenceOrigin: EvidenceStructural, Resolver: ResolverVersions["go_module"],
		})
	}
}

func resolveGoCalls(overlay *ScopeOverlay, member LoadedMember, members map[string]LoadedMember, owners map[string]NodeRef) {
	importsByFile := make(map[string]map[string]string)
	for _, imp := range member.Graph.Imports {
		alias := imp.Alias
		if alias == "" {
			alias = pathpkg.Base(imp.ImportPath)
		}
		if alias == "." || alias == "_" {
			continue
		}
		file := normalizedFile(imp.FromFile)
		if importsByFile[file] == nil {
			importsByFile[file] = make(map[string]string)
		}
		importsByFile[file][alias] = imp.ImportPath
	}
	for _, call := range member.Graph.Calls {
		if call.CalleeSymbolID != "" {
			packagePath, _, ok := strings.Cut(call.CalleeSymbolID, "::")
			owner, owned := longestModuleOwner(packagePath, owners)
			if !ok || !owned || owner.RepositoryID == member.Config.ID {
				continue
			}
			targetMember := members[owner.RepositoryID]
			var targets []NodeRef
			for _, symbol := range targetMember.Graph.Symbols {
				if symbol.ID == call.CalleeSymbolID {
					targets = append(targets, symbolRef(targetMember, symbol))
				}
			}
			if len(targets) == 0 {
				continue
			}
			status := ResolutionExact
			if call.Resolution == graph.CallResolutionCHA {
				status = ResolutionPossible
			} else if len(targets) > 1 {
				status = ResolutionAmbiguous
			}
			source := callerRef(member, call)
			overlay.GoCalls = append(overlay.GoCalls, GoCallResolution{
				LocalCall: LocalCallRef{RepositoryID: member.Config.ID, CallerSymbolID: source.NodeID, File: call.File, Line: call.Line, Column: call.Column, ExternalTarget: call.CalleeSymbolID},
				Source:    source, Targets: targets, ResolutionStatus: status, EvidenceOrigin: EvidenceStructural, Resolver: ResolverVersions["go_symbol"],
			})
			continue
		}
		if call.Synthetic {
			continue
		}
		selector, targetName, ok := strings.Cut(call.CalleeRaw, ".")
		if !ok || selector == "" || targetName == "" || strings.Contains(targetName, ".") {
			continue
		}
		importPath := importsByFile[normalizedFile(call.File)][selector]
		owner, owned := longestModuleOwner(importPath, owners)
		if importPath == "" || !owned || owner.RepositoryID == member.Config.ID {
			continue
		}
		targetMember := members[owner.RepositoryID]
		var targets []NodeRef
		for _, symbol := range targetMember.Graph.Symbols {
			if symbol.ID == importPath+"::"+targetName {
				targets = append(targets, symbolRef(targetMember, symbol))
			}
		}
		if len(targets) == 0 {
			continue
		}
		// Parser-only selector matching cannot prove that the selector is an
		// imported package rather than a shadowing local value.
		status := ResolutionPossible
		if len(targets) > 1 {
			status = ResolutionAmbiguous
		}
		source := callerRef(member, call)
		overlay.GoCalls = append(overlay.GoCalls, GoCallResolution{
			LocalCall: LocalCallRef{RepositoryID: member.Config.ID, CallerSymbolID: source.NodeID, File: call.File, Line: call.Line, Column: call.Column, ExternalTarget: importPath + "." + targetName},
			Source:    source, Targets: targets, ResolutionStatus: status, EvidenceOrigin: EvidenceStructural, Resolver: ResolverVersions["go_symbol"],
		})
	}
}

func resolveServices(members map[string]LoadedMember) (map[string][]serviceOwner, map[string][]serviceOwner, error) {
	byRepository := make(map[string][]serviceOwner)
	aliases := make(map[string][]serviceOwner)
	logicalOwners := make(map[string][]serviceOwner)
	for repositoryID, member := range members {
		for _, service := range member.Config.Services {
			if len(service.HTTP.Authorities) == 0 {
				continue
			}
			owner := serviceOwner{repositoryID: repositoryID, authorityID: service.ID, aliases: service.HTTP.Authorities, shared: service.HTTP.SharedAuthority}
			byRepository[repositoryID] = append(byRepository[repositoryID], owner)
			logicalOwners[owner.authorityID] = append(logicalOwners[owner.authorityID], owner)
			for _, alias := range owner.aliases {
				aliases[alias] = append(aliases[alias], owner)
			}
		}
		if len(byRepository[repositoryID]) > 1 {
			return nil, nil, fmt.Errorf("repository %q configures multiple HTTP services; workspace.v1 requires one HTTP service per repository", repositoryID)
		}
	}
	for authorityID, owners := range logicalOwners {
		if len(owners) > 1 && !allServiceOwnersShared(owners) {
			return nil, nil, fmt.Errorf("logical HTTP authority %q has multiple owners without explicit shared_authority", authorityID)
		}
	}
	for alias, owners := range aliases {
		if len(owners) < 2 {
			continue
		}
		if !allServiceOwnersShared(owners) {
			return nil, nil, fmt.Errorf("HTTP authority %q has multiple owners without explicit shared_authority", alias)
		}
	}
	return byRepository, aliases, nil
}

func resolveHTTP(overlay *ScopeOverlay, members map[string]LoadedMember, services map[string][]serviceOwner, aliases map[string][]serviceOwner) {
	var routes []routeContract
	contractIndex := make(map[string]int)
	addContract := func(contract HTTPContract, qualifier HTTPQualifier) {
		key := httpContractKey(contract.ID)
		if index, exists := contractIndex[key]; exists {
			if qualifier != (HTTPQualifier{}) {
				overlay.HTTPContracts[index].Qualifiers = appendUniqueQualifier(overlay.HTTPContracts[index].Qualifiers, qualifier)
			}
			return
		}
		if qualifier != (HTTPQualifier{}) {
			contract.Qualifiers = []HTTPQualifier{qualifier}
		}
		contractIndex[key] = len(overlay.HTTPContracts)
		overlay.HTTPContracts = append(overlay.HTTPContracts, contract)
	}
	for repositoryID, member := range members {
		for _, service := range services[repositoryID] {
			for _, route := range member.Graph.Routes {
				contractID := HTTPContractID{AuthorityID: service.authorityID, Method: normalizeHTTPMethod(route.Method), NormalizedPath: normalizeHTTPPath(route.Path)}
				handler, handlerStatus := handlerRef(member, route)
				routes = append(routes, routeContract{contract: contractID, handler: handler, status: handlerStatus, file: route.File, line: route.Line})
				addContract(HTTPContract{ID: contractID}, HTTPQualifier{})
				overlay.HTTPRelations = append(overlay.HTTPRelations, HTTPRelation{Kind: "serves_http", Source: handler, Contract: contractID, File: route.File, Line: route.Line, ResolutionStatus: handlerStatus, EvidenceOrigin: EvidenceDerived, Resolver: ResolverVersions["http"]})
			}
		}
	}
	for _, member := range members {
		for _, call := range member.Graph.HTTPCalls {
			owners, parsed, reason := resolveHTTPDestination(member, call, services, aliases)
			if reason != "" {
				source, _ := functionRefStatus(member, call.FunctionName, call.SourceFile)
				overlay.HTTPUnresolved = append(overlay.HTTPUnresolved, HTTPUnresolved{Source: source, File: call.SourceFile, Line: call.SourceLine, Method: call.Method, URL: call.URL, Base: call.URLBase, Reason: reason, Resolver: ResolverVersions["http"]})
				continue
			}
			source, sourceStatus := functionRefStatus(member, call.FunctionName, call.SourceFile)
			for _, owner := range owners {
				method := normalizeHTTPMethod(call.Method)
				path := normalizeHTTPPath(parsed.Path)
				matched := matchingRoutes(routes, owner.authorityID, method, path)
				contractIDs := matchedHTTPContracts(owner.authorityID, method, path, matched)
				status := sourceStatus
				if method == "ANY" || call.RequestOnly {
					status = combineResolutionStatus(status, ResolutionPossible)
				}
				if distinctAuthorityCount(owners) > 1 || len(contractIDs) > 1 {
					status = combineResolutionStatus(status, ResolutionAmbiguous)
				}
				qualifier := HTTPQualifier{Scheme: strings.ToLower(parsed.Scheme), Host: strings.ToLower(parsed.Hostname()), Port: parsed.Port()}
				for _, contractID := range contractIDs {
					addContract(HTTPContract{ID: contractID}, qualifier)
					overlay.HTTPRelations = append(overlay.HTTPRelations, HTTPRelation{Kind: "calls_http", Source: source, Contract: contractID, File: call.SourceFile, Line: call.SourceLine, ResolutionStatus: status, EvidenceOrigin: EvidenceConfigured, Resolver: ResolverVersions["http"]})
					for _, route := range matched {
						normalizedRouteContract := route.contract
						if normalizedRouteContract.Method == "ANY" {
							normalizedRouteContract.Method = method
						}
						if route.contract.Method != "ANY" || normalizedRouteContract != contractID {
							continue
						}
						derivedStatus := combineResolutionStatus(status, route.status)
						overlay.HTTPRelations = append(overlay.HTTPRelations, HTTPRelation{Kind: "serves_http", Source: route.handler, Contract: contractID, File: route.file, Line: route.line, ResolutionStatus: derivedStatus, EvidenceOrigin: EvidenceDerived, Resolver: ResolverVersions["http"]})
					}
				}
			}
		}
	}
}

func longestModuleOwner(importPath string, owners map[string]NodeRef) (NodeRef, bool) {
	var best string
	for modulePath := range owners {
		if importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/") {
			if len(modulePath) > len(best) {
				best = modulePath
			}
		}
	}
	owner, ok := owners[best]
	return owner, ok
}

func packageRefForFile(member LoadedMember, file string) NodeRef {
	file = normalizedFile(file)
	for _, pkg := range member.Graph.Packages {
		for _, pkgFile := range pkg.Files {
			if normalizedFile(pkgFile) == file {
				return NodeRef{RepositoryID: member.Config.ID, ModuleID: moduleForNode(member, pkg.ImportPathBestEffort), NodeID: pkg.ID, Kind: "package", Language: "go"}
			}
		}
	}
	return NodeRef{RepositoryID: member.Config.ID, NodeID: file, Kind: "file", Language: "go"}
}

func callerRef(member LoadedMember, call graph.CallEdge) NodeRef {
	if call.CallerSymbolID != "" {
		for _, symbol := range member.Graph.Symbols {
			if symbol.ID == call.CallerSymbolID {
				return symbolRef(member, symbol)
			}
		}
	}
	return functionRef(member, call.CallerName, call.File)
}

func functionRef(member LoadedMember, name, file string) NodeRef {
	ref, _ := functionRefStatus(member, name, file)
	return ref
}

func functionRefStatus(member LoadedMember, name, file string) (NodeRef, ResolutionStatus) {
	var matches []graph.SymbolNode
	for _, symbol := range member.Graph.Symbols {
		if symbol.Name == name || displaySymbolName(symbol) == name || strings.HasSuffix(name, "."+symbol.Name) {
			if file == "" || normalizedFile(symbol.File) == normalizedFile(file) {
				matches = append(matches, symbol)
			}
		}
	}
	if len(matches) == 1 {
		return symbolRef(member, matches[0]), ResolutionExact
	}
	status := ResolutionPossible
	if len(matches) > 1 {
		status = ResolutionAmbiguous
	}
	return NodeRef{RepositoryID: member.Config.ID, NodeID: name, Kind: "symbol", Language: "go"}, status
}

func handlerRef(member LoadedMember, route graph.HTTPRoute) (NodeRef, ResolutionStatus) {
	ref, status := functionRefStatus(member, route.Handler, route.File)
	if route.DynamicHandler {
		status = ResolutionPossible
	}
	return ref, status
}

func symbolRef(member LoadedMember, symbol graph.SymbolNode) NodeRef {
	return NodeRef{RepositoryID: member.Config.ID, ModuleID: moduleForNode(member, symbol.ID), NodeID: symbol.ID, Kind: "symbol", Language: "go"}
}

func moduleForNode(member LoadedMember, nodeID string) string {
	var best string
	for _, module := range member.Record.Modules {
		if (nodeID == module.Path || strings.HasPrefix(nodeID, module.Path+"/") || strings.HasPrefix(nodeID, module.Path+"::")) && len(module.Path) > len(best) {
			best = module.Path
		}
	}
	return best
}

func normalizeHTTPMethod(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	switch method {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "CONNECT", "TRACE":
		return method
	default:
		return "ANY"
	}
}

func normalizeHTTPPath(value string) string {
	if value == "" {
		return "/"
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	parts := strings.Split(pathpkg.Clean(value), "/")
	for index, part := range parts {
		if strings.HasPrefix(part, ":") || strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			parts[index] = "{}"
		} else if strings.HasPrefix(part, "*") {
			parts[index] = "*"
		}
	}
	return strings.Join(parts, "/")
}

func matchingRoutes(routes []routeContract, authority, method, path string) []routeContract {
	var matched []routeContract
	for _, route := range routes {
		if route.contract.AuthorityID != authority || route.contract.Method != "ANY" && route.contract.Method != method {
			continue
		}
		if httpPathMatches(route.contract.NormalizedPath, path) {
			matched = append(matched, route)
		}
	}
	return matched
}

func matchedHTTPContracts(authority, method, path string, routes []routeContract) []HTTPContractID {
	if len(routes) == 0 {
		return []HTTPContractID{{AuthorityID: authority, Method: method, NormalizedPath: path}}
	}
	seen := make(map[string]bool)
	var contracts []HTTPContractID
	for _, route := range routes {
		contract := route.contract
		if contract.Method == "ANY" {
			contract.Method = method
		}
		key := httpContractKey(contract)
		if !seen[key] {
			seen[key] = true
			contracts = append(contracts, contract)
		}
	}
	sort.Slice(contracts, func(i, j int) bool { return httpContractKey(contracts[i]) < httpContractKey(contracts[j]) })
	return contracts
}

func httpPathMatches(pattern, actual string) bool {
	p := strings.Split(strings.Trim(pattern, "/"), "/")
	a := strings.Split(strings.Trim(actual, "/"), "/")
	for index := range p {
		if p[index] == "*" {
			return index < len(a)
		}
		if index >= len(a) || p[index] != "{}" && p[index] != a[index] {
			return false
		}
	}
	return len(p) == len(a)
}

func httpContractKey(id HTTPContractID) string {
	return id.AuthorityID + "\x00" + id.Method + "\x00" + id.NormalizedPath
}
func normalizedFile(file string) string { return filepathSlashClean(file) }
func filepathSlashClean(file string) string {
	return strings.TrimPrefix(pathpkg.Clean(strings.ReplaceAll(file, "\\", "/")), "./")
}
func displaySymbolName(symbol graph.SymbolNode) string {
	if symbol.Receiver != "" {
		return "(" + symbol.Receiver + ")." + symbol.Name
	}
	return symbol.Name
}
func appendUniqueQualifier(values []HTTPQualifier, qualifier HTTPQualifier) []HTTPQualifier {
	for _, value := range values {
		if value == qualifier {
			return values
		}
	}
	return append(values, qualifier)
}
func distinctAuthorityCount(owners []serviceOwner) int {
	seen := map[string]bool{}
	for _, owner := range owners {
		seen[owner.authorityID] = true
	}
	return len(seen)
}
func copyStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func sortScope(scope *ScopeOverlay) {
	sort.Strings(scope.Repositories)
	sort.Slice(scope.Modules, func(i, j int) bool {
		a, b := scope.Modules[i], scope.Modules[j]
		if a.ModulePath != b.ModulePath {
			return a.ModulePath < b.ModulePath
		}
		return nodeSortKey(a.Owner) < nodeSortKey(b.Owner)
	})
	sort.Slice(scope.Imports, func(i, j int) bool {
		a, b := scope.Imports[i], scope.Imports[j]
		if a.Source.RepositoryID != b.Source.RepositoryID {
			return a.Source.RepositoryID < b.Source.RepositoryID
		}
		if a.Source.NodeID != b.Source.NodeID {
			return a.Source.NodeID < b.Source.NodeID
		}
		if a.ImportPath != b.ImportPath {
			return a.ImportPath < b.ImportPath
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if nodeSortKey(a.Target) != nodeSortKey(b.Target) {
			return nodeSortKey(a.Target) < nodeSortKey(b.Target)
		}
		return string(a.ResolutionStatus)+"\x00"+string(a.EvidenceOrigin)+"\x00"+a.Resolver < string(b.ResolutionStatus)+"\x00"+string(b.EvidenceOrigin)+"\x00"+b.Resolver
	})
	sort.Slice(scope.GoCalls, func(i, j int) bool {
		a, b := scope.GoCalls[i].LocalCall, scope.GoCalls[j].LocalCall
		if a.RepositoryID != b.RepositoryID {
			return a.RepositoryID < b.RepositoryID
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		if a.CallerSymbolID != b.CallerSymbolID {
			return a.CallerSymbolID < b.CallerSymbolID
		}
		if a.ExternalTarget != b.ExternalTarget {
			return a.ExternalTarget < b.ExternalTarget
		}
		return goCallResolutionSortKey(scope.GoCalls[i]) < goCallResolutionSortKey(scope.GoCalls[j])
	})
	for index := range scope.GoCalls {
		sort.Slice(scope.GoCalls[index].Targets, func(i, j int) bool {
			return nodeSortKey(scope.GoCalls[index].Targets[i]) < nodeSortKey(scope.GoCalls[index].Targets[j])
		})
	}
	sort.Slice(scope.HTTPContracts, func(i, j int) bool {
		return httpContractKey(scope.HTTPContracts[i].ID) < httpContractKey(scope.HTTPContracts[j].ID)
	})
	for index := range scope.HTTPContracts {
		sort.Slice(scope.HTTPContracts[index].Qualifiers, func(i, j int) bool {
			a, b := scope.HTTPContracts[index].Qualifiers[i], scope.HTTPContracts[index].Qualifiers[j]
			return a.Scheme+"\x00"+a.Host+"\x00"+a.Port < b.Scheme+"\x00"+b.Host+"\x00"+b.Port
		})
	}
	sort.Slice(scope.HTTPRelations, func(i, j int) bool {
		a, b := scope.HTTPRelations[i], scope.HTTPRelations[j]
		if httpContractKey(a.Contract) != httpContractKey(b.Contract) {
			return httpContractKey(a.Contract) < httpContractKey(b.Contract)
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Source.RepositoryID != b.Source.RepositoryID {
			return a.Source.RepositoryID < b.Source.RepositoryID
		}
		if a.Source.NodeID != b.Source.NodeID {
			return a.Source.NodeID < b.Source.NodeID
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return string(a.ResolutionStatus)+"\x00"+string(a.EvidenceOrigin)+"\x00"+a.Resolver < string(b.ResolutionStatus)+"\x00"+string(b.EvidenceOrigin)+"\x00"+b.Resolver
	})
	scope.HTTPRelations = dedupeHTTPRelations(scope.HTTPRelations)
	sortHTTPUnresolved(scope.HTTPUnresolved)
}

func nodeSortKey(node NodeRef) string {
	return node.RepositoryID + "\x00" + node.ModuleID + "\x00" + node.Kind + "\x00" + node.Language + "\x00" + node.NodeID
}

func goCallResolutionSortKey(resolution GoCallResolution) string {
	targets := make([]string, 0, len(resolution.Targets))
	for _, target := range resolution.Targets {
		targets = append(targets, nodeSortKey(target))
	}
	sort.Strings(targets)
	return nodeSortKey(resolution.Source) + "\x00" + strings.Join(targets, "\x01") + "\x00" + string(resolution.ResolutionStatus) + "\x00" + string(resolution.EvidenceOrigin) + "\x00" + resolution.Resolver
}

func dedupeHTTPRelations(relations []HTTPRelation) []HTTPRelation {
	seen := make(map[string]bool)
	result := make([]HTTPRelation, 0, len(relations))
	for _, relation := range relations {
		key := relation.Kind + "\x00" + nodeSortKey(relation.Source) + "\x00" + httpContractKey(relation.Contract) + "\x00" + string(relation.ResolutionStatus) + "\x00" + relation.File + fmt.Sprint("\x00", relation.Line)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, relation)
	}
	return result
}

func combineResolutionStatus(statuses ...ResolutionStatus) ResolutionStatus {
	result := ResolutionExact
	for _, status := range statuses {
		switch status {
		case ResolutionPossible:
			return ResolutionPossible
		case ResolutionAmbiguous:
			result = ResolutionAmbiguous
		}
	}
	return result
}
