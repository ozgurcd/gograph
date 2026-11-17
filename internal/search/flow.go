package search

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

var validFlowSources = map[string]bool{
	"http_request": true,
	"decoded_json": true,
	"environment":  true,
}

var validFlowSinks = map[string]bool{
	"sql_query":         true,
	"process_execution": true,
	"filesystem":        true,
	"outbound_http":     true,
}

// DefaultFlowContextDepth is the number of most recent call-site frames that
// keep a value's call/return context. Deeper chains are still followed: the
// oldest frames are dropped, and a return that can no longer be matched to its
// call site is reported with low confidence instead of being lost. Each extra
// frame can multiply the number of contexts, so deeper settings need a larger
// work budget on large repositories.
const (
	DefaultFlowContextDepth = 3
	MaxFlowContextDepth     = 16
)

// Flow work is counted in trace propagation attempts. The default budget grows
// with the number of persisted flow facts so that large repositories are not
// cut off by a fixed constant; MaxWork overrides it.
const (
	flowWorkPerFact = 400
	flowMinWork     = 2_000_000
	MaxFlowWork     = 2_000_000_000
)

// FlowOptions controls a security-flow query.
type FlowOptions struct {
	Term         string
	Source       string
	Sink         string
	ConfigPath   string
	IncludeTests bool
	// MaxWork bounds propagation work. Zero selects DefaultFlowWork.
	MaxWork int
	// ContextDepth is the number of call-site frames kept per value. Zero
	// selects DefaultFlowContextDepth.
	ContextDepth int
	// Packages limits analysis to functions in these import paths; an entry
	// ending in "/..." also matches descendants. Empty analyzes everything.
	Packages []string
}

// FlowSanitizer declares a function whose return value is trusted for the
// listed sink kinds. An empty For list applies to every sink kind. With
// Argument set, the function validates that argument instead: within the
// calling function, the variable passed there is trusted for those kinds.
type FlowSanitizer struct {
	Function string   `json:"function"`
	For      []string `json:"for,omitempty"`
	Argument *int     `json:"argument,omitempty"`
}

// FlowCustomSource declares a repository-specific untrusted input: the
// return values of Function (all of them, or only Return when set).
type FlowCustomSource struct {
	Function string `json:"function"`
	Kind     string `json:"kind"`
	Return   *int   `json:"return,omitempty"`
}

// FlowCustomSink declares a repository-specific sensitive operation: the
// arguments of Function (all of them, or only Argument when set).
type FlowCustomSink struct {
	Function string `json:"function"`
	Kind     string `json:"kind"`
	Argument *int   `json:"argument,omitempty"`
	// Severity is high (default), medium or low.
	Severity string `json:"severity,omitempty"`
}

// FlowConfig is the schema for .gograph/flow.json.
type FlowConfig struct {
	Sanitizers []FlowSanitizer    `json:"sanitizers"`
	Sources    []FlowCustomSource `json:"sources,omitempty"`
	Sinks      []FlowCustomSink   `json:"sinks,omitempty"`
}

func (config FlowConfig) sourceKinds() map[string]bool {
	kinds := make(map[string]bool, len(validFlowSources)+len(config.Sources))
	for kind := range validFlowSources {
		kinds[kind] = true
	}
	for _, source := range config.Sources {
		kinds[source.Kind] = true
	}
	return kinds
}

func (config FlowConfig) sinkKinds() map[string]bool {
	kinds := make(map[string]bool, len(validFlowSinks)+len(config.Sinks))
	for kind := range validFlowSinks {
		kinds[kind] = true
	}
	for _, sink := range config.Sinks {
		kinds[sink.Kind] = true
	}
	return kinds
}

func sortedKinds(kinds map[string]bool) string {
	names := make([]string, 0, len(kinds))
	for kind := range kinds {
		names = append(names, kind)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func validFlowKindName(kind string) bool {
	if kind == "" || len(kind) > 64 {
		return false
	}
	for _, r := range kind {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// FlowEndpoint describes one end of a reported source-to-sink path.
type FlowEndpoint struct {
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// FlowStep is one relevant operation along a reported path.
type FlowStep struct {
	Kind     string `json:"kind"`
	Function string `json:"function"`
	Detail   string `json:"detail"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// FlowResult is one potential untrusted-data path to a security-sensitive sink.
type FlowResult struct {
	Severity   string `json:"severity"`
	Confidence string `json:"confidence"`
	// ParameterOnly marks a process sink whose program (argv[0]) is a
	// constant non-shell literal: only its arguments carry the value, so
	// the risk is argument injection, not running another program.
	ParameterOnly bool         `json:"parameter_only,omitempty"`
	Source        FlowEndpoint `json:"source"`
	Sink          FlowEndpoint `json:"sink"`
	Path          []FlowStep   `json:"path"`
}

func (r FlowResult) String() string {
	scope := ""
	if r.ParameterOnly {
		scope = ", parameters only"
	}
	return fmt.Sprintf("[%s] %s -> %s (%s confidence%s)  (%s:%d)",
		strings.ToUpper(r.Severity), r.Source.Kind, r.Sink.Kind, r.Confidence, scope, r.Sink.File, r.Sink.Line)
}

// FlowCompleteness reports whether propagation reached a fixed point.
type FlowCompleteness struct {
	Complete     bool   `json:"complete"`
	Work         int    `json:"work"`
	MaxWork      int    `json:"max_work"`
	ContextDepth int    `json:"context_depth"`
	Reason       string `json:"reason,omitempty"`
	// TopFunctions lists the functions that consumed most of an exhausted
	// budget, to choose --package scopes or sanitizers.
	TopFunctions []FlowWorkShare `json:"top_functions,omitempty"`
}

// FlowWorkShare is one function's share of propagation work.
type FlowWorkShare struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Work     int    `json:"work"`
}

// FlowReport is a flow answer together with its completeness. Results of an
// incomplete report are a subset of the complete answer.
type FlowReport struct {
	Results      []FlowResult     `json:"findings"`
	Completeness FlowCompleteness `json:"completeness"`
}

// DefaultFlowWork is the work budget used when FlowOptions.MaxWork is zero.
func DefaultFlowWork(g *graph.Graph) int {
	facts := 0
	if g != nil {
		for _, function := range g.FlowFunctions {
			facts += len(function.Facts)
		}
	}
	work := facts * flowWorkPerFact
	if work < flowMinWork {
		work = flowMinWork
	}
	if work > MaxFlowWork {
		work = MaxFlowWork
	}
	return work
}

// Flow finds potential paths from untrusted inputs to security-sensitive
// operations. It uses persisted AST facts and never executes target code.
// An exhausted work budget is an error; use FlowWithReport for partial results.
func Flow(g *graph.Graph, options FlowOptions) ([]FlowResult, error) {
	report, err := FlowWithReport(g, options)
	if err != nil {
		return nil, err
	}
	if !report.Completeness.Complete {
		return report.Results, errors.New(report.Completeness.Reason)
	}
	return report.Results, nil
}

// FlowWithReport runs flow analysis and reports whether it completed.
func FlowWithReport(g *graph.Graph, options FlowOptions) (FlowReport, error) {
	if g == nil {
		return FlowReport{}, fmt.Errorf("flow analysis requires a graph")
	}
	if options.MaxWork < 0 || options.MaxWork > MaxFlowWork {
		return FlowReport{}, fmt.Errorf("flow max work must be between 1 and %d", MaxFlowWork)
	}
	if options.ContextDepth < 0 || options.ContextDepth > MaxFlowContextDepth {
		return FlowReport{}, fmt.Errorf("flow context depth must be between 1 and %d", MaxFlowContextDepth)
	}

	config, err := loadFlowConfig(g, options.ConfigPath)
	if err != nil {
		return FlowReport{}, err
	}
	if sources := config.sourceKinds(); options.Source != "" && !sources[options.Source] {
		return FlowReport{}, fmt.Errorf("unsupported flow source %q (valid: %s)", options.Source, sortedKinds(sources))
	}
	if sinks := config.sinkKinds(); options.Sink != "" && !sinks[options.Sink] {
		return FlowReport{}, fmt.Errorf("unsupported flow sink %q (valid: %s)", options.Sink, sortedKinds(sinks))
	}
	if len(g.FlowFunctions) == 0 {
		return FlowReport{}, fmt.Errorf("graph contains no security-flow facts; run `gograph build .` to refresh it")
	}

	analyzer := newFlowAnalyzer(g, options, config)
	analyzer.propagate()
	completeness := FlowCompleteness{
		Complete: !analyzer.exhausted, Work: analyzer.work,
		MaxWork: analyzer.maxWork, ContextDepth: analyzer.contextDepth,
	}
	if analyzer.exhausted {
		completeness.TopFunctions = analyzer.topWork(10)
		completeness.Reason = fmt.Sprintf("flow analysis incomplete: work budget of %d propagation steps exhausted before a fixed point at context depth %d; findings are a partial subset. Rerun with a larger --max-work (MCP max_work), a smaller --context-depth, or narrow with --package/--source/--sink/--no-tests; top_functions lists where the work went", analyzer.maxWork, analyzer.contextDepth)
	}
	return FlowReport{Results: analyzer.findings(), Completeness: completeness}, nil
}

func loadFlowConfig(g *graph.Graph, requestedPath string) (FlowConfig, error) {
	optional := requestedPath == ""
	if requestedPath == "" {
		requestedPath = ".gograph/flow.json"
	}
	resolvedPath, err := boundaryConfigPath(g, requestedPath)
	if err != nil {
		return FlowConfig{}, err
	}
	var data []byte
	if g != nil && g.Root != "" {
		absRoot, rootErr := filepath.Abs(g.Root)
		if rootErr != nil {
			return FlowConfig{}, fmt.Errorf("resolve graph root: %w", rootErr)
		}
		rel, relErr := relativeWithinRoot(absRoot, resolvedPath)
		if relErr != nil {
			return FlowConfig{}, fmt.Errorf("invalid config path: must be inside graph root")
		}
		repository, openErr := sourcefs.Open(absRoot)
		if openErr != nil {
			return FlowConfig{}, fmt.Errorf("open graph root for flow config: %w", openErr)
		}
		data, err = repository.ReadRegularFile(rel)
		_ = repository.Close()
	} else {
		configRoot, name := filepath.Dir(resolvedPath), filepath.Base(resolvedPath)
		reader, openErr := sourcefs.Open(configRoot)
		if openErr != nil {
			return FlowConfig{}, fmt.Errorf("open flow config root: %w", openErr)
		}
		data, err = reader.ReadRegularFile(name)
		_ = reader.Close()
	}
	if err != nil {
		if optional && errors.Is(err, os.ErrNotExist) {
			return FlowConfig{}, nil
		}
		return FlowConfig{}, fmt.Errorf("could not read flow config file: %w", err)
	}
	var config FlowConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return FlowConfig{}, fmt.Errorf("invalid JSON in flow config: %w", err)
	}
	for index, source := range config.Sources {
		if strings.TrimSpace(source.Function) == "" || !validFlowKindName(source.Kind) {
			return FlowConfig{}, fmt.Errorf("invalid flow config: sources[%d] needs a function and a kind of lower-case letters, digits and underscores", index)
		}
		if source.Return != nil && *source.Return < 0 {
			return FlowConfig{}, fmt.Errorf("invalid flow config: sources[%d].return must not be negative", index)
		}
	}
	for index, sink := range config.Sinks {
		if strings.TrimSpace(sink.Function) == "" || !validFlowKindName(sink.Kind) {
			return FlowConfig{}, fmt.Errorf("invalid flow config: sinks[%d] needs a function and a kind of lower-case letters, digits and underscores", index)
		}
		if sink.Argument != nil && *sink.Argument < 0 {
			return FlowConfig{}, fmt.Errorf("invalid flow config: sinks[%d].argument must not be negative", index)
		}
		switch sink.Severity {
		case "", "high", "medium", "low":
		default:
			return FlowConfig{}, fmt.Errorf("invalid flow config: sinks[%d].severity must be high, medium or low", index)
		}
	}
	sinkKinds := config.sinkKinds()
	for index, sanitizer := range config.Sanitizers {
		if strings.TrimSpace(sanitizer.Function) == "" {
			return FlowConfig{}, fmt.Errorf("invalid flow config: sanitizers[%d].function is required", index)
		}
		if sanitizer.Argument != nil && *sanitizer.Argument < 0 {
			return FlowConfig{}, fmt.Errorf("invalid flow config: sanitizers[%d].argument must not be negative", index)
		}
		for _, sink := range sanitizer.For {
			if !sinkKinds[sink] {
				return FlowConfig{}, fmt.Errorf("invalid flow config: sanitizer %q has unsupported sink %q", sanitizer.Function, sink)
			}
		}
	}
	return config, nil
}

// flowPath is an immutable, shared list of steps, newest first. Traces that
// extend the same prefix share it instead of copying the whole path.
type flowPath struct {
	step   FlowStep
	prev   *flowPath
	length int
}

func (p *flowPath) len() int {
	if p == nil {
		return 0
	}
	return p.length
}

func (p *flowPath) steps() []FlowStep {
	result := make([]FlowStep, p.len())
	for node, index := p, p.len()-1; node != nil; node, index = node.prev, index-1 {
		result[index] = node.step
	}
	return result
}

func appendFlowPath(path *flowPath, step FlowStep) *flowPath {
	if path != nil {
		last := path.step
		if last.Kind == step.Kind && last.File == step.File && last.Line == step.Line && last.Detail == step.Detail {
			return path
		}
	}
	return &flowPath{step: step, prev: path, length: path.len() + 1}
}

// compareFlowPaths orders equal-length paths deterministically, oldest step
// first, so tie-breaking never depends on map iteration order.
func compareFlowPaths(left, right *flowPath) int {
	if left == right {
		return 0
	}
	if left.len() != right.len() {
		return left.len() - right.len()
	}
	if left.length > 1 {
		if c := compareFlowPaths(left.prev, right.prev); c != 0 {
			return c
		}
	}
	a, b := left.step, right.step
	for _, pair := range [][2]string{{a.File, b.File}, {a.Kind, b.Kind}, {a.Function, b.Function}, {a.Detail, b.Detail}} {
		if c := strings.Compare(pair[0], pair[1]); c != 0 {
			return c
		}
	}
	return a.Line - b.Line
}

// flowTraceKey identifies one tainted value context: its source, the sink
// kinds it was sanitized for, and its (interned) call-site stack.
type flowTraceKey struct {
	source    int
	sanitized int
	stack     int
}

type flowTrace struct {
	key flowTraceKey
	// path is the shortest known path for this key.
	path *flowPath
	// low marks paths crossing an unresolved call or an unmatched return.
	low bool
	// truncated marks a call stack whose oldest frames were dropped.
	truncated bool
}

// betterFlowTrace is a strict order: confident before low, complete stack
// before truncated, shorter before longer, then a deterministic path order.
func betterFlowTrace(candidate, existing flowTrace) bool {
	if candidate.low != existing.low {
		return !candidate.low
	}
	if candidate.truncated != existing.truncated {
		return !candidate.truncated
	}
	return compareFlowPaths(candidate.path, existing.path) < 0
}

type flowSource struct {
	endpoint FlowEndpoint
	key      string
}

// flowState holds the traces reaching each value of one function. Values are
// indexed by their root identifier because related refs share it.
type flowState struct {
	values  map[string]map[flowTraceKey]flowTrace
	byRoot  map[string][]string
	returns []string
}

func newFlowState() *flowState {
	return &flowState{values: make(map[string]map[flowTraceKey]flowTrace), byRoot: make(map[string][]string)}
}

func flowRefRoot(ref string) string {
	if dot := strings.IndexByte(ref, '.'); dot >= 0 {
		return ref[:dot]
	}
	return ref
}

func flowReturnIndex(value string) int {
	switch {
	case value == "$return":
		return 0
	case strings.HasPrefix(value, "$return:"):
		parsed, err := strconv.Atoi(strings.TrimPrefix(value, "$return:"))
		if err == nil && parsed >= 0 {
			return parsed
		}
	}
	return -1
}

// flowCall is a call fact's resolution, computed once per analysis.
type flowCall struct {
	targets   []int
	uncertain bool
	frame     int
	sanitized map[string]bool
}

type flowAnalyzer struct {
	options   FlowOptions
	config    FlowConfig
	functions []graph.FlowFunction
	states    []*flowState
	calls     [][]flowCall
	callers   [][]int

	indexByKey    map[string]int
	keysByID      map[string][]int
	importsByFile map[string]map[string]string
	callsBySite   map[string][]graph.CallEdge

	sources     []flowSource
	sourceIndex map[string]int
	sanitized   []map[string]bool
	sanIndex    map[string]int
	stacks      [][]int
	stackIndex  map[string]int
	frameIndex  map[string]int

	queue  []int
	queued []bool

	// customSources and customSinks index config entries per call fact.
	customSources map[[2]int][]FlowCustomSource
	customSinks   map[[2]int][]FlowCustomSink
	// argSanitized lists, per function, values validated by an argument
	// sanitizer call anywhere in that function.
	argSanitized [][]flowArgSanitized
	workBy       []int

	work         int
	maxWork      int
	contextDepth int
	exhausted    bool
}

// flowArgSanitized is a value (and its fields) trusted for kinds.
type flowArgSanitized struct {
	ref   string
	kinds map[string]bool
}

func flowPackageSelected(id string, packages []string) bool {
	if len(packages) == 0 {
		return true
	}
	importPath := stableIDPackagePath(id)
	for _, pattern := range packages {
		if prefix, ok := strings.CutSuffix(pattern, "/..."); ok {
			if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
				return true
			}
		} else if importPath == pattern {
			return true
		}
	}
	return false
}

func newFlowAnalyzer(g *graph.Graph, options FlowOptions, config FlowConfig) *flowAnalyzer {
	analyzer := &flowAnalyzer{
		options:       options,
		config:        config,
		indexByKey:    make(map[string]int),
		keysByID:      make(map[string][]int),
		importsByFile: make(map[string]map[string]string),
		callsBySite:   make(map[string][]graph.CallEdge),
		sourceIndex:   make(map[string]int),
		sanitized:     []map[string]bool{{}},
		sanIndex:      map[string]int{"": 0},
		stacks:        [][]int{nil},
		stackIndex:    map[string]int{"": 0},
		frameIndex:    make(map[string]int),
		maxWork:       options.MaxWork,
		contextDepth:  options.ContextDepth,
	}
	if analyzer.maxWork == 0 {
		analyzer.maxWork = DefaultFlowWork(g)
	}
	if analyzer.contextDepth == 0 {
		analyzer.contextDepth = DefaultFlowContextDepth
	}
	for _, function := range g.FlowFunctions {
		if !options.IncludeTests && isTestFile(function.File) {
			continue
		}
		if !flowPackageSelected(function.ID, options.Packages) {
			continue
		}
		analyzer.functions = append(analyzer.functions, function)
	}
	sort.SliceStable(analyzer.functions, func(i, j int) bool {
		return flowFunctionKey(analyzer.functions[i]) < flowFunctionKey(analyzer.functions[j])
	})
	unique := analyzer.functions[:0]
	for _, function := range analyzer.functions {
		key := flowFunctionKey(function)
		if _, exists := analyzer.indexByKey[key]; exists {
			continue
		}
		analyzer.indexByKey[key] = len(unique)
		analyzer.keysByID[function.ID] = append(analyzer.keysByID[function.ID], len(unique))
		unique = append(unique, function)
	}
	analyzer.functions = unique
	analyzer.states = make([]*flowState, len(analyzer.functions))
	for index := range analyzer.states {
		analyzer.states[index] = newFlowState()
	}
	for _, call := range g.Calls {
		if call.Potential || (!options.IncludeTests && isTestFile(call.File)) {
			continue
		}
		site := flowCallSite(call.CallerSymbolID, call.File, call.Line)
		analyzer.callsBySite[site] = append(analyzer.callsBySite[site], call)
	}
	for _, imported := range g.Imports {
		alias := imported.Alias
		if alias == "" {
			alias = imported.ImportPath
			if slash := strings.LastIndex(alias, "/"); slash >= 0 {
				alias = alias[slash+1:]
			}
		}
		if alias == "" || alias == "." || alias == "_" {
			continue
		}
		if analyzer.importsByFile[imported.FromFile] == nil {
			analyzer.importsByFile[imported.FromFile] = make(map[string]string)
		}
		analyzer.importsByFile[imported.FromFile][alias] = imported.ImportPath
	}

	analyzer.calls = make([][]flowCall, len(analyzer.functions))
	analyzer.customSources = make(map[[2]int][]FlowCustomSource)
	analyzer.customSinks = make(map[[2]int][]FlowCustomSink)
	analyzer.argSanitized = make([][]flowArgSanitized, len(analyzer.functions))
	analyzer.workBy = make([]int, len(analyzer.functions))
	callerSets := make([]map[int]bool, len(analyzer.functions))
	for index, function := range analyzer.functions {
		analyzer.calls[index] = make([]flowCall, len(function.Facts))
		for factIndex, fact := range function.Facts {
			if fact.Kind != "call" {
				continue
			}
			targets, uncertain := analyzer.resolveCall(function, fact)
			candidates := analyzer.callCandidates(function, fact, targets)
			analyzer.calls[index][factIndex] = flowCall{
				targets: targets, uncertain: uncertain,
				frame:     analyzer.internFrame(flowCallFrame(function, fact)),
				sanitized: analyzer.sanitizerKinds(candidates),
			}
			analyzer.indexCustomRules(index, factIndex, fact, candidates)
			for _, target := range targets {
				if callerSets[target] == nil {
					callerSets[target] = make(map[int]bool)
				}
				callerSets[target][index] = true
			}
		}
	}
	analyzer.callers = make([][]int, len(analyzer.functions))
	for target, set := range callerSets {
		for caller := range set {
			analyzer.callers[target] = append(analyzer.callers[target], caller)
		}
		sort.Ints(analyzer.callers[target])
	}
	return analyzer
}

func (a *flowAnalyzer) internFrame(frame string) int {
	if id, ok := a.frameIndex[frame]; ok {
		return id
	}
	id := len(a.frameIndex)
	a.frameIndex[frame] = id
	return id
}

func (a *flowAnalyzer) internStack(frames []int) int {
	var key strings.Builder
	for _, frame := range frames {
		key.WriteString(strconv.Itoa(frame))
		key.WriteByte(',')
	}
	if id, ok := a.stackIndex[key.String()]; ok {
		return id
	}
	id := len(a.stacks)
	a.stacks = append(a.stacks, append([]int(nil), frames...))
	a.stackIndex[key.String()] = id
	return id
}

// pushFrame enters a call. Beyond the context depth the oldest frame is dropped.
func (a *flowAnalyzer) pushFrame(trace flowTrace, frame int) flowTrace {
	current := a.stacks[trace.key.stack]
	frames := make([]int, 0, len(current)+1)
	frames = append(frames, current...)
	frames = append(frames, frame)
	if len(frames) > a.contextDepth {
		frames = frames[len(frames)-a.contextDepth:]
		trace.truncated = true
	}
	trace.key.stack = a.internStack(frames)
	return trace
}

func (a *flowAnalyzer) internSanitized(base int, kinds map[string]bool) int {
	merged := make([]string, 0, len(a.sanitized[base])+len(kinds))
	for kind := range a.sanitized[base] {
		merged = append(merged, kind)
	}
	for kind := range kinds {
		if !a.sanitized[base][kind] {
			merged = append(merged, kind)
		}
	}
	sort.Strings(merged)
	key := strings.Join(merged, ",")
	if id, ok := a.sanIndex[key]; ok {
		return id
	}
	set := make(map[string]bool, len(merged))
	for _, kind := range merged {
		set[kind] = true
	}
	id := len(a.sanitized)
	a.sanitized = append(a.sanitized, set)
	a.sanIndex[key] = id
	return id
}

func (a *flowAnalyzer) sourceTrace(function graph.FlowFunction, fact graph.FlowFact) flowTrace {
	key := fmt.Sprintf("%s|%s|%d|%s|%s", fact.SourceKind, function.File, fact.Line, function.ID, fact.Target)
	id, ok := a.sourceIndex[key]
	if !ok {
		id = len(a.sources)
		a.sources = append(a.sources, flowSource{key: key, endpoint: FlowEndpoint{
			Kind: fact.SourceKind, Label: fact.Detail, Function: function.Name,
			File: function.File, Line: fact.Line,
		}})
		a.sourceIndex[key] = id
	}
	return flowTrace{key: flowTraceKey{source: id}, path: appendFlowPath(nil, FlowStep{
		Kind: "source", Function: function.Name, Detail: fact.Detail,
		File: function.File, Line: fact.Line,
	})}
}

func (a *flowAnalyzer) enqueue(index int) {
	if !a.queued[index] {
		a.queued[index] = true
		a.queue = append(a.queue, index)
	}
}

// propagate runs a worklist to a fixed point: a function is revisited only
// when a value it reads, or a callee's return value, gained a trace.
func (a *flowAnalyzer) propagate() {
	a.queued = make([]bool, len(a.functions))
	for index := range a.functions {
		a.enqueue(index)
	}
	for len(a.queue) > 0 && !a.exhausted {
		index := a.queue[0]
		a.queue = a.queue[1:]
		a.queued[index] = false
		a.process(index)
	}
}

func (a *flowAnalyzer) process(index int) {
	function := a.functions[index]
	for factIndex, fact := range function.Facts {
		if a.exhausted {
			return
		}
		switch fact.Kind {
		case "source":
			if a.options.Source != "" && fact.SourceKind != a.options.Source {
				continue
			}
			a.addTrace(index, fact.Target, a.sourceTrace(function, fact))
		case "transfer", "return":
			for _, trace := range a.tracesFor(index, fact.Inputs) {
				trace.path = appendFlowPath(trace.path, FlowStep{
					Kind: fact.Kind, Function: function.Name, Detail: fact.Detail,
					File: function.File, Line: fact.Line,
				})
				a.addTrace(index, fact.Target, trace)
			}
		case "call":
			for _, source := range a.customSources[[2]int{index, factIndex}] {
				if a.options.Source != "" && source.Kind != a.options.Source {
					continue
				}
				sourceFact := graph.FlowFact{Kind: "source", SourceKind: source.Kind, Target: fact.Target, Detail: "return of " + fact.Callee + " (custom source)", Line: fact.Line}
				trace := a.sourceTrace(function, sourceFact)
				if source.Return == nil {
					a.addTrace(index, fact.Target, trace)
					a.addTrace(index, fact.Target+":0", trace)
					for result := 1; result < 8; result++ {
						a.addTrace(index, fmt.Sprintf("%s:%d", fact.Target, result), trace)
					}
					continue
				}
				if *source.Return == 0 {
					a.addTrace(index, fact.Target, trace)
				}
				a.addTrace(index, fmt.Sprintf("%s:%d", fact.Target, *source.Return), trace)
			}
			a.propagateCall(index, fact, a.calls[index][factIndex])
		}
	}
}

// addTrace records a trace and schedules the functions that can observe it.
func (a *flowAnalyzer) addTrace(index int, target string, trace flowTrace) {
	if target == "" || a.exhausted {
		return
	}
	a.work++
	a.workBy[index]++
	if a.work > a.maxWork {
		a.exhausted = true
		return
	}
	state := a.states[index]
	byKey := state.values[target]
	if byKey == nil {
		byKey = make(map[flowTraceKey]flowTrace)
		state.values[target] = byKey
		root := flowRefRoot(target)
		state.byRoot[root] = append(state.byRoot[root], target)
		if flowReturnIndex(target) >= 0 {
			state.returns = append(state.returns, target)
		}
	}
	if existing, exists := byKey[trace.key]; exists && !betterFlowTrace(trace, existing) {
		return
	}
	byKey[trace.key] = trace
	a.enqueue(index)
	if flowReturnIndex(target) >= 0 {
		for _, caller := range a.callers[index] {
			a.enqueue(caller)
		}
	}
}

func (s *flowState) tracesForRefs(refs []string) []flowTrace {
	best := make(map[flowTraceKey]flowTrace)
	for _, ref := range refs {
		for _, value := range s.byRoot[flowRefRoot(ref)] {
			if !flowRefsRelated(ref, value) {
				continue
			}
			for key, trace := range s.values[value] {
				if existing, ok := best[key]; !ok || betterFlowTrace(trace, existing) {
					best[key] = trace
				}
			}
		}
	}
	return sortedFlowTraces(best)
}

func sortedFlowTraces(best map[flowTraceKey]flowTrace) []flowTrace {
	result := make([]flowTrace, 0, len(best))
	for _, trace := range best {
		result = append(result, trace)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i].key, result[j].key
		if left.source != right.source {
			return left.source < right.source
		}
		if left.sanitized != right.sanitized {
			return left.sanitized < right.sanitized
		}
		return left.stack < right.stack
	})
	return result
}

func dedupeFlowTraces(traces []flowTrace) []flowTrace {
	best := make(map[flowTraceKey]flowTrace, len(traces))
	for _, trace := range traces {
		if existing, ok := best[trace.key]; !ok || betterFlowTrace(trace, existing) {
			best[trace.key] = trace
		}
	}
	return sortedFlowTraces(best)
}

// returnTraces collects a callee's return traces visible at one call site:
// traces whose innermost frame is this call, with that frame popped, and
// traces that originated inside the callee (empty stack).
func (a *flowAnalyzer) returnTraces(target, frame int) map[int][]flowTrace {
	result := make(map[int][]flowTrace)
	state := a.states[target]
	for _, value := range state.returns {
		index := flowReturnIndex(value)
		for _, trace := range state.values[value] {
			frames := a.stacks[trace.key.stack]
			if len(frames) > 0 {
				last := len(frames) - 1
				if frames[last] != frame {
					continue
				}
				trace.key.stack = a.internStack(frames[:last])
			} else if trace.truncated {
				// The matching call site was dropped from the stack; this
				// return may reach callers that never passed the value.
				trace.low = true
			}
			result[index] = append(result[index], trace)
		}
	}
	return result
}

func (a *flowAnalyzer) propagateCall(callerIndex int, fact graph.FlowFact, call flowCall) {
	caller := a.functions[callerIndex]
	inputTraces := a.tracesFor(callerIndex, fact.Inputs)

	for _, targetIndex := range call.targets {
		target := a.functions[targetIndex]
		for argumentIndex, refs := range fact.Arguments {
			parameterIndex := argumentIndex
			if parameterIndex >= len(target.Params) {
				if len(target.Params) == 0 || !strings.HasPrefix(target.Params[len(target.Params)-1].Type, "...") {
					continue
				}
				parameterIndex = len(target.Params) - 1
			}
			parameter := target.Params[parameterIndex]
			for _, trace := range a.tracesFor(callerIndex, refs) {
				trace = a.pushFrame(trace, call.frame)
				trace.low = trace.low || call.uncertain
				trace.path = appendFlowPath(trace.path, FlowStep{
					Kind: "call", Function: caller.Name,
					Detail: fmt.Sprintf("passes value to %s", target.Name),
					File:   caller.File, Line: fact.Line,
				})
				a.addTrace(targetIndex, parameter.Name, trace)
			}
		}
	}

	returnTraces := make(map[int][]flowTrace)
	for _, targetIndex := range call.targets {
		for index, traces := range a.returnTraces(targetIndex, call.frame) {
			returnTraces[index] = append(returnTraces[index], traces...)
		}
	}
	if len(call.sanitized) > 0 {
		firstReturn := append(returnTraces[0], inputTraces...)
		for _, trace := range dedupeFlowTraces(firstReturn) {
			trace.key.sanitized = a.internSanitized(trace.key.sanitized, call.sanitized)
			trace.path = appendFlowPath(trace.path, FlowStep{
				Kind: "sanitizer", Function: caller.Name,
				Detail: fact.Detail, File: caller.File, Line: fact.Line,
			})
			a.addTrace(callerIndex, fact.Target, trace)
			a.addTrace(callerIndex, fact.Target+":0", trace)
		}
		return
	}

	if len(call.targets) > 0 {
		indexes := make([]int, 0, len(returnTraces))
		for index := range returnTraces {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			for _, trace := range dedupeFlowTraces(returnTraces[index]) {
				trace.low = trace.low || call.uncertain
				trace.path = appendFlowPath(trace.path, FlowStep{
					Kind: "return", Function: caller.Name,
					Detail: fmt.Sprintf("returns from %s", fact.Callee),
					File:   caller.File, Line: fact.Line,
				})
				a.addTrace(callerIndex, fmt.Sprintf("%s:%d", fact.Target, index), trace)
				if index == 0 {
					a.addTrace(callerIndex, fact.Target, trace)
				}
			}
		}
		return
	}

	for _, trace := range inputTraces {
		trace.low = true
		trace.path = appendFlowPath(trace.path, FlowStep{
			Kind: "external_call", Function: caller.Name,
			Detail: fact.Detail, File: caller.File, Line: fact.Line,
		})
		a.addTrace(callerIndex, fact.Target, trace)
		a.addTrace(callerIndex, fact.Target+":0", trace)
	}
}

func (a *flowAnalyzer) resolveCall(caller graph.FlowFunction, fact graph.FlowFact) ([]int, bool) {
	site := flowCallSite(caller.ID, caller.File, fact.Line)
	var preciseKeys []int
	for _, call := range a.callsBySite[site] {
		if call.CalleeRaw == fact.Callee && call.CalleeSymbolID != "" {
			preciseKeys = append(preciseKeys, a.keysByID[call.CalleeSymbolID]...)
		}
	}
	preciseKeys = uniqueFlowIndexes(preciseKeys)
	if len(preciseKeys) > 0 {
		return preciseKeys, false
	}

	shortName := flowShortFunctionName(fact.Callee)
	if dot := strings.Index(fact.Callee, "."); dot > 0 {
		qualifier := fact.Callee[:dot]
		if importPath := a.importsByFile[caller.File][qualifier]; importPath != "" {
			return uniqueFlowIndexes(a.keysByID[importPath+"::"+shortName]), false
		}
	}
	if !strings.Contains(fact.Callee, ".") {
		if separator := strings.LastIndex(caller.ID, "::"); separator >= 0 {
			id := caller.ID[:separator+2] + shortName
			if keys := a.keysByID[id]; len(keys) > 0 {
				return uniqueFlowIndexes(keys), false
			}
		}
	}
	return nil, false
}

func uniqueFlowIndexes(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	result := sorted[:1]
	for _, value := range sorted[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

// callCandidates lists the spellings a call fact can be matched by: its
// selector text, resolved repository flow targets, and precise callee IDs
// (including dependencies such as os/exec::Command) recorded at the site.
func (a *flowAnalyzer) callCandidates(caller graph.FlowFunction, fact graph.FlowFact, targets []int) []string {
	candidates := []string{fact.Callee}
	for _, target := range targets {
		function := a.functions[target]
		candidates = append(candidates, function.ID, function.Name)
	}
	for _, call := range a.callsBySite[flowCallSite(caller.ID, caller.File, fact.Line)] {
		if call.CalleeRaw == fact.Callee && call.CalleeSymbolID != "" {
			candidates = append(candidates, call.CalleeSymbolID)
		}
	}
	return candidates
}

func flowCandidatesMatch(candidates []string, configured string) bool {
	for _, candidate := range candidates {
		if flowFunctionMatches(candidate, configured) {
			return true
		}
	}
	return false
}

// indexCustomRules records configured sources, sinks and argument
// sanitizers that match one call fact.
func (a *flowAnalyzer) indexCustomRules(index, factIndex int, fact graph.FlowFact, candidates []string) {
	key := [2]int{index, factIndex}
	for _, source := range a.config.Sources {
		if flowCandidatesMatch(candidates, source.Function) {
			a.customSources[key] = append(a.customSources[key], source)
		}
	}
	for _, sink := range a.config.Sinks {
		if flowCandidatesMatch(candidates, sink.Function) {
			a.customSinks[key] = append(a.customSinks[key], sink)
		}
	}
	for _, sanitizer := range a.config.Sanitizers {
		if sanitizer.Argument == nil || *sanitizer.Argument >= len(fact.Arguments) || !flowCandidatesMatch(candidates, sanitizer.Function) {
			continue
		}
		refs := fact.Arguments[*sanitizer.Argument]
		if len(refs) == 0 {
			continue
		}
		kinds := map[string]bool{"*": true}
		if len(sanitizer.For) > 0 {
			kinds = make(map[string]bool, len(sanitizer.For))
			for _, kind := range sanitizer.For {
				kinds[kind] = true
			}
		}
		// The first ref is the whole argument expression; its fields are
		// validated with it, its parents are not.
		a.argSanitized[index] = append(a.argSanitized[index], flowArgSanitized{ref: refs[0], kinds: kinds})
	}
}

// tracesFor returns the traces reaching refs in one function, marking those
// read through a value an argument sanitizer validated in that function.
func (a *flowAnalyzer) tracesFor(index int, refs []string) []flowTrace {
	state := a.states[index]
	sanitizers := a.argSanitized[index]
	if len(sanitizers) == 0 {
		return state.tracesForRefs(refs)
	}
	var plain []string
	var traces []flowTrace
	for _, ref := range refs {
		var kinds map[string]bool
		for _, sanitizer := range sanitizers {
			if ref == sanitizer.ref || strings.HasPrefix(ref, sanitizer.ref+".") {
				if kinds == nil {
					kinds = make(map[string]bool)
				}
				for kind := range sanitizer.kinds {
					kinds[kind] = true
				}
			}
		}
		if kinds == nil {
			plain = append(plain, ref)
			continue
		}
		for _, trace := range state.tracesForRefs([]string{ref}) {
			trace.key.sanitized = a.internSanitized(trace.key.sanitized, kinds)
			traces = append(traces, trace)
		}
	}
	if traces == nil {
		return state.tracesForRefs(plain)
	}
	return dedupeFlowTraces(append(traces, state.tracesForRefs(plain)...))
}

// topWork returns the functions with the most propagation work.
func (a *flowAnalyzer) topWork(limit int) []FlowWorkShare {
	shares := make([]FlowWorkShare, 0, len(a.functions))
	for index, work := range a.workBy {
		if work > 0 {
			shares = append(shares, FlowWorkShare{Function: a.functions[index].ID, File: a.functions[index].File, Work: work})
		}
	}
	sort.Slice(shares, func(i, j int) bool {
		if shares[i].Work != shares[j].Work {
			return shares[i].Work > shares[j].Work
		}
		return shares[i].Function < shares[j].Function
	})
	if len(shares) > limit {
		shares = shares[:limit]
	}
	return shares
}

func (a *flowAnalyzer) sanitizerKinds(candidates []string) map[string]bool {
	matched := make(map[string]bool)
	for _, sanitizer := range a.config.Sanitizers {
		if sanitizer.Argument != nil {
			continue
		}
		found := false
		for _, candidate := range candidates {
			if flowFunctionMatches(candidate, sanitizer.Function) {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		if len(sanitizer.For) == 0 {
			matched["*"] = true
			continue
		}
		for _, kind := range sanitizer.For {
			matched[kind] = true
		}
	}
	return matched
}

func (a *flowAnalyzer) findings() []FlowResult {
	byKey := make(map[string]FlowResult)
	record := func(index int, fact graph.FlowFact, kind, severity string, parameterOnly bool, refs []string) {
		function := a.functions[index]
		for _, trace := range a.tracesFor(index, refs) {
			sanitized := a.sanitized[trace.key.sanitized]
			if sanitized["*"] || sanitized[kind] {
				continue
			}
			confidence := "medium"
			if trace.low {
				confidence = "low"
			}
			source := a.sources[trace.key.source]
			sink := FlowEndpoint{
				Kind: kind, Label: fact.Detail, Function: function.Name,
				File: function.File, Line: fact.Line,
			}
			resultSeverity := severity
			if resultSeverity == "" {
				resultSeverity = flowSeverity(source.endpoint.Kind, kind)
			}
			if parameterOnly && resultSeverity == "high" {
				resultSeverity = "medium"
			}
			result := FlowResult{
				Severity: resultSeverity, Confidence: confidence, ParameterOnly: parameterOnly,
				Source: source.endpoint, Sink: sink,
				Path: appendFlowPath(trace.path, FlowStep{
					Kind: "sink", Function: function.Name, Detail: fact.Detail,
					File: function.File, Line: fact.Line,
				}).steps(),
			}
			if !flowResultMatches(result, a.options.Term) {
				continue
			}
			key := fmt.Sprintf("%s|%s|%d|%s|%s|%d", source.key, sink.File, sink.Line, sink.Kind, function.ID, fact.Column)
			if existing, ok := byKey[key]; !ok || betterFlowResult(result, existing) {
				byKey[key] = result
			}
		}
	}
	for index, function := range a.functions {
		for factIndex, fact := range function.Facts {
			if fact.Kind == "sink" && (a.options.Sink == "" || fact.SinkKind == a.options.Sink) {
				record(index, fact, fact.SinkKind, "", fact.ParameterOnly, fact.Inputs)
			}
			for _, sink := range a.customSinks[[2]int{index, factIndex}] {
				if a.options.Sink != "" && sink.Kind != a.options.Sink {
					continue
				}
				severity := sink.Severity
				if severity == "" {
					severity = "high"
				}
				var refs []string
				for argumentIndex, argument := range fact.Arguments {
					if sink.Argument == nil || *sink.Argument == argumentIndex {
						refs = append(refs, argument...)
					}
				}
				record(index, fact, sink.Kind, severity, false, refs)
			}
		}
	}
	results := make([]FlowResult, 0, len(byKey))
	for _, result := range byKey {
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool {
		left, right := results[i], results[j]
		if flowSeverityRank(left.Severity) != flowSeverityRank(right.Severity) {
			return flowSeverityRank(left.Severity) > flowSeverityRank(right.Severity)
		}
		if left.Sink.File != right.Sink.File {
			return left.Sink.File < right.Sink.File
		}
		if left.Sink.Line != right.Sink.Line {
			return left.Sink.Line < right.Sink.Line
		}
		if left.Source.File != right.Source.File {
			return left.Source.File < right.Source.File
		}
		if left.Source.Line != right.Source.Line {
			return left.Source.Line < right.Source.Line
		}
		if left.Sink.Function != right.Sink.Function {
			return left.Sink.Function < right.Sink.Function
		}
		return left.Source.Kind < right.Source.Kind
	})
	return results
}

func betterFlowResult(candidate, existing FlowResult) bool {
	if candidate.Confidence != existing.Confidence {
		return candidate.Confidence == "medium"
	}
	if len(candidate.Path) != len(existing.Path) {
		return len(candidate.Path) < len(existing.Path)
	}
	for index := range candidate.Path {
		left, right := candidate.Path[index], existing.Path[index]
		if left != right {
			if left.File != right.File {
				return left.File < right.File
			}
			if left.Line != right.Line {
				return left.Line < right.Line
			}
			return left.Kind+left.Function+left.Detail < right.Kind+right.Function+right.Detail
		}
	}
	return false
}

func flowRefsRelated(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+".") || strings.HasPrefix(right, left+".")
}

func flowFunctionKey(function graph.FlowFunction) string {
	return function.ID + "|" + function.File
}

func flowCallSite(callerID, file string, line int) string {
	return fmt.Sprintf("%s|%s|%d", callerID, file, line)
}

func flowCallFrame(caller graph.FlowFunction, fact graph.FlowFact) string {
	return fmt.Sprintf("%s|%s|%d|%d|%s", caller.ID, caller.File, fact.Line, fact.Column, fact.Target)
}

func flowShortFunctionName(name string) string {
	if separator := strings.LastIndex(name, "::"); separator >= 0 {
		name = name[separator+2:]
	}
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[dot+1:]
	}
	return name
}

func flowFunctionMatches(candidate, configured string) bool {
	candidate = strings.TrimSpace(candidate)
	configured = strings.TrimSpace(configured)
	if strings.EqualFold(candidate, configured) {
		return true
	}
	if !strings.Contains(configured, ".") && !strings.Contains(configured, "::") {
		return strings.EqualFold(flowShortFunctionName(candidate), configured)
	}
	return strings.HasSuffix(strings.ToLower(candidate), "."+strings.ToLower(configured)) ||
		strings.HasSuffix(strings.ToLower(candidate), "::"+strings.ToLower(configured))
}

func flowSeverity(source, sink string) string {
	if sink == "sql_query" || sink == "process_execution" {
		return "high"
	}
	if sink == "filesystem" && (source == "http_request" || source == "decoded_json") {
		return "high"
	}
	return "medium"
}

func flowSeverityRank(severity string) int {
	switch severity {
	case "high":
		return 3
	case "medium":
		return 2
	default:
		return 1
	}
}

func flowResultMatches(result FlowResult, term string) bool {
	term = strings.ToLower(strings.TrimSpace(term))
	if term == "" {
		return true
	}
	values := []string{
		result.Source.Kind, result.Source.Label, result.Source.Function, result.Source.File,
		result.Sink.Kind, result.Sink.Label, result.Sink.Function, result.Sink.File,
	}
	for _, step := range result.Path {
		values = append(values, step.Kind, step.Function, step.Detail, step.File)
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), term) {
			return true
		}
	}
	return false
}
