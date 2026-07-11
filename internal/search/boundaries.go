package search

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// BoundaryLayer defines an architectural layer and its allowed dependencies.
type BoundaryLayer struct {
	Name      string   `json:"name"`
	Packages  []string `json:"packages"`
	MayImport []string `json:"may_import"`
}

// BoundariesConfig represents the .gograph/boundaries.json structure.
type BoundariesConfig struct {
	Layers []BoundaryLayer `json:"layers"`
}

// matchPackage checks if a package path (or import path) matches a pattern.
// Supports: exact match, "prefix/**" (recursive), and "prefix/*" (one level).
func matchPackage(pkg, pattern string) bool {
	if pattern == pkg {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return pkg == prefix || strings.HasPrefix(pkg, prefix+"/")
	}
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		if !strings.HasPrefix(pkg, prefix+"/") {
			return false
		}
		remaining := strings.TrimPrefix(pkg, prefix+"/")
		return !strings.Contains(remaining, "/")
	}
	return false
}

// isStdLib uses a simple heuristic: if the first path segment lacks a dot,
// it's considered standard library (e.g. "fmt", "net/http").
func isStdLib(importPath string) bool {
	firstSegment := strings.Split(importPath, "/")[0]
	return !strings.Contains(firstSegment, ".")
}

func boundaryConfigPath(g *graph.Graph, configPath string) (string, error) {
	if configPath == "" {
		return "", fmt.Errorf("invalid config path: empty")
	}
	if strings.Contains(configPath, "\\") {
		return "", fmt.Errorf("invalid config path: backslash not allowed")
	}
	if strings.Contains(configPath, "..") {
		return "", fmt.Errorf("invalid config path: path traversal detected")
	}

	if g == nil || g.Root == "" {
		resolved, err := filepath.Abs(configPath)
		if err != nil {
			return "", fmt.Errorf("resolve config path: %w", err)
		}
		return resolved, nil
	}
	root := g.Root
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve graph root: %w", err)
	}
	resolved := configPath
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(absRoot, resolved)
	}
	absConfig, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve config path: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absConfig)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid config path: must be inside graph root")
	}
	return absConfig, nil
}

// Boundaries checks the package import graph against constraints defined in a JSON file.
func Boundaries(g *graph.Graph, configPath string) ([]Result, error) {
	resolvedPath, err := boundaryConfigPath(g, configPath)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("could not read boundaries config file: %w", err)
	}

	var config BoundariesConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("invalid JSON in boundaries config: %w", err)
	}

	// 1. Build a map of ImportPath -> local directory
	// This lets us match internal imports against patterns like "internal/domain/**".
	importPathToDir := make(map[string]string)
	for _, pkg := range g.Packages {
		if pkg.ImportPathBestEffort != "" {
			importPathToDir[pkg.ImportPathBestEffort] = pkg.Dir
		}
	}

	var results []Result

	// 2. Iterate through all imports in the graph
	for _, imp := range g.Imports {
		fromDir := filepath.Dir(imp.FromFile)

		// Find which layer this file belongs to.
		// If a file matches multiple layers, we check against the first matching layer.
		var sourceLayer *BoundaryLayer
		for i, layer := range config.Layers {
			for _, pattern := range layer.Packages {
				if matchPackage(fromDir, pattern) {
					sourceLayer = &config.Layers[i]
					break
				}
			}
			if sourceLayer != nil {
				break
			}
		}

		if sourceLayer == nil {
			// File doesn't belong to any layer; ignore it.
			continue
		}

		// Implicitly allow standard library
		if isStdLib(imp.ImportPath) {
			continue
		}

		// Resolve import path to local dir if it's an internal package
		targetPath := imp.ImportPath
		if dir, ok := importPathToDir[imp.ImportPath]; ok {
			targetPath = dir
		}

		// Check if targetPath matches any of the MayImport patterns
		allowed := false
		for _, pattern := range sourceLayer.MayImport {
			if matchPackage(targetPath, pattern) {
				allowed = true
				break
			}
		}

		// If a layer imports another file within the EXACT SAME directory, we should allow it implicitly.
		if fromDir == targetPath {
			allowed = true
		}

		// Also implicitly allow imports within the exact same layer
		// (e.g. internal/domain/models importing internal/domain/errors)
		// if they match the SAME layer's package patterns.
		if !allowed {
			for _, pattern := range sourceLayer.Packages {
				if matchPackage(targetPath, pattern) {
					allowed = true
					break
				}
			}
		}

		if !allowed {
			results = append(results, Result{
				Kind:   "boundary_violation",
				Name:   sourceLayer.Name,
				File:   imp.FromFile,
				Line:   0,
				Detail: fmt.Sprintf("layer '%s' illegally imports '%s' (restricted by architecture)", sourceLayer.Name, imp.ImportPath),
				Score:  100,
			})
		}
	}

	return results, nil
}

// CreateBoundaries scans the graph and generates a baseline boundaries.json file based on current imports.
func CreateBoundaries(g *graph.Graph, configPath string) error {
	resolvedPath, err := boundaryConfigPath(g, configPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(resolvedPath); err == nil {
		return fmt.Errorf("file already exists, refusing to overwrite. Delete it first if you want to regenerate")
	}

	importPathToDir := make(map[string]string)
	for _, pkg := range g.Packages {
		if pkg.ImportPathBestEffort != "" {
			importPathToDir[pkg.ImportPathBestEffort] = pkg.Dir
		}
	}

	// Identify layers based on the first two segments of the directory (e.g. internal/domain)
	getLayer := func(dir string) string {
		if dir == "." {
			return "."
		}
		parts := strings.Split(filepath.ToSlash(dir), "/")
		if len(parts) >= 2 && (parts[0] == "internal" || parts[0] == "pkg" || parts[0] == "cmd") {
			return parts[0] + "/" + parts[1]
		}
		return parts[0]
	}

	layersMap := make(map[string]*BoundaryLayer)
	for _, pkg := range g.Packages {
		lname := getLayer(pkg.Dir)
		if layersMap[lname] == nil {
			layersMap[lname] = &BoundaryLayer{
				Name:      strings.ReplaceAll(lname, "/", "_"),
				Packages:  []string{lname + "/**"},
				MayImport: []string{},
			}
		}
	}

	// Populate may_import sets
	mayImportSets := make(map[string]map[string]bool)
	for name := range layersMap {
		mayImportSets[name] = make(map[string]bool)
	}

	for _, imp := range g.Imports {
		if isStdLib(imp.ImportPath) {
			continue
		}
		sourceDir := filepath.Dir(imp.FromFile)
		sourceLayer := getLayer(sourceDir)
		if mayImportSets[sourceLayer] == nil {
			continue
		}

		targetDir := imp.ImportPath
		if d, ok := importPathToDir[imp.ImportPath]; ok {
			targetDir = d
		}

		isInternal := false
		targetLayer := ""
		for _, pkg := range g.Packages {
			if pkg.Dir == targetDir {
				targetLayer = getLayer(targetDir)
				isInternal = true
				break
			}
		}

		if isInternal {
			if sourceLayer != targetLayer && targetLayer != "" {
				mayImportSets[sourceLayer][targetLayer+"/**"] = true
			}
		} else {
			// External third-party import. Group by module host/author (e.g. github.com/gin-gonic/**)
			parts := strings.Split(targetDir, "/")
			if len(parts) >= 2 {
				mayImportSets[sourceLayer][parts[0]+"/"+parts[1]+"/**"] = true
			} else {
				mayImportSets[sourceLayer][targetDir+"/**"] = true
			}
		}
	}

	var config BoundariesConfig
	for lname, layer := range layersMap {
		for target := range mayImportSets[lname] {
			layer.MayImport = append(layer.MayImport, target)
		}
		sort.Strings(layer.MayImport)
		config.Layers = append(config.Layers, *layer)
	}

	sort.Slice(config.Layers, func(i, j int) bool {
		return config.Layers[i].Name < config.Layers[j].Name
	})

	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(resolvedPath, data, 0644)
}
