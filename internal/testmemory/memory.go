// Package testmemory remembers which Go tests passed on exactly the same
// inputs, across worktrees and clones, so a lane can skip a repeated run.
//
// A test's inputs are keyed conservatively: the bytes of every file in its
// package directory and in every in-module package that directory imports,
// transitively (non-Go files and testdata included), the module files, the Go
// version and build environment, the build tags, the go test flags the caller
// names, extra input paths the caller names, and the current values of every
// environment variable the closure reads by name. Any change to any of these
// changes the key, so a recorded pass is never reported for different inputs.
// Inputs outside that set (network services, files read outside the closure,
// environment read through computed names) are not observed; callers name
// such paths with extra inputs or do not record those tests.
package testmemory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/ozgurcd/gograph/internal/gitenv"
	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/graphstore"
)

const (
	// DirEnv overrides the memory directory; "off" disables test memory.
	DirEnv = "GOGRAPH_TEST_MEMORY_DIR"
	// maxStoreBytes caps the memory directory; least recently used records go.
	maxStoreBytes int64 = 256 << 20
	schemaVersion       = "gograph.test-memory.v1"
	recordSuffix        = ".json"
	lockWait            = 30 * time.Second
)

// buildEnvironment names the environment variables that change how go test
// builds or runs every package, so they are part of every key.
var buildEnvironment = []string{
	"CGO_ENABLED", "CGO_CFLAGS", "CGO_LDFLAGS", "CC", "CXX", "GOARCH", "GOARM", "GOAMD64",
	"GODEBUG", "GOEXPERIMENT", "GOFLAGS", "GOOS", "GOWORK", "GO111MODULE",
}

// Options select the parts of a key that the graph does not hold.
type Options struct {
	// GoFlags are the go test flags of the run (for example "-race -short");
	// a pass with other flags is a different input.
	GoFlags string
	// Inputs are extra repository-relative files or directories the tests read.
	Inputs []string
	// BuildTags, when set, are the tags of the run (gograph --tags); otherwise
	// the graph's recorded tags, which gograph's printed commands pass.
	BuildTags []string
	// TrustEnv accepts environment reads the key cannot name (os.Environ,
	// os.ExpandEnv, os.Getenv with a computed name). Without it such a
	// package has no memory.
	TrustEnv bool
}

// unnamedEnvAccessors read environment the key cannot name; os.Getenv and
// os.LookupEnv are unnamed only when the graph recorded no literal key there.
var unnamedEnvAccessors = map[string]bool{
	"os.Environ": true, "syscall.Environ": true, "os.ExpandEnv": true,
}

var namedEnvAccessors = map[string]bool{
	"os.Getenv": true, "os.LookupEnv": true, "syscall.Getenv": true,
}

// Memory computes keys over one graph and reads and writes the shared store.
type Memory struct {
	root       string
	g          *graph.Graph
	options    Options
	dir        string
	byImport   map[string]string   // import path -> package dir
	imports    map[string][]string // package dir -> imported in-module dirs
	packages   map[string]bool     // package dirs
	digests    map[string]string   // dir -> content digest (memoized)
	goVersion  string
	modulePath string
	base       string // digest of module files, environment, flags, inputs
}

// Open prepares test memory for g rooted at root, or ok=false when it is
// disabled. err reports a module layout whose inputs cannot be bounded
// (local replace directives, go.work); then no test is ever skipped.
func Open(root string, g *graph.Graph, options Options) (*Memory, bool, error) {
	dir := strings.TrimSpace(os.Getenv(DirEnv))
	if strings.EqualFold(dir, "off") {
		return nil, false, nil
	}
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return nil, false, nil
		}
		dir = filepath.Join(cache, "gograph", "tests")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, nil
	}
	m := &Memory{
		root: root, g: g, options: options, dir: dir,
		byImport: map[string]string{}, imports: map[string][]string{},
		packages: map[string]bool{}, digests: map[string]string{},
	}
	for _, module := range g.Modules {
		if module.Dir == "." || module.Dir == "" {
			m.modulePath = module.Path
		}
	}
	for _, pkg := range g.Packages {
		m.packages[pkg.Dir] = true
		if pkg.ImportPathBestEffort != "" {
			m.byImport[pkg.ImportPathBestEffort] = pkg.Dir
		}
	}
	seen := map[string]map[string]bool{}
	for _, edge := range g.Imports {
		target, ok := m.byImport[edge.ImportPath]
		if !ok {
			continue
		}
		from := path.Dir(filepath.ToSlash(edge.FromFile))
		if seen[from] == nil {
			seen[from] = map[string]bool{}
		}
		if !seen[from][target] {
			seen[from][target] = true
			m.imports[from] = append(m.imports[from], target)
		}
	}
	if err := m.prepareBase(); err != nil {
		return m, true, err
	}
	return m, true, nil
}

func (m *Memory) prepareBase() error {
	if _, err := os.Lstat(filepath.Join(m.root, "go.work")); err == nil {
		return errors.New("go.work selects modules outside this graph, so test inputs cannot be bounded")
	}
	modData, err := os.ReadFile(filepath.Join(m.root, "go.mod"))
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(modData), "\n") {
		if _, target, ok := strings.Cut(line, "=>"); ok {
			target = strings.TrimSpace(target)
			if strings.HasPrefix(target, ".") || strings.HasPrefix(target, "/") {
				return errors.New("go.mod replaces a module with a local directory, so test inputs cannot be bounded")
			}
		}
	}
	version, err := goVersion(m.root)
	if err != nil {
		return err
	}
	m.goVersion = version
	h := sha256.New()
	write := func(parts ...string) {
		for _, part := range parts {
			_, _ = io.WriteString(h, part)
			_, _ = h.Write([]byte{0})
		}
	}
	write(schemaVersion, version, strings.TrimSpace(m.options.GoFlags), fmt.Sprint(m.options.TrustEnv))
	if m.g.Build != nil && m.g.Build.Selection != nil {
		selection := m.g.Build.Selection
		tags := append([]string(nil), selection.BuildTags...)
		if len(m.options.BuildTags) > 0 {
			tags = append([]string(nil), m.options.BuildTags...)
		}
		sort.Strings(tags)
		write("tags", strings.Join(tags, ","), selection.GOOS, selection.GOARCH, fmt.Sprint(selection.CgoEnabled))
	} else {
		return errors.New("the graph records no build selection")
	}
	for _, name := range buildEnvironment {
		value, set := os.LookupEnv(name)
		write("env", name, fmt.Sprint(set), value)
	}
	for _, name := range []string{"go.mod", "go.sum", "vendor"} {
		digest, err := treeDigest(m.root, name, nil)
		if err != nil {
			return err
		}
		write("module", name, digest)
	}
	inputs := append([]string(nil), m.options.Inputs...)
	sort.Strings(inputs)
	for _, input := range inputs {
		clean := filepath.Clean(filepath.FromSlash(input))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("extra input %q must be inside the repository", input)
		}
		digest, err := treeDigest(m.root, clean, nil)
		if err != nil {
			return err
		}
		write("input", filepath.ToSlash(clean), digest)
	}
	m.base = hex.EncodeToString(h.Sum(nil))
	return nil
}

func goVersion(root string) (string, error) {
	cmd := exec.Command("go", "env", "GOVERSION")
	cmd.Dir = root
	cmd.Env = gitenv.Clean(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go env GOVERSION: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// closure returns dir and every in-module package directory it imports,
// transitively, sorted.
func (m *Memory) closure(dir string) []string {
	seen := map[string]bool{dir: true}
	queue := []string{dir}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, imported := range m.imports[next] {
			if !seen[imported] {
				seen[imported] = true
				queue = append(queue, imported)
			}
		}
	}
	dirs := make([]string, 0, len(seen))
	for d := range seen {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs
}

// Key returns the input key of every test of the package in dir (a graph
// package directory) with the given import path.
func (m *Memory) Key(importPath, dir string) (string, error) {
	if m.base == "" {
		return "", errors.New("test memory is unavailable for this module layout")
	}
	dirs := m.closure(dir)
	inClosure := map[string]bool{}
	for _, d := range dirs {
		inClosure[d] = true
	}
	// An in-module import the graph does not hold (an ignored, generated or
	// excluded directory) is code the key cannot see.
	if m.modulePath == "" {
		return "", errors.New("the graph records no main module, so in-module imports cannot be checked")
	}
	for _, edge := range m.g.Imports {
		if !inClosure[path.Dir(filepath.ToSlash(edge.FromFile))] {
			continue
		}
		if edge.ImportPath != m.modulePath && !strings.HasPrefix(edge.ImportPath, m.modulePath+"/") {
			continue
		}
		if _, ok := m.byImport[edge.ImportPath]; !ok {
			return "", fmt.Errorf("%s imports %s, which is not in the graph", edge.FromFile, edge.ImportPath)
		}
	}
	if !m.options.TrustEnv {
		named := map[string]bool{}
		for _, read := range m.g.EnvReads {
			named[fmt.Sprintf("%s:%d", read.File, read.Line)] = true
		}
		for _, call := range m.g.Calls {
			if !inClosure[path.Dir(filepath.ToSlash(call.File))] {
				continue
			}
			if unnamedEnvAccessors[call.CalleeRaw] || (namedEnvAccessors[call.CalleeRaw] && !named[fmt.Sprintf("%s:%d", call.File, call.Line)]) {
				return "", fmt.Errorf("%s:%d reads the environment through %s, which the key cannot name (--trust-env accepts it)", call.File, call.Line, call.CalleeRaw)
			}
		}
	}
	h := sha256.New()
	write := func(parts ...string) {
		for _, part := range parts {
			_, _ = io.WriteString(h, part)
			_, _ = h.Write([]byte{0})
		}
	}
	write(m.base, importPath)
	for _, d := range dirs {
		digest, ok := m.digests[d]
		if !ok {
			var err error
			digest, err = treeDigest(m.root, filepath.FromSlash(d), m.packages)
			if err != nil {
				return "", err
			}
			m.digests[d] = digest
		}
		write("dir", d, digest)
	}
	var envKeys []string
	seenEnv := map[string]bool{}
	for _, read := range m.g.EnvReads {
		if inClosure[path.Dir(filepath.ToSlash(read.File))] && !seenEnv[read.Key] {
			seenEnv[read.Key] = true
			envKeys = append(envKeys, read.Key)
		}
	}
	sort.Strings(envKeys)
	for _, name := range envKeys {
		value, set := os.LookupEnv(name)
		write("read", name, fmt.Sprint(set), value)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// treeDigest hashes the files under root/rel (a file or a directory). In a
// directory it skips .git and .gograph, nested modules, and subdirectories
// that are other graph packages (they count only when imported).
func treeDigest(root, rel string, packages map[string]bool) (string, error) {
	start := filepath.Join(root, rel)
	info, err := os.Lstat(start)
	if errors.Is(err, fs.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	h := sha256.New()
	add := func(name string, mode fs.FileMode, full string) error {
		_, _ = io.WriteString(h, name+"\x00"+mode.Type().String()+"\x00")
		switch {
		case mode.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(full)
			if err != nil {
				return err
			}
			_, _ = io.WriteString(h, target)
		case mode.IsRegular():
			file, err := os.Open(full)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(h, file)
			_ = file.Close()
			if copyErr != nil {
				return copyErr
			}
		}
		_, _ = h.Write([]byte{0})
		return nil
	}
	if !info.IsDir() {
		if err := add(filepath.ToSlash(rel), info.Mode(), start); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	err = filepath.WalkDir(start, func(full string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name, _ := filepath.Rel(root, full)
		name = filepath.ToSlash(name)
		if entry.IsDir() {
			if full == start {
				return nil
			}
			base := entry.Name()
			if base == ".git" || base == ".gograph" || packages[name] {
				return filepath.SkipDir
			}
			if _, err := os.Lstat(filepath.Join(full, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return add(name, info.Mode(), full)
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type record struct {
	Schema  string               `json:"schema"`
	Package string               `json:"package"`
	Passed  map[string]time.Time `json:"passed"`
}

func (m *Memory) recordPath(key string) string { return filepath.Join(m.dir, key+recordSuffix) }

func (m *Memory) load(key string) record {
	data, err := os.ReadFile(m.recordPath(key))
	var r record
	if err != nil || json.Unmarshal(data, &r) != nil || r.Schema != schemaVersion {
		return record{Schema: schemaVersion, Passed: map[string]time.Time{}}
	}
	if r.Passed == nil {
		r.Passed = map[string]time.Time{}
	}
	return r
}

// Passed reports whether test passed on inputs with this key.
func (m *Memory) Passed(key, test string) bool {
	_, ok := m.load(key).Passed[test]
	if ok {
		now := time.Now()
		_ = os.Chtimes(m.recordPath(key), now, now)
	}
	return ok
}

// Update records passed tests and forgets failed ones for key, atomically
// under a per-key lock, then keeps the store under its cap.
func (m *Memory) Update(key, importPath string, passed, failed []string) error {
	lock := flock.New(filepath.Join(m.dir, key+".lock"), flock.SetPermissions(0o600))
	ctx, cancel := context.WithTimeout(context.Background(), lockWait)
	defer cancel()
	locked, err := lock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil || !locked {
		return fmt.Errorf("lock test memory: %w", errors.Join(err, ctx.Err()))
	}
	defer func() { _ = lock.Unlock() }()
	r := m.load(key)
	r.Package = importPath
	now := time.Now().UTC()
	for _, test := range passed {
		r.Passed[test] = now
	}
	for _, test := range failed {
		delete(r.Passed, test)
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(m.dir, key+".tmp-*")
	if err != nil {
		return err
	}
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmp.Name())
		return errors.Join(writeErr, closeErr)
	}
	if err := os.Rename(tmp.Name(), m.recordPath(key)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	_, err = graphstore.Evict(m.dir, maxStoreBytes, map[string]bool{key + recordSuffix: true}, recordSuffix)
	return err
}

// GoVersion is the Go version the keys were computed for.
func (m *Memory) GoVersion() string { return m.goVersion }

// PackageDir maps an import path to its graph package directory.
func (m *Memory) PackageDir(importPath string) (string, bool) {
	dir, ok := m.byImport[importPath]
	return dir, ok
}
