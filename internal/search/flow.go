package search

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
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

const maxFlowCallDepth = 16

// FlowOptions controls a security-flow query.
type FlowOptions struct {
	Term         string
	Source       string
	Sink         string
	ConfigPath   string
	IncludeTests bool
}

// FlowSanitizer declares a function whose return value is trusted for the
// listed sink kinds. An empty For list applies to every sink kind.
type FlowSanitizer struct {
	Function string   `json:"function"`
	For      []string `json:"for,omitempty"`
}

// FlowConfig is the schema for .gograph/flow.json.
type FlowConfig struct {
	Sanitizers []FlowSanitizer `json:"sanitizers"`
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
	Severity   string       `json:"severity"`
	Confidence string       `json:"confidence"`
	Source     FlowEndpoint `json:"source"`
	Sink       FlowEndpoint `json:"sink"`
	Path       []FlowStep   `json:"path"`
}

func (r FlowResult) String() string {
	return fmt.Sprintf("[%s] %s -> %s (%s confidence)  (%s:%d)",
		strings.ToUpper(r.Severity), r.Source.Kind, r.Sink.Kind, r.Confidence, r.Sink.File, r.Sink.Line)
}

type flowTrace struct {
	Source        FlowEndpoint
	SourceKey     string
	Path          []FlowStep
	SanitizedFor  map[string]bool
	CallStack     []string
	LowConfidence bool
}

type flowAnalyzer struct {
	options       FlowOptions
	config        FlowConfig
	functions     []graph.FlowFunction
	functionByKey map[string]graph.FlowFunction
	keysByID      map[string][]string
	importsByFile map[string]map[string]string
	callsBySite   map[string][]graph.CallEdge
	states        map[string]map[string]map[string]flowTrace
}

// Flow finds potential paths from untrusted inputs to security-sensitive
// operations. It uses persisted AST facts and never executes target code.
func Flow(g *graph.Graph, options FlowOptions) ([]FlowResult, error) {
	if g == nil {
		return nil, fmt.Errorf("flow analysis requires a graph")
	}
	if options.Source != "" && !validFlowSources[options.Source] {
		return nil, fmt.Errorf("unsupported flow source %q (valid: decoded_json, environment, http_request)", options.Source)
	}
	if options.Sink != "" && !validFlowSinks[options.Sink] {
		return nil, fmt.Errorf("unsupported flow sink %q (valid: filesystem, outbound_http, process_execution, sql_query)", options.Sink)
	}

	config, err := loadFlowConfig(g, options.ConfigPath)
	if err != nil {
		return nil, err
	}
	if len(g.FlowFunctions) == 0 {
		return nil, fmt.Errorf("graph contains no security-flow facts; run `gograph build .` to refresh it")
	}

	analyzer := newFlowAnalyzer(g, options, config)
	analyzer.propagate()
	return analyzer.findings(), nil
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
	if _, err := os.Lstat(resolvedPath); err != nil {
		if optional && os.IsNotExist(err) {
			return FlowConfig{}, nil
		}
		return FlowConfig{}, fmt.Errorf("could not read flow config file: %w", err)
	}
	if err := validateFlowConfigTarget(g, resolvedPath); err != nil {
		return FlowConfig{}, err
	}
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return FlowConfig{}, fmt.Errorf("could not read flow config file: %w", err)
	}
	var config FlowConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return FlowConfig{}, fmt.Errorf("invalid JSON in flow config: %w", err)
	}
	for index, sanitizer := range config.Sanitizers {
		if strings.TrimSpace(sanitizer.Function) == "" {
			return FlowConfig{}, fmt.Errorf("invalid flow config: sanitizers[%d].function is required", index)
		}
		for _, sink := range sanitizer.For {
			if !validFlowSinks[sink] {
				return FlowConfig{}, fmt.Errorf("invalid flow config: sanitizer %q has unsupported sink %q", sanitizer.Function, sink)
			}
		}
	}
	return config, nil
}

func validateFlowConfigTarget(g *graph.Graph, configPath string) error {
	if g == nil || g.Root == "" {
		return nil
	}
	absRoot, err := filepath.Abs(g.Root)
	if err != nil {
		return fmt.Errorf("resolve graph root: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return fmt.Errorf("resolve graph root symlinks: %w", err)
	}
	realConfig, err := filepath.EvalSymlinks(configPath)
	if err != nil {
		return fmt.Errorf("resolve flow config symlinks: %w", err)
	}
	relative, err := filepath.Rel(realRoot, realConfig)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("invalid config path: symlink target must be inside graph root")
	}
	return nil
}

func newFlowAnalyzer(g *graph.Graph, options FlowOptions, config FlowConfig) *flowAnalyzer {
	analyzer := &flowAnalyzer{
		options:       options,
		config:        config,
		functionByKey: make(map[string]graph.FlowFunction),
		keysByID:      make(map[string][]string),
		importsByFile: make(map[string]map[string]string),
		callsBySite:   make(map[string][]graph.CallEdge),
		states:        make(map[string]map[string]map[string]flowTrace),
	}
	for _, function := range g.FlowFunctions {
		if !options.IncludeTests && isTestFile(function.File) {
			continue
		}
		analyzer.functions = append(analyzer.functions, function)
		key := flowFunctionKey(function)
		analyzer.functionByKey[key] = function
		analyzer.keysByID[function.ID] = append(analyzer.keysByID[function.ID], key)
		analyzer.states[key] = make(map[string]map[string]flowTrace)
	}
	sort.Slice(analyzer.functions, func(i, j int) bool {
		return flowFunctionKey(analyzer.functions[i]) < flowFunctionKey(analyzer.functions[j])
	})
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
	return analyzer
}

func (a *flowAnalyzer) propagate() {
	maxIterations := 2*len(a.functions) + 10
	for iteration := 0; iteration < maxIterations; iteration++ {
		changed := false
		for _, function := range a.functions {
			key := flowFunctionKey(function)
			state := a.states[key]
			for _, fact := range function.Facts {
				switch fact.Kind {
				case "source":
					if a.options.Source != "" && fact.SourceKind != a.options.Source {
						continue
					}
					trace := sourceFlowTrace(function, fact)
					changed = addFlowTrace(state, fact.Target, trace) || changed
				case "transfer", "return":
					for _, trace := range flowTracesForRefs(state, fact.Inputs) {
						trace.Path = appendFlowStep(trace.Path, FlowStep{
							Kind: fact.Kind, Function: function.Name, Detail: fact.Detail,
							File: function.File, Line: fact.Line,
						})
						changed = addFlowTrace(state, fact.Target, trace) || changed
					}
				case "call":
					changed = a.propagateCall(function, fact) || changed
				}
			}
		}
		if !changed {
			return
		}
	}
}

func (a *flowAnalyzer) propagateCall(caller graph.FlowFunction, fact graph.FlowFact) bool {
	callerKey := flowFunctionKey(caller)
	callerState := a.states[callerKey]
	inputTraces := flowTracesForRefs(callerState, fact.Inputs)
	targetKeys, uncertain := a.resolveCall(caller, fact)
	callFrame := flowCallFrame(caller, fact)
	changed := false

	for _, targetKey := range targetKeys {
		target := a.functionByKey[targetKey]
		targetState := a.states[targetKey]
		for argumentIndex, refs := range fact.Arguments {
			parameterIndex := argumentIndex
			if parameterIndex >= len(target.Params) {
				if len(target.Params) == 0 || !strings.HasPrefix(target.Params[len(target.Params)-1].Type, "...") {
					continue
				}
				parameterIndex = len(target.Params) - 1
			}
			parameter := target.Params[parameterIndex]
			for _, trace := range flowTracesForRefs(callerState, refs) {
				if len(trace.CallStack) >= maxFlowCallDepth {
					continue
				}
				trace.CallStack = appendFlowFrame(trace.CallStack, callFrame)
				trace.LowConfidence = trace.LowConfidence || uncertain
				trace.Path = appendFlowStep(trace.Path, FlowStep{
					Kind: "call", Function: caller.Name,
					Detail: fmt.Sprintf("passes value to %s", target.Name),
					File:   caller.File, Line: fact.Line,
				})
				changed = addFlowTrace(targetState, parameter.Name, trace) || changed
			}
		}
	}

	returnTraces := make(map[int][]flowTrace)
	for _, targetKey := range targetKeys {
		for index, traces := range flowReturnTraces(a.states[targetKey], callFrame) {
			returnTraces[index] = append(returnTraces[index], traces...)
		}
	}
	sanitizedKinds := a.sanitizerKinds(fact, targetKeys)
	if len(sanitizedKinds) > 0 {
		firstReturn := append(returnTraces[0], inputTraces...)
		for _, trace := range dedupeFlowTraces(firstReturn) {
			trace.SanitizedFor = cloneFlowSet(trace.SanitizedFor)
			for kind := range sanitizedKinds {
				trace.SanitizedFor[kind] = true
			}
			trace.Path = appendFlowStep(trace.Path, FlowStep{
				Kind: "sanitizer", Function: caller.Name,
				Detail: fact.Detail, File: caller.File, Line: fact.Line,
			})
			changed = addFlowTrace(callerState, fact.Target, trace) || changed
			changed = addFlowTrace(callerState, fact.Target+":0", trace) || changed
		}
		return changed
	}

	if len(targetKeys) > 0 {
		for index, traces := range returnTraces {
			for _, trace := range dedupeFlowTraces(traces) {
				trace.LowConfidence = trace.LowConfidence || uncertain
				trace.Path = appendFlowStep(trace.Path, FlowStep{
					Kind: "return", Function: caller.Name,
					Detail: fmt.Sprintf("returns from %s", fact.Callee),
					File:   caller.File, Line: fact.Line,
				})
				changed = addFlowTrace(callerState, fmt.Sprintf("%s:%d", fact.Target, index), trace) || changed
				if index == 0 {
					changed = addFlowTrace(callerState, fact.Target, trace) || changed
				}
			}
		}
		return changed
	}

	for _, trace := range inputTraces {
		trace.LowConfidence = true
		trace.Path = appendFlowStep(trace.Path, FlowStep{
			Kind: "external_call", Function: caller.Name,
			Detail: fact.Detail, File: caller.File, Line: fact.Line,
		})
		changed = addFlowTrace(callerState, fact.Target, trace) || changed
		changed = addFlowTrace(callerState, fact.Target+":0", trace) || changed
	}
	return changed
}

func (a *flowAnalyzer) resolveCall(caller graph.FlowFunction, fact graph.FlowFact) ([]string, bool) {
	site := flowCallSite(caller.ID, caller.File, fact.Line)
	var preciseKeys []string
	for _, call := range a.callsBySite[site] {
		if call.CalleeRaw == fact.Callee && call.CalleeSymbolID != "" {
			preciseKeys = append(preciseKeys, a.keysByID[call.CalleeSymbolID]...)
		}
	}
	preciseKeys = uniqueFlowStrings(preciseKeys)
	if len(preciseKeys) > 0 {
		return preciseKeys, false
	}

	shortName := flowShortFunctionName(fact.Callee)
	if dot := strings.Index(fact.Callee, "."); dot > 0 {
		qualifier := fact.Callee[:dot]
		if importPath := a.importsByFile[caller.File][qualifier]; importPath != "" {
			if keys := a.keysByID[importPath+"::"+shortName]; len(keys) > 0 {
				return uniqueFlowStrings(keys), false
			}
			return nil, false
		}
	}
	if !strings.Contains(fact.Callee, ".") {
		if separator := strings.LastIndex(caller.ID, "::"); separator >= 0 {
			id := caller.ID[:separator+2] + shortName
			if keys := a.keysByID[id]; len(keys) > 0 {
				return uniqueFlowStrings(keys), false
			}
		}
	}
	return nil, false
}

func flowReturnTraces(state map[string]map[string]flowTrace, callFrame string) map[int][]flowTrace {
	result := make(map[int][]flowTrace)
	for value, traces := range state {
		index := -1
		switch {
		case value == "$return":
			index = 0
		case strings.HasPrefix(value, "$return:"):
			parsed, err := strconv.Atoi(strings.TrimPrefix(value, "$return:"))
			if err == nil && parsed >= 0 {
				index = parsed
			}
		}
		if index < 0 {
			continue
		}
		for _, trace := range traces {
			if len(trace.CallStack) > 0 {
				last := len(trace.CallStack) - 1
				if trace.CallStack[last] != callFrame {
					continue
				}
				trace.CallStack = cloneFlowFrames(trace.CallStack[:last])
			}
			result[index] = append(result[index], trace)
		}
	}
	for index, traces := range result {
		result[index] = dedupeFlowTraces(traces)
	}
	return result
}

func (a *flowAnalyzer) sanitizerKinds(fact graph.FlowFact, targetKeys []string) map[string]bool {
	candidates := []string{fact.Callee}
	for _, key := range targetKeys {
		function := a.functionByKey[key]
		candidates = append(candidates, function.ID, function.Name)
	}
	matched := make(map[string]bool)
	for _, sanitizer := range a.config.Sanitizers {
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
	for _, function := range a.functions {
		state := a.states[flowFunctionKey(function)]
		for _, fact := range function.Facts {
			if fact.Kind != "sink" || (a.options.Sink != "" && fact.SinkKind != a.options.Sink) {
				continue
			}
			for _, trace := range flowTracesForRefs(state, fact.Inputs) {
				if trace.SanitizedFor["*"] || trace.SanitizedFor[fact.SinkKind] {
					continue
				}
				confidence := "medium"
				if trace.LowConfidence {
					confidence = "low"
				}
				sink := FlowEndpoint{
					Kind: fact.SinkKind, Label: fact.Detail, Function: function.Name,
					File: function.File, Line: fact.Line,
				}
				result := FlowResult{
					Severity:   flowSeverity(trace.Source.Kind, fact.SinkKind),
					Confidence: confidence, Source: trace.Source, Sink: sink,
					Path: appendFlowStep(trace.Path, FlowStep{
						Kind: "sink", Function: function.Name, Detail: fact.Detail,
						File: function.File, Line: fact.Line,
					}),
				}
				if !flowResultMatches(result, a.options.Term) {
					continue
				}
				key := fmt.Sprintf("%s|%s|%d|%s|%s|%d", trace.SourceKey, sink.File, sink.Line, sink.Kind, function.ID, fact.Column)
				if existing, ok := byKey[key]; !ok || betterFlowResult(result, existing) {
					byKey[key] = result
				}
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
		return left.Source.Line < right.Source.Line
	})
	return results
}

func sourceFlowTrace(function graph.FlowFunction, fact graph.FlowFact) flowTrace {
	endpoint := FlowEndpoint{
		Kind: fact.SourceKind, Label: fact.Detail, Function: function.Name,
		File: function.File, Line: fact.Line,
	}
	key := fmt.Sprintf("%s|%s|%d|%s|%s", fact.SourceKind, function.File, fact.Line, function.ID, fact.Target)
	return flowTrace{
		Source: endpoint, SourceKey: key, SanitizedFor: make(map[string]bool),
		Path: []FlowStep{{
			Kind: "source", Function: function.Name, Detail: fact.Detail,
			File: function.File, Line: fact.Line,
		}},
	}
}

func addFlowTrace(state map[string]map[string]flowTrace, target string, trace flowTrace) bool {
	if target == "" {
		return false
	}
	if state[target] == nil {
		state[target] = make(map[string]flowTrace)
	}
	key := flowTraceKey(trace)
	existing, exists := state[target][key]
	if exists && !betterFlowTrace(trace, existing) {
		return false
	}
	state[target][key] = trace
	return true
}

func flowTracesForRefs(state map[string]map[string]flowTrace, refs []string) []flowTrace {
	var traces []flowTrace
	for _, ref := range refs {
		for value, byTrace := range state {
			if !flowRefsRelated(ref, value) {
				continue
			}
			for _, trace := range byTrace {
				traces = append(traces, trace)
			}
		}
	}
	return dedupeFlowTraces(traces)
}

func dedupeFlowTraces(traces []flowTrace) []flowTrace {
	best := make(map[string]flowTrace)
	for _, trace := range traces {
		key := flowTraceKey(trace)
		if existing, ok := best[key]; !ok || betterFlowTrace(trace, existing) {
			best[key] = trace
		}
	}
	keys := make([]string, 0, len(best))
	for key := range best {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]flowTrace, 0, len(keys))
	for _, key := range keys {
		result = append(result, best[key])
	}
	return result
}

func flowTraceKey(trace flowTrace) string {
	kinds := make([]string, 0, len(trace.SanitizedFor))
	for kind := range trace.SanitizedFor {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return trace.SourceKey + "|" + strings.Join(kinds, ",") + "|" + strings.Join(trace.CallStack, ">")
}

func betterFlowTrace(candidate, existing flowTrace) bool {
	if candidate.LowConfidence != existing.LowConfidence {
		return !candidate.LowConfidence
	}
	return len(candidate.Path) < len(existing.Path)
}

func betterFlowResult(candidate, existing FlowResult) bool {
	if candidate.Confidence != existing.Confidence {
		return candidate.Confidence == "medium"
	}
	return len(candidate.Path) < len(existing.Path)
}

func appendFlowStep(path []FlowStep, step FlowStep) []FlowStep {
	if len(path) > 0 {
		last := path[len(path)-1]
		if last.Kind == step.Kind && last.File == step.File && last.Line == step.Line && last.Detail == step.Detail {
			return path
		}
	}
	result := make([]FlowStep, len(path), len(path)+1)
	copy(result, path)
	return append(result, step)
}

func cloneFlowSet(values map[string]bool) map[string]bool {
	clone := make(map[string]bool, len(values)+1)
	for key, value := range values {
		clone[key] = value
	}
	return clone
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

func appendFlowFrame(stack []string, frame string) []string {
	result := make([]string, len(stack), len(stack)+1)
	copy(result, stack)
	return append(result, frame)
}

func cloneFlowFrames(stack []string) []string {
	result := make([]string, len(stack))
	copy(result, stack)
	return result
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

func uniqueFlowStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
