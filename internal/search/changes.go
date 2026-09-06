package search

import (
	"context"
	"errors"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ozgurcd/gograph/internal/graph"
	sourceparser "github.com/ozgurcd/gograph/internal/parser"
	"github.com/ozgurcd/gograph/internal/repositoryfingerprint"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

// ChangeStatus classifies a declaration, not merely its enclosing file.
type ChangeStatus string

const (
	ChangeModified ChangeStatus = "modified"
	ChangeNew      ChangeStatus = "new"
	ChangeDeleted  ChangeStatus = "deleted"
	// ChangeUnknown requires a newer baseline with declaration fingerprints.
	ChangeUnknown ChangeStatus = "unknown"
	// ChangeExcluded is a file that exists but left the selected source inventory.
	ChangeExcluded ChangeStatus = "excluded"
)

type ChangedSymbol struct {
	Name        string       `json:"name"`
	StableID    string       `json:"stable_id,omitempty"`
	Receiver    string       `json:"receiver,omitempty"`
	PackageName string       `json:"package_name,omitempty"`
	File        string       `json:"file"`
	Line        int          `json:"line,omitempty"`
	Status      ChangeStatus `json:"status"`
}

type ChangesResult struct {
	SchemaVersion string          `json:"schema_version"`
	GraphAge      time.Time       `json:"graph_age"`
	Evaluation    string          `json:"evaluation"`
	Diagnostics   []string        `json:"diagnostics,omitempty"`
	ChangedFiles  []string        `json:"changed_files"`
	Symbols       []ChangedSymbol `json:"symbols"`
}

// Changes compares current selected source with the persisted declaration
// baseline. It does not rebuild or overwrite that baseline.
func Changes(g *graph.Graph, root string) *ChangesResult {
	return ChangesContext(context.Background(), g, root)
}

func ChangesContext(ctx context.Context, g *graph.Graph, root string) *ChangesResult {
	result := &ChangesResult{SchemaVersion: "gograph.changes.v1", GraphAge: g.GeneratedAt,
		Evaluation: "complete", ChangedFiles: []string{}, Symbols: []ChangedSymbol{}}
	reader, err := sourcefs.Open(root)
	if err != nil {
		result.Evaluation = "cannot_evaluate"
		result.Diagnostics = []string{err.Error()}
		return result
	}
	defer func() { _ = reader.Close() }()
	config, files, scanErrors := changesSelection(ctx, root, graphSelection(g))
	var observation repositoryfingerprint.Result
	if len(scanErrors) == 0 {
		var observeErr error
		observation, observeErr = repositoryfingerprint.Compute(ctx, root, config, files)
		if observeErr != nil {
			scanErrors = append(scanErrors, observeErr)
		}
	}
	for _, err := range scanErrors {
		result.incomplete(err.Error())
	}
	if g.Build != nil && g.Build.BuildContextFingerprint != "" && g.Build.Selection == nil {
		result.incomplete("baseline lacks recorded build selection; current environment used, rebuild a baseline before the next edit")
	}
	current, err := changesCurrentInventory(root, files, g)
	if err != nil {
		result.Evaluation = "cannot_evaluate"
		result.Diagnostics = append(result.Diagnostics, err.Error())
		return result
	}
	previousFiles := make(map[string]string)
	oldByFile := make(map[string][]graph.SymbolNode)
	for _, file := range g.Files {
		rel := graphFileRelative(root, file.Path)
		if !filepath.IsLocal(rel) {
			result.incomplete("unsafe baseline file path: " + file.Path)
			continue
		}
		previousFiles[rel] = file.ContentDigest
	}
	for _, symbol := range g.Symbols {
		if symbol.File != "" {
			rel := graphFileRelative(root, symbol.File)
			if !filepath.IsLocal(rel) {
				result.incomplete("unsafe baseline symbol path: " + symbol.File)
				continue
			}
			oldByFile[rel] = append(oldByFile[rel], symbol)
		}
	}
	existing := make(map[string]bool)
	changed := make(map[string]bool)
	evaluated := 0
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			result.incomplete(err.Error())
			break
		}
		rel := graphFileRelative(root, path)
		existing[rel] = true
		data, err := reader.ReadFile(rel)
		if err != nil {
			result.incomplete(fmt.Sprintf("%s: %v", rel, err))
			continue
		}
		digest := graph.SourceDigest(data)
		if expected := observation.Files[filepath.ToSlash(rel)]; expected != "" && digest != expected {
			result.incomplete("source changed during declaration comparison: " + rel)
			continue
		}
		oldDigest, hadFile := previousFiles[rel]
		importPath := changesImportPath(current, root, rel)
		ownershipChanged := importPath != changesImportPath(g, root, rel)
		if hadFile && digest == oldDigest && !ownershipChanged {
			evaluated++
			continue
		}
		// Legacy, handcrafted graphs may not have a file inventory. Preserve their
		// mtime selection, but never claim declaration precision without fingerprints.
		if !hadFile && len(g.Files) == 0 && len(oldByFile[rel]) > 0 {
			if info, err := os.Lstat(path); err == nil && !info.ModTime().After(g.GeneratedAt) {
				evaluated++
				continue
			}
		}
		changed[rel] = true
		parsed, err := sourceparser.ParseSource(token.NewFileSet(), rel, data, rel, importPath)
		if err != nil {
			result.incomplete(err.Error())
			continue
		}
		evaluated++
		compareDeclarations(result, oldByFile[rel], parsed.Symbols, rel)
	}
	// A failed inventory is not evidence that an omitted file or symbol vanished.
	if len(scanErrors) == 0 && ctx.Err() == nil {
		for rel, old := range oldByFile {
			if existing[rel] {
				continue
			}
			status := ChangeDeleted
			if err := reader.ValidateRegularFile(rel); err == nil {
				status = ChangeExcluded
			} else if !errors.Is(err, os.ErrNotExist) {
				result.incomplete(fmt.Sprintf("%s: %v", rel, err))
				continue
			}
			changed[rel] = true
			for _, symbol := range old {
				result.Symbols = append(result.Symbols, changedDeclaration(symbol, rel, status))
			}
		}
		for rel := range previousFiles {
			if !existing[rel] {
				changed[rel] = true
			}
		}
	}
	for rel := range changed {
		result.ChangedFiles = append(result.ChangedFiles, rel)
	}
	if result.Evaluation == "partial" && evaluated == 0 {
		result.Evaluation = "cannot_evaluate"
	}
	if observation.Fingerprint != "" {
		if err := verifyChangesObservation(ctx, root, graphSelection(g), observation.Fingerprint); err != nil {
			result.incomplete(err.Error())
			result.Evaluation = "cannot_evaluate"
			result.Symbols = []ChangedSymbol{}
			result.ChangedFiles = []string{}
		}
	}
	finalizeChanges(result)
	return result
}

func (r *ChangesResult) incomplete(message string) {
	r.Evaluation = "partial"
	for _, existing := range r.Diagnostics {
		if existing == message {
			return
		}
	}
	r.Diagnostics = append(r.Diagnostics, message)
}

func declarationKey(s graph.SymbolNode) string {
	if strings.Contains(s.ID, "::") {
		return s.PackageName + "\x00" + s.ID
	}
	return s.Receiver + "::" + s.Name
}

func changedDeclaration(s graph.SymbolNode, file string, status ChangeStatus) ChangedSymbol {
	return ChangedSymbol{Name: s.Name, StableID: s.ID, Receiver: s.Receiver, PackageName: s.PackageName, File: file, Line: s.Line, Status: status}
}

func compareDeclarations(result *ChangesResult, before, after []graph.SymbolNode, file string) {
	// Legal repeated declarations (init functions and blank identifiers) are
	// multisets, not a map from ID to one symbol. Match unchanged digests first
	// so removing the first initializer does not misattribute the second.
	old := make(map[string][]graph.SymbolNode)
	legacy := make(map[string]string)
	for _, symbol := range before {
		key := declarationKey(symbol)
		old[key] = append(old[key], symbol)
		if !strings.Contains(symbol.ID, "::") {
			legacy[symbol.Receiver+"::"+symbol.Name] = key
		}
	}
	keyFor := func(symbol graph.SymbolNode) string {
		key := declarationKey(symbol)
		if _, ok := old[key]; ok {
			return key
		}
		if _, ok := old["\x00"+symbol.ID]; ok && symbol.PackageName != "" {
			return "\x00" + symbol.ID // A legacy graph omitted package names.
		}
		if legacyKey, ok := legacy[symbol.Receiver+"::"+symbol.Name]; ok {
			return legacyKey
		}
		return key
	}
	matched := make([]bool, len(after))
	type matchPosition struct{ index, line int }
	positions := make(map[string][]matchPosition)
	for i, symbol := range after {
		if symbol.DeclarationDigest == "" {
			continue
		}
		key := keyFor(symbol)
		for j, prior := range old[key] {
			if prior.DeclarationDigest == symbol.DeclarationDigest {
				old[key] = append(old[key][:j], old[key][j+1:]...)
				matched[i] = true
				if symbol.Name == "init" || symbol.Name == "_" {
					positions[key] = append(positions[key], matchPosition{i, prior.Line})
				}
				break
			}
		}
	}
	for _, group := range positions {
		// Retain semantic initialization-order changes even when bodies match.
		minimumAfter := make([]int, len(group))
		minimum := int(^uint(0) >> 1)
		for i := len(group) - 1; i >= 0; i-- {
			minimumAfter[i] = minimum
			if group[i].line < minimum {
				minimum = group[i].line
			}
		}
		maximumBefore := 0
		for i, match := range group {
			if match.line < maximumBefore || match.line > minimumAfter[i] {
				result.Symbols = append(result.Symbols, changedDeclaration(after[match.index], file, ChangeModified))
			}
			if match.line > maximumBefore {
				maximumBefore = match.line
			}
		}
	}
	for i, symbol := range after {
		if matched[i] {
			continue
		}
		key := keyFor(symbol)
		prior := old[key]
		if len(prior) == 0 {
			result.Symbols = append(result.Symbols, changedDeclaration(symbol, file, ChangeNew))
			continue
		}
		old[key] = prior[1:]
		if prior[0].DeclarationDigest == "" || symbol.DeclarationDigest == "" {
			result.incomplete(file + ": baseline lacks declaration fingerprints; rebuild a baseline before the next edit")
			result.Symbols = append(result.Symbols, changedDeclaration(symbol, file, ChangeUnknown))
		} else {
			result.Symbols = append(result.Symbols, changedDeclaration(symbol, file, ChangeModified))
		}
	}
	for _, remaining := range old {
		for _, symbol := range remaining {
			result.Symbols = append(result.Symbols, changedDeclaration(symbol, file, ChangeDeleted))
		}
	}
}

func finalizeChanges(result *ChangesResult) {
	sort.Strings(result.ChangedFiles)
	sort.Strings(result.Diagnostics)
	sort.Slice(result.Symbols, func(i, j int) bool {
		a, b := result.Symbols[i], result.Symbols[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.StableID != b.StableID {
			return a.StableID < b.StableID
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.PackageName != b.PackageName {
			return a.PackageName < b.PackageName
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Status < b.Status
	})
}

func changesImportPath(g *graph.Graph, root, file string) string {
	dir := filepath.Dir(file)
	for _, symbol := range g.Symbols {
		if filepath.Dir(graphFileRelative(root, symbol.File)) == dir {
			if pkg, _, ok := strings.Cut(symbol.ID, "::"); ok {
				return pkg
			}
		}
	}
	for _, pkg := range g.Packages {
		if graphFileRelative(root, pkg.Dir) == dir && pkg.ImportPathBestEffort != "" {
			return pkg.ImportPathBestEffort
		}
	}
	owner := -1
	longest := -1
	for i, module := range g.Modules {
		moduleDir := graphFileRelative(root, module.Dir)
		rel, err := filepath.Rel(moduleDir, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if len(moduleDir) > longest {
			owner = i
			longest = len(moduleDir)
		}
	}
	if owner >= 0 {
		module := g.Modules[owner]
		rel, _ := filepath.Rel(graphFileRelative(root, module.Dir), dir)
		if rel == "." {
			return module.Path
		}
		return module.Path + "/" + filepath.ToSlash(rel)
	}
	return filepath.ToSlash(dir)
}

func graphFileRelative(root, path string) string {
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(root, path); err == nil {
			return filepath.Clean(rel)
		}
	}
	return filepath.Clean(path)
}
