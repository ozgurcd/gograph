package search

import (
	"path"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// receiverTypeIndex resolves the lexical receiver type the parser records on
// unresolved method calls (CallEdge.QualifierType) through struct fields, so
// AST matching can reject a same-name method of a different receiver type.
// Every unknown step keeps the call a candidate.
type receiverTypeIndex struct {
	types    map[string]graph.SymbolNode  // pkg::Type -> type declaration
	methods  map[string]bool              // pkg::Type.Method
	filePkg  map[string]string            // file -> package path
	packages map[string]bool              // package paths declared in the graph
	importOf map[string]map[string]string // file -> qualifier -> import path
}

func newReceiverTypeIndex(g *graph.Graph) *receiverTypeIndex {
	index := &receiverTypeIndex{
		types:    make(map[string]graph.SymbolNode),
		methods:  make(map[string]bool),
		filePkg:  make(map[string]string),
		packages: make(map[string]bool),
		importOf: make(map[string]map[string]string),
	}
	if g == nil {
		return index
	}
	for _, symbol := range g.Symbols {
		if !strings.Contains(symbol.ID, "::") {
			continue
		}
		pkg := symbolPackagePath(symbol.ID)
		index.filePkg[symbol.File] = pkg
		index.packages[pkg] = true
		switch symbol.Kind {
		case graph.KindStruct, graph.KindInterface, graph.KindType:
			index.types[pkg+"::"+symbol.Name] = symbol
		case graph.KindMethod:
			index.methods[pkg+"::"+receiverBaseName(symbol.Receiver)+"."+symbol.Name] = true
		}
	}
	for _, edge := range g.Imports {
		qualifier := edge.Alias
		if qualifier == "_" || qualifier == "." {
			continue
		}
		if qualifier == "" {
			qualifier = path.Base(edge.ImportPath)
		}
		byQualifier := index.importOf[edge.FromFile]
		if byQualifier == nil {
			byQualifier = make(map[string]string)
			index.importOf[edge.FromFile] = byQualifier
		}
		byQualifier[qualifier] = edge.ImportPath
	}
	return index
}

// receiverBaseName strips pointer, parentheses and type parameters from a
// method receiver such as "*List[T]".
func receiverBaseName(receiver string) string {
	receiver = strings.Trim(receiver, "(*) ")
	if position := strings.Index(receiver, "["); position >= 0 {
		receiver = receiver[:position]
	}
	return receiver
}

// typeKey resolves a type spelling ("T", "*T", "pkg.T", "T[int]") written in
// file to pkg::T. It returns "" for anything that is not a named type.
func (index *receiverTypeIndex) typeKey(spelling, file string) string {
	spelling = strings.TrimLeft(strings.TrimSpace(spelling), "*")
	if position := strings.Index(spelling, "["); position > 0 {
		spelling = spelling[:position]
	}
	qualifier, name, qualified := strings.Cut(spelling, ".")
	if !qualified {
		qualifier, name = "", spelling
	}
	if !isIdentifier(name) || qualified && !isIdentifier(qualifier) {
		return ""
	}
	if !qualified {
		pkg := index.filePkg[file]
		if pkg == "" {
			return ""
		}
		return pkg + "::" + name
	}
	pkg := index.importOf[file][qualifier]
	if pkg == "" {
		return ""
	}
	return pkg + "::" + name
}

func isIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 127 || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

// mayCall reports whether a method called directly on an expression of the
// spelled type qualifierType (T{}.M(), new(T).M(), x.(T).M()), written in
// file, may be target. The type must be declared in the graph: a method of
// an external type is never a repository method.
func (index *receiverTypeIndex) mayCall(qualifierType, file string, target graph.SymbolNode) bool {
	if index == nil || target.Kind != graph.KindMethod {
		return false
	}
	key := index.typeKey(qualifierType, file)
	if key == "" {
		return false
	}
	if _, ok := index.types[key]; !ok {
		return false
	}
	return !index.excludes("", qualifierType, file, target)
}

// excludes reports whether an unresolved call "qualifier.Name" whose
// receiver root has lexical type qualifierType, written in file, provably
// calls a method of another type than target.
func (index *receiverTypeIndex) excludes(qualifier, qualifierType, file string, target graph.SymbolNode) bool {
	if index == nil || qualifierType == "" || target.Kind != graph.KindMethod || !strings.Contains(target.ID, "::") {
		return false
	}
	parts := strings.Split(qualifier, ".")
	key := index.typeKey(qualifierType, file)
	for _, field := range parts[1:] {
		if key == "" {
			return false
		}
		declaration, ok := index.types[key]
		if !ok || declaration.Kind != graph.KindStruct {
			return false
		}
		next := ""
		for _, candidate := range declaration.StructFields {
			if candidate.Name == field {
				next = index.typeKey(candidate.Type, declaration.File)
				break
			}
		}
		if next == "" {
			// Unknown, promoted through an embedded struct, or not a named type.
			return false
		}
		key = next
	}
	if key == "" {
		return false
	}
	targetKey := symbolPackagePath(target.ID) + "::" + receiverBaseName(target.Receiver)
	if key == targetKey {
		return false
	}
	declaration, ok := index.types[key]
	if !ok {
		// A type of a package outside the graph (bytes.Buffer, for
		// example) has no repository methods.
		return !index.packages[symbolPackagePath(key)]
	}
	if declaration.Kind == graph.KindInterface {
		return false
	}
	if index.methods[key+"."+target.Name] {
		// The known type declares this method itself.
		return true
	}
	// Without its own method, a struct with embedded types or a non-struct
	// type (an alias, for example) may still reach the target.
	return declaration.Kind == graph.KindStruct && len(declaration.EmbeddedStructs) == 0
}
