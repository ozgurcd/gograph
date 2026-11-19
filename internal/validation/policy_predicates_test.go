package validation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

const policyModule = "example.com/policy"

var policySources = map[string]string{
	"runner/runner.go": `package runner

import "os/exec"

const gitBinary = "git"

func Run(args ...string) error { return exec.Command(gitBinary, args...).Run() }
func Spread(argv ...string) error { return exec.Command(argv[0], argv...).Run() }
`,
	"runner/runner_test.go": `package runner

import "os/exec"

func TestShell() { _ = exec.Command("sh", "-c", "true").Run() }
`,
	"tool/tool.go": `package tool

import osexec "os/exec"

func Bad(input string) error { return osexec.Command("sh", "-c", input).Run() }

func Dyn(name string) error { return osexec.Command(name).Run() }

func Local(input string) error {
	const shell = "ba" + "sh"
	return osexec.Command(shell, "-c", input).Run()
}
`,
}

// policySnapshot writes the sources and derives the graph records gograph
// would persist: packages, imports, and call edges at each call's Lparen,
// resolved to os/exec::Command.
func policySnapshot(t *testing.T) Snapshot {
	t.Helper()
	root := t.TempDir()
	snapshot := completeSnapshot()
	snapshot.Root = root
	g := snapshot.Graph
	g.Packages, g.Imports, g.Calls, g.Files = nil, nil, nil, nil
	packages := map[string]*graph.PackageNode{}
	fset := token.NewFileSet()
	for name, source := range policySources {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		importPath := policyModule + "/" + filepath.ToSlash(filepath.Dir(name))
		if packages[importPath] == nil {
			packages[importPath] = &graph.PackageNode{ImportPathBestEffort: importPath}
		}
		packages[importPath].Files = append(packages[importPath].Files, name)
		g.Files = append(g.Files, graph.FileNode{Path: name})
		for _, spec := range file.Imports {
			edge := graph.ImportEdge{FromFile: name, ImportPath: "os/exec"}
			if spec.Name != nil {
				edge.Alias = spec.Name.Name
			}
			g.Imports = append(g.Imports, edge)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Command" {
				position := fset.Position(call.Lparen)
				g.Calls = append(g.Calls, graph.CallEdge{
					CallerSymbolID: importPath + "::Caller", CalleeRaw: "Command", CalleeSymbolID: "os/exec::Command",
					File: name, Line: position.Line, Column: position.Column, Resolution: graph.CallResolutionStatic,
				})
			}
			return true
		})
	}
	for _, pkg := range packages {
		g.Packages = append(g.Packages, *pkg)
	}
	return snapshot
}

func allowPackage(id string) Reference {
	return Reference{Language: "go", Kind: ReferencePackage, ID: id}
}
func allowFile(id string) Reference { return Reference{Language: "go", Kind: ReferenceFile, ID: id} }

func violationSites(result Result) []string {
	sites := []string{}
	for _, relation := range result.Evidence.MatchedRelations {
		site := relation.Classification + "@"
		if len(relation.Locations) > 0 {
			site += relation.Locations[0].Path
		}
		sites = append(sites, site)
	}
	return sites
}

func TestPolicyPredicates(t *testing.T) {
	argument := func(value int) *int { return &value }
	tests := []struct {
		name    string
		binding Binding
		mutate  func(*Snapshot)
		outcome Outcome
		reason  Reason
		sites   []string
	}{
		{
			name:    "imports only from allowed package fails with site",
			binding: Binding{Predicate: PredicateImportsOnlyFrom, Subject: packageRef("os/exec"), Allow: []Reference{allowPackage(policyModule + "/runner")}, RequiredPrecision: PrecisionAST},
			outcome: OutcomeFail, reason: ReasonPredicateFailed, sites: []string{"import_outside_allow_list@tool/tool.go"},
		},
		{
			name:    "imports only from package pattern passes",
			binding: Binding{Predicate: PredicateImportsOnlyFrom, Subject: packageRef("os/exec"), Allow: []Reference{allowPackage(policyModule + "/...")}, RequiredPrecision: PrecisionAST},
			outcome: OutcomePass, reason: ReasonPredicatePassed, sites: []string{},
		},
		{
			name:    "imports only from works on ast graphs",
			binding: Binding{Predicate: PredicateImportsOnlyFrom, Subject: packageRef("os/exec"), Allow: []Reference{allowPackage(policyModule + "/runner"), allowFile("tool/tool.go")}, RequiredPrecision: PrecisionAST},
			mutate:  makeAST, outcome: OutcomePass, reason: ReasonPredicatePassed, sites: []string{},
		},
		{
			name:    "callers only from fails with each call site",
			binding: Binding{Predicate: PredicateCallersOnlyFrom, Subject: symbolRef("os/exec::Command"), Allow: []Reference{allowPackage(policyModule + "/runner")}, RequiredPrecision: PrecisionPrecise},
			outcome: OutcomeFail, reason: ReasonPredicateFailed,
			sites: []string{"call_outside_allow_list@tool/tool.go", "call_outside_allow_list@tool/tool.go", "call_outside_allow_list@tool/tool.go"},
		},
		{
			name:    "callers only from includes tests on request",
			binding: Binding{Predicate: PredicateCallersOnlyFrom, Subject: symbolRef("os/exec::Command"), Allow: []Reference{allowPackage(policyModule + "/tool")}, IncludeTests: true, RequiredPrecision: PrecisionPrecise},
			outcome: OutcomeFail, reason: ReasonPredicateFailed,
			sites: []string{"call_outside_allow_list@runner/runner.go", "call_outside_allow_list@runner/runner.go", "call_outside_allow_list@runner/runner_test.go"},
		},
		{
			name:    "callers only from needs precise graph",
			binding: Binding{Predicate: PredicateCallersOnlyFrom, Subject: symbolRef("os/exec::Command"), Allow: []Reference{allowPackage(policyModule + "/runner")}, RequiredPrecision: PrecisionAST},
			mutate:  makeAST, outcome: OutcomeCannotEvaluate, reason: ReasonPrecisionInsufficient,
		},
		{
			name:    "argument constant fails for dynamic and spread arguments",
			binding: Binding{Predicate: PredicateCallArgConstant, Subject: symbolRef("os/exec::Command"), Argument: argument(0), RequiredPrecision: PrecisionPrecise},
			outcome: OutcomeFail, reason: ReasonPredicateFailed,
			sites: []string{"argument_not_constant@runner/runner.go", "argument_not_constant@tool/tool.go"},
		},
		{
			name:    "argument never a forbidden literal, local constants folded",
			binding: Binding{Predicate: PredicateCallArgConstant, Subject: symbolRef("os/exec::Command"), Argument: argument(0), ForbiddenValues: []string{"sh", "bash"}, RequiredPrecision: PrecisionPrecise},
			outcome: OutcomeFail, reason: ReasonPredicateFailed,
			sites: []string{"forbidden_constant:sh@tool/tool.go", "forbidden_constant:bash@tool/tool.go"},
		},
		{
			name:    "argument constant allow list exempts files",
			binding: Binding{Predicate: PredicateCallArgConstant, Subject: symbolRef("os/exec::Command"), Argument: argument(0), Allow: []Reference{allowFile("runner/runner.go"), allowFile("tool/tool.go")}, RequiredPrecision: PrecisionPrecise},
			outcome: OutcomePass, reason: ReasonPredicatePassed, sites: []string{},
		},
		{
			name:    "argument beyond a call's arguments has no value to check",
			binding: Binding{Predicate: PredicateCallArgConstant, Subject: symbolRef("os/exec::Command"), Argument: argument(5), Allow: []Reference{allowFile("runner/runner.go")}, RequiredPrecision: PrecisionPrecise},
			outcome: OutcomePass, reason: ReasonPredicatePassed, sites: []string{},
		},
		{
			name:    "argument constant on stale call positions cannot evaluate",
			binding: Binding{Predicate: PredicateCallArgConstant, Subject: symbolRef("os/exec::Command"), Argument: argument(0), RequiredPrecision: PrecisionPrecise},
			mutate: func(snapshot *Snapshot) {
				// policySources is a map, so call order varies; shift a call
				// in a production file, which the predicate always evaluates.
				for index := range snapshot.Graph.Calls {
					if snapshot.Graph.Calls[index].File == "tool/tool.go" {
						snapshot.Graph.Calls[index].Column += 1000
						return
					}
				}
				panic("policy fixture has no call in tool/tool.go")
			},
			outcome: OutcomeCannotEvaluate, reason: ReasonAnalysisIncomplete,
		},
		{
			name:    "policies on a stale graph cannot evaluate",
			binding: Binding{Predicate: PredicateImportsOnlyFrom, Subject: packageRef("os/exec"), Allow: []Reference{allowPackage(policyModule + "/runner")}, RequiredPrecision: PrecisionAST},
			mutate:  func(snapshot *Snapshot) { snapshot.Freshness = "stale" },
			outcome: OutcomeCannotEvaluate, reason: ReasonGraphStale,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := policySnapshot(t)
			if test.mutate != nil {
				test.mutate(&snapshot)
			}
			test.binding.SchemaVersion = BindingSchemaVersion
			result := evaluate(t, snapshot, test.binding, "")
			if result.Evaluation.Outcome != test.outcome || result.Evaluation.Reason != test.reason {
				t.Fatalf("evaluation = %s/%s, want %s/%s; diagnostics=%v", result.Evaluation.Outcome, result.Evaluation.Reason, test.outcome, test.reason, result.Evaluation.Diagnostics)
			}
			if test.sites != nil && !reflect.DeepEqual(violationSites(result), test.sites) {
				t.Fatalf("sites = %v, want %v", violationSites(result), test.sites)
			}
		})
	}
}

func TestPolicyBindingValidation(t *testing.T) {
	argument := 0
	for name, value := range map[string]Binding{
		"imports without allow":       {Predicate: PredicateImportsOnlyFrom, Subject: packageRef("os/exec"), RequiredPrecision: PrecisionAST},
		"callers with object":         {Predicate: PredicateCallersOnlyFrom, Subject: symbolRef("os/exec::Command"), Object: ref(symbolRef("os/exec::Command")), Allow: []Reference{allowPackage("a/b")}, RequiredPrecision: PrecisionPrecise},
		"constant without argument":   {Predicate: PredicateCallArgConstant, Subject: symbolRef("os/exec::Command"), RequiredPrecision: PrecisionPrecise},
		"allow on symbol_exists":      {Predicate: PredicateSymbolExists, Subject: symbolRef(callerID), Allow: []Reference{allowPackage("a/b")}, RequiredPrecision: PrecisionAST},
		"argument on callers":         {Predicate: PredicateCallersOnlyFrom, Subject: symbolRef("os/exec::Command"), Allow: []Reference{allowPackage("a/b")}, Argument: &argument, RequiredPrecision: PrecisionPrecise},
		"escaping allow file":         {Predicate: PredicateCallersOnlyFrom, Subject: symbolRef("os/exec::Command"), Allow: []Reference{allowFile("../x.go")}, RequiredPrecision: PrecisionPrecise},
		"symbol in allow list":        {Predicate: PredicateCallersOnlyFrom, Subject: symbolRef("os/exec::Command"), Allow: []Reference{symbolRef(callerID)}, RequiredPrecision: PrecisionPrecise},
		"non-go allow file":           {Predicate: PredicateImportsOnlyFrom, Subject: packageRef("os/exec"), Allow: []Reference{allowFile("README.md")}, RequiredPrecision: PrecisionAST},
		"include_tests on implements": {Predicate: PredicateTypeImplements, Subject: symbolRef(concreteID), Object: ref(symbolRef(interfaceID)), IncludeTests: true, RequiredPrecision: PrecisionPrecise},
	} {
		value.SchemaVersion = BindingSchemaVersion
		result := evaluate(t, completeSnapshot(), value, "")
		if result.Evaluation.Outcome != OutcomeCannotEvaluate || result.Evaluation.Reason != ReasonInvalidRequest {
			t.Errorf("%s: evaluation = %s/%s, want cannot_evaluate/invalid_request", name, result.Evaluation.Outcome, result.Evaluation.Reason)
		}
	}
}
