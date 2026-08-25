// Package wiki generates a structured llm-wiki/ directory from the gograph
// static index. Every page is a self-contained, token-efficient markdown
// document designed to be injected directly into an LLM context window.
//
// No network calls are made. All data is derived from the in-memory graph.
package wiki

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

// WikiPage is a named, generated markdown document.
type WikiPage struct {
	// Filename is the relative path inside the output directory, e.g.
	// "overview.md" or "packages/internal-search.md".
	Filename string
	// Content holds the full markdown text of the page.
	Content string
}

// WikiGenerator builds all wiki pages from a loaded graph.
type WikiGenerator struct {
	g *graph.Graph
}

// New returns a WikiGenerator for the given graph.
func New(g *graph.Graph) *WikiGenerator {
	return &WikiGenerator{g: g}
}

// Generate builds all wiki pages and writes them to outputDir. Relative output
// paths are confined beneath the graph root; an absolute path explicitly
// selects a different local output root. The directory is created if it does
// not exist.
// Returns the list of pages that were written.
func (wg *WikiGenerator) Generate(outputDir string) ([]WikiPage, error) {
	pages := wg.buildAll()
	if !filepath.IsAbs(outputDir) {
		if wg.g == nil || wg.g.Root == "" {
			return nil, fmt.Errorf("wiki: relative output requires a graph root")
		}
		outputDir = filepath.Clean(outputDir)
		if !filepath.IsLocal(outputDir) {
			return nil, fmt.Errorf("wiki: relative output path must stay inside the graph root: %q", outputDir)
		}
		repository, err := sourcefs.Open(wg.g.Root)
		if err != nil {
			return nil, fmt.Errorf("wiki: open graph root: %w", err)
		}
		defer func() { _ = repository.Close() }()
		if err := repository.EnsureRealDirectory(outputDir, 0o755); err != nil {
			return nil, fmt.Errorf("wiki: create output directory: %w", err)
		}
		return writePages(repository, outputDir, pages)
	}

	// An absolute output path is an explicit user-selected local destination.
	// Its final entry must still be a real directory, and page descendants are
	// confined beneath the opened directory handle.
	info, err := os.Lstat(outputDir)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(outputDir, 0o755); err != nil {
			return nil, fmt.Errorf("wiki: create output directory: %w", err)
		}
		info, err = os.Lstat(outputDir)
	}
	if err != nil {
		return nil, fmt.Errorf("wiki: inspect output directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("wiki: output path must be a real directory: %s", outputDir)
	}
	output, err := sourcefs.Open(outputDir)
	if err != nil {
		return nil, fmt.Errorf("wiki: open output directory: %w", err)
	}
	defer func() { _ = output.Close() }()
	return writePages(output, ".", pages)
}

func writePages(output *sourcefs.Reader, prefix string, pages []WikiPage) ([]WikiPage, error) {
	for _, p := range pages {
		if p.Content == "" {
			// Skip empty pages (e.g. routes.md when no routes exist).
			continue
		}

		if !validPageFilename(p.Filename) {
			return nil, fmt.Errorf("wiki: unsafe generated page filename %q", p.Filename)
		}
		pagePath := filepath.Join(prefix, filepath.FromSlash(p.Filename))
		if err := output.EnsureRealDirectory(filepath.Dir(pagePath), 0o755); err != nil {
			return nil, fmt.Errorf("wiki: create dir for %s: %w", p.Filename, err)
		}
		if err := output.WriteRegularFile(pagePath, []byte(p.Content), 0o644, false); err != nil {
			return nil, fmt.Errorf("wiki: write %s: %w", p.Filename, err)
		}
	}
	if err := pruneStalePackagePages(output, prefix, pages); err != nil {
		return nil, err
	}

	return pages, nil
}

func pruneStalePackagePages(output *sourcefs.Reader, prefix string, pages []WikiPage) error {
	current := make(map[string]struct{})
	for _, page := range pages {
		if page.Content != "" && strings.HasPrefix(page.Filename, "packages/") {
			current[filepath.Base(page.Filename)] = struct{}{}
		}
	}
	packageDirectory := filepath.Join(prefix, "packages")
	entries, err := output.ReadDirectory(packageDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("wiki: inspect generated package pages: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "README.md" || filepath.Ext(name) != ".md" || !entry.Type().IsRegular() {
			continue
		}
		if _, exists := current[name]; exists {
			continue
		}
		pagePath := filepath.Join(packageDirectory, name)
		data, readErr := output.ReadRegularFileLimit(pagePath, 1<<20)
		if errors.Is(readErr, sourcefs.ErrFileTooLarge) {
			continue
		}
		if readErr != nil {
			return fmt.Errorf("wiki: inspect obsolete package page %s: %w", name, readErr)
		}
		content := string(data)
		if !strings.HasPrefix(content, "# Package: `") || !strings.Contains(content, "\n**Import path:** `") {
			continue
		}
		if err := output.RemoveRegularFile(pagePath); err != nil {
			return fmt.Errorf("wiki: remove obsolete package page %s: %w", name, err)
		}
	}
	return nil
}

func validPageFilename(name string) bool {
	if name == "" || strings.Contains(name, "\\") || filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return false
	}
	clean := path.Clean(name)
	return clean == name && clean != "." && filepath.IsLocal(filepath.FromSlash(clean))
}

// buildAll calls every page builder and collects results.
// Add new pages here as they are implemented.
func (wg *WikiGenerator) buildAll() []WikiPage {
	pages := []WikiPage{
		buildOverviewPage(wg.g),
		buildArchitecturePage(wg.g),
		buildHotspotsPage(wg.g),
		buildRoutesPage(wg.g),
		buildEnvPage(wg.g),
		buildErrorsPage(wg.g),
		buildConcurrencyPage(wg.g),
	}
	pages = append(pages, buildPackagePages(wg.g)...)
	pages = append(pages, buildAPIPage(wg.g))
	return pages
}
