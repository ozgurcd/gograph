package validation

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

// policyScope decides whether a source file is exempt through the allow list
// and whether it is checked at all.
type policyScope struct {
	allowedFiles    map[string]bool
	allowedPackages []string
	includeTests    bool
	packageByFile   map[string]string
}

func newPolicyScope(binding Binding, g *graph.Graph) policyScope {
	scope := policyScope{allowedFiles: make(map[string]bool), includeTests: binding.IncludeTests, packageByFile: make(map[string]string)}
	for _, allowed := range binding.Allow {
		switch allowed.Kind {
		case ReferenceFile:
			scope.allowedFiles[allowed.ID] = true
		case ReferencePackage:
			scope.allowedPackages = append(scope.allowedPackages, allowed.ID)
		}
	}
	for _, pkg := range g.Packages {
		for _, file := range pkg.Files {
			scope.packageByFile[path.Clean(filepathToSlash(file))] = pkg.ImportPathBestEffort
		}
	}
	return scope
}

func filepathToSlash(name string) string { return strings.ReplaceAll(name, "\\", "/") }

func (scope policyScope) checked(file string) bool {
	return scope.includeTests || !strings.HasSuffix(file, "_test.go")
}

func (scope policyScope) allowed(file, importPath string) bool {
	if scope.allowedFiles[path.Clean(file)] {
		return true
	}
	for _, pattern := range scope.allowedPackages {
		if prefix, ok := strings.CutSuffix(pattern, "/..."); ok {
			if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
				return true
			}
			continue
		}
		if importPath == pattern {
			return true
		}
	}
	return false
}

func violation(binding Binding, root, file string, line, column int, classification string) MatchedRelation {
	return MatchedRelation{
		Kind: string(binding.Predicate), SubjectID: binding.Subject.ID, Classification: classification,
		Locations: locations(location(root, file, line, column)),
	}
}

func sortViolations(relations []MatchedRelation) {
	sort.SliceStable(relations, func(i, j int) bool {
		left, right := relations[i].Locations, relations[j].Locations
		if len(left) == 0 || len(right) == 0 {
			return len(left) > len(right)
		}
		if left[0].Path != right[0].Path {
			return left[0].Path < right[0].Path
		}
		if left[0].Line != right[0].Line {
			return left[0].Line < right[0].Line
		}
		return left[0].Column < right[0].Column
	})
}

func policyOutcome(evidence Evidence, label string) Evaluation {
	if len(evidence.MatchedRelations) == 0 {
		return passed()
	}
	return failed(ReasonPredicateFailed, "policy_violated", fmt.Sprintf("%d %s outside the allowed scope; matched_relations lists each site", len(evidence.MatchedRelations), label))
}

// sourceFiles parses repository files on demand through the confined reader.
type sourceFiles struct {
	reader *sourcefs.Reader
	fset   *token.FileSet
	files  map[string]*ast.File
	errs   map[string]error
}

func openSourceFiles(root string) (*sourceFiles, error) {
	reader, err := sourcefs.Open(root)
	if err != nil {
		return nil, err
	}
	return &sourceFiles{reader: reader, fset: token.NewFileSet(), files: make(map[string]*ast.File), errs: make(map[string]error)}, nil
}

func (s *sourceFiles) close() { _ = s.reader.Close() }

func (s *sourceFiles) parse(name string, mode parser.Mode) (*ast.File, error) {
	if file, ok := s.files[name]; ok {
		return file, nil
	}
	if err, ok := s.errs[name]; ok {
		return nil, err
	}
	data, err := s.reader.ReadFile(name)
	if err == nil {
		var file *ast.File
		file, err = parser.ParseFile(s.fset, name, data, mode)
		if err == nil {
			s.files[name] = file
			return file, nil
		}
	}
	s.errs[name] = err
	return nil, err
}

func evaluateImportsOnlyFrom(binding Binding, snapshot Snapshot) (Evaluation, Evidence) {
	g := snapshot.Graph
	evidence := emptyEvidence()
	evidence.ResolvedSubject = &ResolvedReference{Kind: ReferencePackage, ID: binding.Subject.ID, Locations: []Location{}}
	scope := newPolicyScope(binding, g)
	files, err := openSourceFiles(snapshot.Root)
	if err != nil {
		return cannotEvaluate(ReasonInternalError, "source_unreadable", err.Error()), evidence
	}
	defer files.close()
	for _, edge := range g.Imports {
		file := path.Clean(filepathToSlash(edge.FromFile))
		if edge.ImportPath != binding.Subject.ID || !scope.checked(file) {
			continue
		}
		if scope.allowed(file, scope.packageByFile[file]) {
			continue
		}
		line := 0
		if parsed, err := files.parse(file, parser.ImportsOnly); err == nil {
			for _, spec := range parsed.Imports {
				if value, err := strconv.Unquote(spec.Path.Value); err == nil && value == binding.Subject.ID {
					line = files.fset.Position(spec.Pos()).Line
					break
				}
			}
		}
		evidence.MatchedRelations = append(evidence.MatchedRelations, violation(binding, snapshot.Root, file, line, 0, "import_outside_allow_list"))
	}
	sortViolations(evidence.MatchedRelations)
	return policyOutcome(evidence, "imports"), evidence
}

// callSite is one call of the policy subject.
type callSite struct {
	file         string
	line, column int
	callerID     string
	resolved     bool
}

// subjectCallSites returns every call of subjectID. Resolved precise edges
// match by callee ID. Parser edges without a resolved callee match when the
// selector qualifier is the file's import name for the subject's package
// (for example exec.Command with os/exec imported as exec).
func subjectCallSites(g *graph.Graph, subjectID string) []callSite {
	packagePath, declaration, _ := strings.Cut(subjectID, "::")
	importNames := make(map[string]map[string]bool) // file -> names bound to packagePath
	for _, edge := range g.Imports {
		if edge.ImportPath != packagePath {
			continue
		}
		file := path.Clean(filepathToSlash(edge.FromFile))
		name := edge.Alias
		if name == "" {
			name = path.Base(packagePath)
		}
		if importNames[file] == nil {
			importNames[file] = make(map[string]bool)
		}
		importNames[file][name] = true
	}
	seen := make(map[string]bool)
	var sites []callSite
	for _, edge := range g.Calls {
		if edge.Synthetic || edge.File == "" {
			continue
		}
		file := path.Clean(filepathToSlash(edge.File))
		resolved := edge.CalleeSymbolID == subjectID
		if !resolved {
			if edge.CalleeSymbolID != "" || strings.Contains(declaration, ".") {
				continue
			}
			qualifier, name, ok := strings.Cut(edge.CalleeRaw, ".")
			if !ok || name != declaration || !importNames[file][qualifier] {
				continue
			}
		}
		key := fmt.Sprintf("%s:%d:%d", file, edge.Line, edge.Column)
		if seen[key] {
			continue
		}
		seen[key] = true
		sites = append(sites, callSite{file: file, line: edge.Line, column: edge.Column, callerID: edge.CallerSymbolID, resolved: resolved})
	}
	return sites
}

func callerImportPath(scope policyScope, site callSite) string {
	if importPath, _, ok := strings.Cut(site.callerID, "::"); ok && importPath != "" {
		return importPath
	}
	return scope.packageByFile[site.file]
}

func evaluateCallersOnlyFrom(binding Binding, snapshot Snapshot) (Evaluation, Evidence) {
	evidence := emptyEvidence()
	evidence.ResolvedSubject = &ResolvedReference{Kind: ReferenceSymbol, ID: binding.Subject.ID, Locations: []Location{}}
	if matches := exactSymbols(snapshot.Graph, binding.Subject.ID); len(matches) == 1 {
		evidence.ResolvedSubject = resolvedSymbol(snapshot.Root, matches[0])
	}
	scope := newPolicyScope(binding, snapshot.Graph)
	for _, site := range subjectCallSites(snapshot.Graph, binding.Subject.ID) {
		if !scope.checked(site.file) || scope.allowed(site.file, callerImportPath(scope, site)) {
			continue
		}
		evidence.MatchedRelations = append(evidence.MatchedRelations, violation(binding, snapshot.Root, site.file, site.line, site.column, "call_outside_allow_list"))
	}
	sortViolations(evidence.MatchedRelations)
	return policyOutcome(evidence, "calls"), evidence
}

func evaluateCallArgConstant(binding Binding, snapshot Snapshot) (Evaluation, Evidence) {
	evidence := emptyEvidence()
	evidence.ResolvedSubject = &ResolvedReference{Kind: ReferenceSymbol, ID: binding.Subject.ID, Locations: []Location{}}
	if matches := exactSymbols(snapshot.Graph, binding.Subject.ID); len(matches) == 1 {
		evidence.ResolvedSubject = resolvedSymbol(snapshot.Root, matches[0])
	}
	files, err := openSourceFiles(snapshot.Root)
	if err != nil {
		return cannotEvaluate(ReasonInternalError, "source_unreadable", err.Error()), evidence
	}
	defer files.close()
	scope := newPolicyScope(binding, snapshot.Graph)
	constants := newPackageConstants(snapshot.Graph, files)
	forbidden := make(map[string]bool, len(binding.ForbiddenValues))
	for _, value := range binding.ForbiddenValues {
		forbidden[value] = true
	}
	argument := *binding.Argument
	for _, site := range subjectCallSites(snapshot.Graph, binding.Subject.ID) {
		if !scope.checked(site.file) || len(binding.Allow) > 0 && scope.allowed(site.file, callerImportPath(scope, site)) {
			continue
		}
		parsed, err := files.parse(site.file, parser.SkipObjectResolution)
		if err != nil {
			return cannotEvaluate(ReasonAnalysisIncomplete, "call_site_unparseable", fmt.Sprintf("%s: %v", site.file, err)), emptyEvidence()
		}
		call := callAtLparen(files.fset, parsed, site.line, site.column)
		if call == nil {
			return cannotEvaluate(ReasonAnalysisIncomplete, "call_site_not_found", fmt.Sprintf("no call expression at %s:%d:%d; rebuild the graph", site.file, site.line, site.column)), emptyEvidence()
		}
		if argument >= len(call.Args) {
			// An absent variadic argument has no value to check.
			continue
		}
		if call.Ellipsis.IsValid() && argument >= len(call.Args)-1 {
			evidence.MatchedRelations = append(evidence.MatchedRelations, violation(binding, snapshot.Root, site.file, site.line, site.column, "argument_spread_not_constant"))
			continue
		}
		pkg := scope.packageByFile[site.file]
		value, isConstant := constants.evaluate(call.Args[argument], pkg, parsed)
		if len(forbidden) > 0 {
			if isConstant && value.known && forbidden[value.text] {
				evidence.MatchedRelations = append(evidence.MatchedRelations, violation(binding, snapshot.Root, site.file, site.line, site.column, "forbidden_constant:"+value.text))
			}
			continue
		}
		if !isConstant {
			evidence.MatchedRelations = append(evidence.MatchedRelations, violation(binding, snapshot.Root, site.file, site.line, site.column, "argument_not_constant"))
		}
	}
	sortViolations(evidence.MatchedRelations)
	label := "call sites pass a non-constant argument"
	if len(forbidden) > 0 {
		label = "call sites pass a forbidden constant"
	}
	if len(evidence.MatchedRelations) == 0 {
		return passed(), evidence
	}
	return failed(ReasonPredicateFailed, "policy_violated", fmt.Sprintf("%d %s; matched_relations lists each site", len(evidence.MatchedRelations), label)), evidence
}

func callAtLparen(fset *token.FileSet, file *ast.File, line, column int) *ast.CallExpr {
	var found *ast.CallExpr
	ast.Inspect(file, func(node ast.Node) bool {
		if found != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		position := fset.Position(call.Lparen)
		if position.Line == line && position.Column == column {
			found = call
			return false
		}
		return true
	})
	return found
}

// constantValue is the folded value of a constant expression. known is false
// for constants whose value is not a plain string (numbers, booleans, iota).
type constantValue struct {
	text  string
	known bool
}

// packageConstants resolves identifiers to package-level const declarations
// of repository packages, parsed from source on first use.
type packageConstants struct {
	g      *graph.Graph
	files  *sourceFiles
	byPkg  map[string]map[string]ast.Expr
	byFile map[*ast.File]map[string]ast.Expr
}

func newPackageConstants(g *graph.Graph, files *sourceFiles) *packageConstants {
	return &packageConstants{g: g, files: files, byPkg: make(map[string]map[string]ast.Expr), byFile: make(map[*ast.File]map[string]ast.Expr)}
}

// constSpecs records every name of a const declaration, repeating the
// previous expression list for implicit iota-style specs.
func constSpecs(gen *ast.GenDecl, into map[string]ast.Expr) {
	var previous []ast.Expr
	for _, spec := range gen.Specs {
		valueSpec := spec.(*ast.ValueSpec)
		values := valueSpec.Values
		if len(values) == 0 {
			values = previous
		}
		previous = values
		for index, ident := range valueSpec.Names {
			var value ast.Expr = &ast.BasicLit{Kind: token.INT, Value: "0"}
			if index < len(values) {
				value = values[index]
			}
			into[ident.Name] = value
		}
	}
}

// localConstants holds const declarations anywhere in file, including
// function-local ones, as a fallback for identifiers.
func (c *packageConstants) localConstants(file *ast.File) map[string]ast.Expr {
	if decls, ok := c.byFile[file]; ok {
		return decls
	}
	decls := make(map[string]ast.Expr)
	ast.Inspect(file, func(node ast.Node) bool {
		if gen, ok := node.(*ast.GenDecl); ok && gen.Tok == token.CONST {
			constSpecs(gen, decls)
		}
		return true
	})
	c.byFile[file] = decls
	return decls
}

func (c *packageConstants) declarations(importPath string) map[string]ast.Expr {
	if decls, ok := c.byPkg[importPath]; ok {
		return decls
	}
	decls := make(map[string]ast.Expr)
	c.byPkg[importPath] = decls
	for _, pkg := range c.g.Packages {
		if pkg.ImportPathBestEffort != importPath {
			continue
		}
		for _, name := range pkg.Files {
			file, err := c.files.parse(path.Clean(filepathToSlash(name)), parser.SkipObjectResolution)
			if err != nil {
				continue
			}
			for _, decl := range file.Decls {
				if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.CONST {
					constSpecs(gen, decls)
				}
			}
		}
	}
	return decls
}

// evaluate reports whether expr is a compile-time constant built from
// literals, operators and package-level constants of repository packages.
// Constants of packages outside the repository are treated as non-constant,
// which can only produce a stricter result.
func (c *packageConstants) evaluate(expr ast.Expr, importPath string, file *ast.File) (constantValue, bool) {
	return c.eval(expr, importPath, file, 0)
}

func (c *packageConstants) eval(expr ast.Expr, importPath string, file *ast.File, depth int) (constantValue, bool) {
	if depth > 32 {
		return constantValue{}, false
	}
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			text, err := strconv.Unquote(value.Value)
			return constantValue{text: text, known: err == nil}, err == nil
		}
		return constantValue{}, true
	case *ast.ParenExpr:
		return c.eval(value.X, importPath, file, depth+1)
	case *ast.UnaryExpr:
		if value.Op == token.AND || value.Op == token.ARROW {
			return constantValue{}, false
		}
		_, ok := c.eval(value.X, importPath, file, depth+1)
		return constantValue{}, ok
	case *ast.BinaryExpr:
		left, leftOK := c.eval(value.X, importPath, file, depth+1)
		right, rightOK := c.eval(value.Y, importPath, file, depth+1)
		if !leftOK || !rightOK {
			return constantValue{}, false
		}
		if value.Op == token.ADD && left.known && right.known {
			return constantValue{text: left.text + right.text, known: true}, true
		}
		return constantValue{}, true
	case *ast.Ident:
		switch value.Name {
		case "true", "false", "iota":
			return constantValue{}, true
		}
		if declared, ok := c.declarations(importPath)[value.Name]; ok {
			return c.eval(declared, importPath, file, depth+1)
		}
		if declared, ok := c.localConstants(file)[value.Name]; ok {
			return c.eval(declared, importPath, file, depth+1)
		}
		return constantValue{}, false
	case *ast.SelectorExpr:
		qualifier, ok := value.X.(*ast.Ident)
		if !ok {
			return constantValue{}, false
		}
		for _, spec := range file.Imports {
			target, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			name := path.Base(target)
			if spec.Name != nil {
				name = spec.Name.Name
			}
			if name != qualifier.Name {
				continue
			}
			if declared, ok := c.declarations(target)[value.Sel.Name]; ok {
				return c.eval(declared, target, file, depth+1)
			}
		}
		return constantValue{}, false
	case *ast.CallExpr:
		// Conversions of constants such as string("x") remain constant.
		if len(value.Args) == 1 {
			if ident, ok := value.Fun.(*ast.Ident); ok && isBasicTypeName(ident.Name) {
				return c.eval(value.Args[0], importPath, file, depth+1)
			}
		}
		return constantValue{}, false
	}
	return constantValue{}, false
}

func isBasicTypeName(name string) bool {
	switch name {
	case "string", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"float32", "float64", "bool", "byte", "rune", "complex64", "complex128":
		return true
	}
	return false
}
