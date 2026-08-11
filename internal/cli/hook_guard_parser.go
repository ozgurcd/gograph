package cli

import (
	"regexp"
	"strings"
	"unicode"
)

type hookSearchTool uint8

const (
	hookToolGrep hookSearchTool = iota
	hookToolRipgrep
)

type hookPatternMode uint8

const (
	hookPatternBRE hookPatternMode = iota
	hookPatternERE
	hookPatternFixed
)

type hookSearchInvocation struct {
	tool            hookSearchTool
	mode            hookPatternMode
	patterns        []string
	paths           []string
	inputPaths      []string
	selectors       []hookSelector
	recursive       bool
	globInsensitive bool
	explicitPattern bool
}

type hookSelectorKind uint8

const (
	hookSelectorIncludeGlob hookSelectorKind = iota
	hookSelectorExcludeGlob
	hookSelectorIncludeType
	hookSelectorExcludeType
)

type hookSelector struct {
	kind            hookSelectorKind
	value           string
	caseInsensitive bool
}

type hookDecision struct {
	block   bool
	symbols []string
}

var hookIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{2,}$`)
var hookGoValuePattern = regexp.MustCompile(`\.go(?:$|[,}])`)

var hookCommentMarkers = map[string]struct{}{
	"bug":        {},
	"deprecated": {},
	"fixme":      {},
	"hack":       {},
	"note":       {},
	"todo":       {},
	"xxx":        {},
}

var hookGoKeywords = map[string]struct{}{
	"break": {}, "case": {}, "chan": {}, "const": {}, "continue": {},
	"default": {}, "defer": {}, "else": {}, "fallthrough": {}, "for": {},
	"func": {}, "go": {}, "goto": {}, "if": {}, "import": {},
	"interface": {}, "map": {}, "package": {}, "range": {}, "return": {},
	"select": {}, "struct": {}, "switch": {}, "type": {}, "var": {},
}

func classifyHookCommand(command string) hookDecision {
	invocation, ok := parseHookCommand(command)
	if !ok {
		return hookDecision{}
	}
	return classifyHookSearchInvocation(invocation)
}

func parseHookCommand(command string) (hookSearchInvocation, bool) {
	argv, inputPaths, ok := lexHookCommand(command)
	if !ok {
		return hookSearchInvocation{}, false
	}

	invocation, ok := parseHookSearchInvocation(argv)
	if !ok {
		return hookSearchInvocation{}, false
	}
	invocation.inputPaths = inputPaths
	return invocation, true
}

func classifyHookSearchInvocation(invocation hookSearchInvocation) hookDecision {
	if !hookSearchesGo(invocation) {
		return hookDecision{}
	}

	seen := make(map[string]struct{})
	var symbols []string
	for _, pattern := range invocation.patterns {
		patternSymbols, pure := pureHookSymbolAlternatives(pattern, invocation.mode)
		if !pure {
			return hookDecision{}
		}
		for _, symbol := range patternSymbols {
			if hookSymbolExempt(symbol) {
				continue
			}
			if _, exists := seen[symbol]; exists {
				continue
			}
			seen[symbol] = struct{}{}
			symbols = append(symbols, symbol)
		}
	}
	if len(symbols) == 0 {
		return hookDecision{}
	}
	return hookDecision{block: true, symbols: symbols}
}

// lexHookCommand cooks the arguments of the first simple shell command. It is
// deliberately limited: unsupported expansion or malformed quoting fails open
// because the hook is workflow steering, not a shell security boundary.
func lexHookCommand(command string) ([]string, []string, bool) {
	lexer := hookShellLexer{command: command}
	for lexer.index < len(command) {
		var step hookLexStep
		switch {
		case lexer.inSingle:
			step = lexer.consumeSingleQuoted()
		case lexer.inDouble:
			step = lexer.consumeDoubleQuoted()
		default:
			step = lexer.consumeUnquoted()
		}
		if step == hookLexInvalid {
			return nil, nil, false
		}
		if step == hookLexStop {
			return lexer.argv, lexer.inputPaths, len(lexer.argv) > 0
		}
		lexer.index++
	}
	return lexer.finish()
}

type hookLexStep uint8

const (
	hookLexContinue hookLexStep = iota
	hookLexStop
	hookLexInvalid
)

type hookShellLexer struct {
	command        string
	index          int
	argv           []string
	inputPaths     []string
	word           strings.Builder
	inSingle       bool
	inDouble       bool
	started        bool
	redirectTarget hookRedirectTarget
	recordInput    bool
}

type hookRedirectTarget uint8

const (
	hookRedirectNone hookRedirectTarget = iota
	hookRedirectOutput
	hookRedirectInput
)

func (lexer *hookShellLexer) consumeSingleQuoted() hookLexStep {
	char := lexer.command[lexer.index]
	if char == '\'' {
		lexer.inSingle = false
		return hookLexContinue
	}
	lexer.append(char)
	return hookLexContinue
}

func (lexer *hookShellLexer) consumeDoubleQuoted() hookLexStep {
	char := lexer.command[lexer.index]
	switch char {
	case '"':
		lexer.inDouble = false
	case '$', '`':
		return hookLexInvalid
	case '\\':
		return lexer.consumeDoubleQuotedEscape()
	default:
		lexer.append(char)
	}
	return hookLexContinue
}

func (lexer *hookShellLexer) consumeDoubleQuotedEscape() hookLexStep {
	if lexer.index+1 >= len(lexer.command) {
		return hookLexInvalid
	}
	next := lexer.command[lexer.index+1]
	switch next {
	case '$', '`', '"', '\\':
		lexer.append(next)
		lexer.index++
	case '\n':
		lexer.index++
	default:
		lexer.append('\\')
	}
	return hookLexContinue
}

func (lexer *hookShellLexer) consumeUnquoted() hookLexStep {
	char := lexer.command[lexer.index]
	switch char {
	case '\'', '"':
		lexer.started = true
		lexer.inSingle = char == '\''
		lexer.inDouble = char == '"'
	case '\\':
		return lexer.consumeUnquotedEscape()
	case '$', '`', '(', ')':
		return hookLexInvalid
	case '<':
		return lexer.consumeInputRedirect()
	case '>':
		return lexer.consumeOutputRedirect()
	case '\n':
		if len(lexer.argv) == 0 && !lexer.started && lexer.redirectTarget == hookRedirectNone {
			return hookLexContinue
		}
		return lexer.stopCommand()
	case '|', '&', ';':
		return lexer.stopCommand()
	default:
		return lexer.consumePlain(char)
	}
	return hookLexContinue
}

func (lexer *hookShellLexer) stopCommand() hookLexStep {
	lexer.emit()
	if lexer.redirectTarget != hookRedirectNone {
		return hookLexInvalid
	}
	return hookLexStop
}

func (lexer *hookShellLexer) consumeUnquotedEscape() hookLexStep {
	if lexer.index+1 >= len(lexer.command) {
		return hookLexInvalid
	}
	next := lexer.command[lexer.index+1]
	if next != '\n' {
		lexer.append(next)
	}
	lexer.index++
	return hookLexContinue
}

func (lexer *hookShellLexer) consumeOutputRedirect() hookLexStep {
	if lexer.redirectTarget != hookRedirectNone {
		return hookLexInvalid
	}
	if lexer.started && allHookDigits(lexer.word.String()) {
		lexer.discardWord()
	} else {
		lexer.emit()
	}
	if lexer.index+1 < len(lexer.command) && lexer.command[lexer.index+1] == '>' {
		lexer.index++
	}
	if lexer.index+1 < len(lexer.command) && (lexer.command[lexer.index+1] == '&' || lexer.command[lexer.index+1] == '|') {
		lexer.index++
	}
	lexer.redirectTarget = hookRedirectOutput
	return hookLexContinue
}

func (lexer *hookShellLexer) consumeInputRedirect() hookLexStep {
	if lexer.redirectTarget != hookRedirectNone {
		return hookLexInvalid
	}
	if lexer.index+1 < len(lexer.command) {
		next := lexer.command[lexer.index+1]
		if next == '<' || next == '&' || next == '>' {
			return hookLexInvalid
		}
	}
	lexer.recordInput = true
	if lexer.started && allHookDigits(lexer.word.String()) {
		lexer.recordInput = allHookZeros(lexer.word.String())
		lexer.discardWord()
	} else {
		lexer.emit()
	}
	lexer.redirectTarget = hookRedirectInput
	return hookLexContinue
}

func (lexer *hookShellLexer) consumePlain(char byte) hookLexStep {
	if unicode.IsSpace(rune(char)) {
		lexer.emit()
		return hookLexContinue
	}
	if char == '#' && !lexer.started {
		return lexer.consumeComment()
	}
	lexer.append(char)
	return hookLexContinue
}

func (lexer *hookShellLexer) consumeComment() hookLexStep {
	if len(lexer.argv) > 0 || lexer.redirectTarget != hookRedirectNone {
		return hookLexStop
	}
	for lexer.index+1 < len(lexer.command) && lexer.command[lexer.index+1] != '\n' {
		lexer.index++
	}
	return hookLexContinue
}

func (lexer *hookShellLexer) append(char byte) {
	lexer.word.WriteByte(char)
	lexer.started = true
}

func (lexer *hookShellLexer) emit() {
	if !lexer.started {
		return
	}
	switch lexer.redirectTarget {
	case hookRedirectInput:
		if lexer.recordInput {
			lexer.inputPaths = append(lexer.inputPaths, lexer.word.String())
		}
		lexer.recordInput = false
		lexer.redirectTarget = hookRedirectNone
	case hookRedirectOutput:
		lexer.redirectTarget = hookRedirectNone
	default:
		lexer.argv = append(lexer.argv, lexer.word.String())
	}
	lexer.discardWord()
}

func (lexer *hookShellLexer) discardWord() {
	lexer.word.Reset()
	lexer.started = false
}

func (lexer *hookShellLexer) finish() ([]string, []string, bool) {
	if lexer.inSingle || lexer.inDouble {
		return nil, nil, false
	}
	lexer.emit()
	if lexer.redirectTarget != hookRedirectNone {
		return nil, nil, false
	}
	return lexer.argv, lexer.inputPaths, len(lexer.argv) > 0
}

func allHookDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func allHookZeros(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char != '0' {
			return false
		}
	}
	return true
}

func parseHookSearchInvocation(argv []string) (hookSearchInvocation, bool) {
	if len(argv) < 2 {
		return hookSearchInvocation{}, false
	}

	invocation := hookSearchInvocation{}
	switch argv[0] {
	case "grep":
		invocation.tool = hookToolGrep
		invocation.mode = hookPatternBRE
	case "rg":
		invocation.tool = hookToolRipgrep
		invocation.mode = hookPatternERE
	default:
		return hookSearchInvocation{}, false
	}

	var operands []string
	options := true
	for index := 1; index < len(argv); index++ {
		argument := argv[index]
		if options && argument == "--" {
			options = false
			continue
		}
		if options && strings.HasPrefix(argument, "--") {
			if !parseHookLongOption(&invocation, argument, argv, &index) {
				return hookSearchInvocation{}, false
			}
			continue
		}
		if options && strings.HasPrefix(argument, "-") && argument != "-" {
			if !parseHookShortOptions(&invocation, argument, argv, &index) {
				return hookSearchInvocation{}, false
			}
			continue
		}
		operands = append(operands, argument)
	}

	if invocation.explicitPattern {
		invocation.paths = operands
	} else {
		if len(operands) == 0 {
			return hookSearchInvocation{}, false
		}
		invocation.patterns = append(invocation.patterns, operands[0])
		invocation.paths = operands[1:]
	}
	return invocation, len(invocation.patterns) > 0
}

type hookOptionKind uint8

const (
	hookOptionUnknown hookOptionKind = iota
	hookOptionTerminal
	hookOptionFailOpen
	hookOptionFlag
	hookOptionOptionalValue
	hookOptionValue
	hookOptionDirectories
	hookOptionPattern
	hookOptionPatternFile
	hookOptionInclude
	hookOptionInsensitiveInclude
	hookOptionExclude
	hookOptionIncludeType
	hookOptionExcludeType
	hookOptionBRE
	hookOptionERE
	hookOptionFixed
	hookOptionRecursive
	hookOptionGlobInsensitive
	hookOptionGlobSensitive
)

// The option inventory is intentionally fail-open for classification: an
// unknown option makes the steering hook allow the command rather than guess
// its operand arity. Keep these tables aligned with supported grep/rg releases.
var hookCommonLongOptions = map[string]hookOptionKind{
	"after-context":  hookOptionValue,
	"before-context": hookOptionValue,
	"context":        hookOptionValue,
	"file":           hookOptionPatternFile,
	"fixed-strings":  hookOptionFixed,
	"help":           hookOptionTerminal,
	"max-count":      hookOptionValue,
	"regexp":         hookOptionPattern,
	"version":        hookOptionTerminal,
}

var hookGrepLongOptions = map[string]hookOptionKind{
	"basic-regexp":          hookOptionBRE,
	"binary":                hookOptionFlag,
	"binary-files":          hookOptionValue,
	"byte-offset":           hookOptionFlag,
	"count":                 hookOptionFlag,
	"color":                 hookOptionOptionalValue,
	"colour":                hookOptionOptionalValue,
	"dereference-recursive": hookOptionRecursive,
	"devices":               hookOptionValue,
	"directories":           hookOptionDirectories,
	"exclude":               hookOptionExclude,
	"exclude-dir":           hookOptionValue,
	"exclude-from":          hookOptionPatternFile,
	"extended-regexp":       hookOptionERE,
	"files-with-matches":    hookOptionFlag,
	"files-without-match":   hookOptionFlag,
	"group-separator":       hookOptionValue,
	"ignore-case":           hookOptionFlag,
	"include":               hookOptionInclude,
	"initial-tab":           hookOptionFlag,
	"invert-match":          hookOptionFlag,
	"label":                 hookOptionValue,
	"line-buffered":         hookOptionFlag,
	"line-number":           hookOptionFlag,
	"line-regexp":           hookOptionFlag,
	"no-filename":           hookOptionFlag,
	"no-group-separator":    hookOptionFlag,
	"no-ignore-case":        hookOptionFlag,
	"no-messages":           hookOptionFlag,
	"null":                  hookOptionFlag,
	"null-data":             hookOptionFlag,
	"only-matching":         hookOptionFlag,
	"perl-regexp":           hookOptionERE,
	"quiet":                 hookOptionFlag,
	"recursive":             hookOptionRecursive,
	"silent":                hookOptionFlag,
	"text":                  hookOptionFlag,
	"with-filename":         hookOptionFlag,
	"word-regexp":           hookOptionFlag,
}

var hookRipgrepLongOptions = map[string]hookOptionKind{
	"auto-hybrid-regex":            hookOptionFlag,
	"binary":                       hookOptionFlag,
	"block-buffered":               hookOptionFlag,
	"byte-offset":                  hookOptionFlag,
	"case-sensitive":               hookOptionFlag,
	"column":                       hookOptionFlag,
	"color":                        hookOptionValue,
	"context-separator":            hookOptionValue,
	"count":                        hookOptionFlag,
	"count-matches":                hookOptionFlag,
	"crlf":                         hookOptionFlag,
	"debug":                        hookOptionFlag,
	"dfa-size-limit":               hookOptionValue,
	"encoding":                     hookOptionValue,
	"engine":                       hookOptionValue,
	"field-context-separator":      hookOptionValue,
	"field-match-separator":        hookOptionValue,
	"files":                        hookOptionTerminal,
	"files-with-matches":           hookOptionFlag,
	"files-without-match":          hookOptionFlag,
	"follow":                       hookOptionFlag,
	"generate":                     hookOptionTerminal,
	"glob":                         hookOptionInclude,
	"glob-case-insensitive":        hookOptionGlobInsensitive,
	"heading":                      hookOptionFlag,
	"hidden":                       hookOptionFlag,
	"hostname-bin":                 hookOptionValue,
	"hyperlink-format":             hookOptionValue,
	"iglob":                        hookOptionInsensitiveInclude,
	"ignore-case":                  hookOptionFlag,
	"ignore-file":                  hookOptionValue,
	"ignore-file-case-insensitive": hookOptionFlag,
	"include-zero":                 hookOptionFlag,
	"invert-match":                 hookOptionFlag,
	"json":                         hookOptionFlag,
	"line-number":                  hookOptionFlag,
	"line-regexp":                  hookOptionFlag,
	"line-buffered":                hookOptionFlag,
	"max-columns":                  hookOptionValue,
	"max-columns-preview":          hookOptionFlag,
	"max-depth":                    hookOptionValue,
	"max-filesize":                 hookOptionValue,
	"messages":                     hookOptionFlag,
	"mmap":                         hookOptionFlag,
	"multiline":                    hookOptionFlag,
	"multiline-dotall":             hookOptionFlag,
	"no-config":                    hookOptionFlag,
	"no-context-separator":         hookOptionFlag,
	"no-auto-hybrid-regex":         hookOptionFlag,
	"no-fixed-strings":             hookOptionERE,
	"no-filename":                  hookOptionFlag,
	"no-glob-case-insensitive":     hookOptionGlobSensitive,
	"no-heading":                   hookOptionFlag,
	"no-ignore":                    hookOptionFlag,
	"no-ignore-dot":                hookOptionFlag,
	"no-ignore-exclude":            hookOptionFlag,
	"no-ignore-files":              hookOptionFlag,
	"no-ignore-global":             hookOptionFlag,
	"no-ignore-messages":           hookOptionFlag,
	"no-ignore-parent":             hookOptionFlag,
	"no-ignore-vcs":                hookOptionFlag,
	"no-line-number":               hookOptionFlag,
	"no-messages":                  hookOptionFlag,
	"no-mmap":                      hookOptionFlag,
	"no-pcre2":                     hookOptionFlag,
	"no-pcre2-unicode":             hookOptionFlag,
	"no-require-git":               hookOptionFlag,
	"no-unicode":                   hookOptionFlag,
	"null":                         hookOptionFlag,
	"null-data":                    hookOptionFlag,
	"one-file-system":              hookOptionFlag,
	"only-matching":                hookOptionFlag,
	"passthru":                     hookOptionFlag,
	"passthrough":                  hookOptionFlag,
	"path-separator":               hookOptionValue,
	"pcre2":                        hookOptionFlag,
	"pcre2-unicode":                hookOptionFlag,
	"pcre2-version":                hookOptionTerminal,
	"pre":                          hookOptionValue,
	"pre-glob":                     hookOptionValue,
	"pretty":                       hookOptionFlag,
	"quiet":                        hookOptionFlag,
	"regex-size-limit":             hookOptionValue,
	"replace":                      hookOptionValue,
	"search-zip":                   hookOptionFlag,
	"smart-case":                   hookOptionFlag,
	"sort":                         hookOptionValue,
	"sort-files":                   hookOptionFlag,
	"sortr":                        hookOptionValue,
	"stats":                        hookOptionFlag,
	"stop-on-nonmatch":             hookOptionFlag,
	"text":                         hookOptionFlag,
	"threads":                      hookOptionValue,
	"trace":                        hookOptionFlag,
	"trim":                         hookOptionFlag,
	"type":                         hookOptionIncludeType,
	"type-add":                     hookOptionFailOpen,
	"type-clear":                   hookOptionFailOpen,
	"type-not":                     hookOptionExcludeType,
	"type-list":                    hookOptionTerminal,
	"unicode":                      hookOptionFlag,
	"unrestricted":                 hookOptionFlag,
	"vimgrep":                      hookOptionFlag,
	"with-filename":                hookOptionFlag,
	"word-regexp":                  hookOptionFlag,
	"colors":                       hookOptionValue,
}

var hookGrepShortOptions = map[byte]hookOptionKind{
	'A': hookOptionValue, 'B': hookOptionValue, 'C': hookOptionValue,
	'D': hookOptionValue, 'd': hookOptionDirectories, 'm': hookOptionValue,
	'E': hookOptionERE, 'P': hookOptionERE, 'F': hookOptionFixed, 'G': hookOptionBRE,
	'R': hookOptionRecursive, 'r': hookOptionRecursive,
	'e': hookOptionPattern, 'f': hookOptionPatternFile,
	'V': hookOptionTerminal,
}

var hookRipgrepShortOptions = map[byte]hookOptionKind{
	'A': hookOptionValue, 'B': hookOptionValue, 'C': hookOptionValue,
	'E': hookOptionValue, 'M': hookOptionValue, 'd': hookOptionValue,
	'j': hookOptionValue, 'm': hookOptionValue, 'r': hookOptionValue,
	'F': hookOptionFixed, 'P': hookOptionFlag,
	'e': hookOptionPattern, 'f': hookOptionPatternFile,
	'g': hookOptionInclude, 't': hookOptionIncludeType, 'T': hookOptionExcludeType,
	'h': hookOptionTerminal, 'V': hookOptionTerminal,
}

func parseHookLongOption(invocation *hookSearchInvocation, argument string, argv []string, index *int) bool {
	nameValue := strings.TrimPrefix(argument, "--")
	name, value, attached := strings.Cut(nameValue, "=")
	kind := hookLongOptionKind(invocation.tool, name)
	if kind == hookOptionUnknown {
		return false
	}
	if kind == hookOptionPatternFile || kind == hookOptionTerminal || kind == hookOptionFailOpen {
		return false
	}
	if !hookOptionNeedsValue(kind) {
		if attached && kind != hookOptionOptionalValue {
			return false
		}
		return applyHookFlagOption(invocation, kind)
	}
	if !attached {
		if *index+1 >= len(argv) {
			return false
		}
		(*index)++
		value = argv[*index]
	}
	return applyHookValueOption(invocation, kind, value)
}

func hookOptionNeedsValue(kind hookOptionKind) bool {
	switch kind {
	case hookOptionValue, hookOptionDirectories, hookOptionPattern, hookOptionInclude, hookOptionInsensitiveInclude, hookOptionExclude,
		hookOptionIncludeType, hookOptionExcludeType:
		return true
	default:
		return false
	}
}

func applyHookFlagOption(invocation *hookSearchInvocation, kind hookOptionKind) bool {
	switch kind {
	case hookOptionFlag, hookOptionOptionalValue:
		return true
	case hookOptionBRE:
		invocation.mode = hookPatternBRE
	case hookOptionERE:
		invocation.mode = hookPatternERE
	case hookOptionFixed:
		invocation.mode = hookPatternFixed
	case hookOptionRecursive:
		invocation.recursive = true
	case hookOptionGlobInsensitive:
		invocation.globInsensitive = true
	case hookOptionGlobSensitive:
		invocation.globInsensitive = false
	default:
		return false
	}
	return true
}

func applyHookValueOption(invocation *hookSearchInvocation, kind hookOptionKind, value string) bool {
	switch kind {
	case hookOptionValue:
		return true
	case hookOptionDirectories:
		invocation.recursive = strings.EqualFold(value, "recurse")
	case hookOptionPattern:
		invocation.patterns = append(invocation.patterns, value)
		invocation.explicitPattern = true
	case hookOptionInclude:
		addHookInclude(invocation, value, false)
	case hookOptionInsensitiveInclude:
		addHookInclude(invocation, value, true)
	case hookOptionExclude:
		invocation.selectors = append(invocation.selectors, hookSelector{kind: hookSelectorExcludeGlob, value: value})
	case hookOptionIncludeType:
		invocation.selectors = append(invocation.selectors, hookSelector{kind: hookSelectorIncludeType, value: value})
	case hookOptionExcludeType:
		invocation.selectors = append(invocation.selectors, hookSelector{kind: hookSelectorExcludeType, value: value})
	default:
		return false
	}
	return true
}

func hookLongOptionKind(tool hookSearchTool, name string) hookOptionKind {
	if kind, exists := hookCommonLongOptions[name]; exists {
		return kind
	}
	if tool == hookToolGrep {
		return hookGrepLongOptions[name]
	}
	return hookRipgrepLongOptions[name]
}

func parseHookShortOptions(invocation *hookSearchInvocation, argument string, argv []string, index *int) bool {
	cluster := strings.TrimPrefix(argument, "-")
	optionKinds := hookRipgrepShortOptions
	flagOptions := ".0abchiHiIlLnNopqSsUuvwxz"
	if invocation.tool == hookToolGrep {
		optionKinds = hookGrepShortOptions
		flagOptions = "0123456789abcHhIiJLlMnOopqSsTUuvwXxyZz"
	}
	for position := 0; position < len(cluster); position++ {
		option := cluster[position]
		remainder := cluster[position+1:]
		kind, special := optionKinds[option]
		if !special {
			if !strings.ContainsRune(flagOptions, rune(option)) {
				return false
			}
			continue
		}
		if kind == hookOptionPatternFile || kind == hookOptionTerminal || kind == hookOptionFailOpen {
			return false
		}
		if !hookOptionNeedsValue(kind) {
			if !applyHookFlagOption(invocation, kind) {
				return false
			}
			continue
		}
		value, ok := hookShortOptionValue(remainder, argv, index)
		if !ok {
			return false
		}
		return applyHookValueOption(invocation, kind, value)
	}
	return true
}

func hookShortOptionValue(remainder string, argv []string, index *int) (string, bool) {
	if remainder != "" {
		return remainder, true
	}
	if *index+1 >= len(argv) {
		return "", false
	}
	(*index)++
	return argv[*index], true
}

func addHookInclude(invocation *hookSearchInvocation, value string, caseInsensitive bool) {
	if invocation.tool == hookToolRipgrep && strings.HasPrefix(value, "!") {
		invocation.selectors = append(invocation.selectors, hookSelector{
			kind:            hookSelectorExcludeGlob,
			value:           strings.TrimPrefix(value, "!"),
			caseInsensitive: caseInsensitive,
		})
		return
	}
	invocation.selectors = append(invocation.selectors, hookSelector{
		kind:            hookSelectorIncludeGlob,
		value:           value,
		caseInsensitive: caseInsensitive,
	})
}

func hookSearchesGo(invocation hookSearchInvocation) bool {
	if len(invocation.inputPaths) > 0 {
		inputCanContainGo := hookPathCanContainGo(invocation.inputPaths[len(invocation.inputPaths)-1])
		if hookHasStdinPath(invocation.paths) && inputCanContainGo {
			return true
		}
		if len(invocation.paths) == 0 && (invocation.tool != hookToolGrep || !invocation.recursive) {
			return inputCanContainGo
		}
	}
	if !hookPathsCanContainGo(invocation) {
		return false
	}
	if invocation.tool == hookToolRipgrep && hookHasExplicitGoPath(invocation.paths) {
		return true
	}
	return hookSelectorsCanSearchGo(invocation)
}

func hookHasStdinPath(paths []string) bool {
	for _, path := range paths {
		if path == "-" {
			return true
		}
	}
	return false
}

func hookPathsCanContainGo(invocation hookSearchInvocation) bool {
	if len(invocation.paths) == 0 {
		return invocation.tool == hookToolRipgrep || invocation.recursive
	}
	for _, path := range invocation.paths {
		if hookPathCanContainGo(path) {
			return true
		}
	}
	return false
}

func hookPathCanContainGo(path string) bool {
	if path == "-" || hookExemptPath(path) {
		return false
	}
	if hookGoValue(path) {
		return true
	}
	return !hookExplicitNonGoValue(path)
}

func hookSelectorsCanSearchGo(invocation hookSearchInvocation) bool {
	if allowed, selected := hookGlobSelectorsCanSearchGo(invocation); selected {
		return allowed
	}
	if allowed, selected := hookTypeSelectorsCanSearchGo(invocation.selectors); selected {
		return allowed
	}
	return true
}

func hookGlobSelectorsCanSearchGo(invocation hookSearchInvocation) (bool, bool) {
	allowed := true
	positive := false
	relevant := false
	for _, selector := range invocation.selectors {
		caseInsensitive := invocation.globInsensitive || selector.caseInsensitive
		switch selector.kind {
		case hookSelectorIncludeGlob:
			if !positive {
				allowed = false
				positive = true
			}
			if hookGlobCanMatchGo(selector.value, caseInsensitive) {
				allowed = true
			}
			relevant = true
		case hookSelectorExcludeGlob:
			if hookExcludesEveryGoFile(selector.value, caseInsensitive) {
				allowed = false
				relevant = true
			}
		}
	}
	return allowed, relevant
}

func hookTypeSelectorsCanSearchGo(selectors []hookSelector) (bool, bool) {
	allowed := true
	positive := false
	relevant := false
	for _, selector := range selectors {
		switch selector.kind {
		case hookSelectorIncludeType:
			if !positive {
				allowed = false
				positive = true
			}
			if hookGoType(selector.value) {
				allowed = true
			}
			relevant = true
		case hookSelectorExcludeType:
			if hookGoType(selector.value) {
				allowed = false
				relevant = true
			}
		}
	}
	return allowed, relevant
}

func hookGoType(value string) bool {
	return strings.EqualFold(value, "go") || strings.EqualFold(value, "golang") || strings.EqualFold(value, "all")
}

func hookHasExplicitGoPath(paths []string) bool {
	for _, path := range paths {
		if hookGoValue(path) {
			return true
		}
	}
	return false
}

func hookExemptPath(value string) bool {
	normalized := strings.ReplaceAll(strings.ToLower(value), "\\", "/")
	for strings.HasPrefix(normalized, "./") {
		normalized = strings.TrimPrefix(normalized, "./")
	}
	normalized = strings.Trim(normalized, "/")
	for _, part := range strings.Split(normalized, "/") {
		switch part {
		case "docs", ".github", "testdata", "migrations":
			return true
		}
	}
	return false
}

func hookExplicitNonGoValue(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, extension := range []string{
		".go", ".yaml", ".yml", ".json", ".md", ".sh", ".toml", ".env", ".sql", ".txt",
		".html", ".css", ".js", ".ts", ".py", ".rb", ".java", ".c", ".cpp", ".h", ".rs", ".tmp",
	} {
		if strings.HasSuffix(lower, extension) {
			return true
		}
	}
	return false
}

func hookGoValue(value string) bool {
	return hookGoValuePattern.MatchString(value)
}

func hookGlobCanMatchGo(value string, caseInsensitive bool) bool {
	if caseInsensitive {
		value = strings.ToLower(value)
	}
	if hookGoValue(value) {
		return true
	}
	if hookGoValue(strings.ToLower(value)) {
		return false
	}
	return !hookExplicitNonGoValue(value)
}

func hookExcludesEveryGoFile(value string, caseInsensitive bool) bool {
	normalized := strings.TrimSpace(value)
	if caseInsensitive {
		normalized = strings.ToLower(normalized)
	}
	if normalized == "*.go" || normalized == "**/*.go" || normalized == "**.go" {
		return true
	}
	for _, prefix := range []string{"*.{", "**/*.{", "**.{"} {
		if strings.HasPrefix(normalized, prefix) && strings.HasSuffix(normalized, "}") {
			extensions := strings.Split(strings.TrimSuffix(strings.TrimPrefix(normalized, prefix), "}"), ",")
			for _, extension := range extensions {
				if extension == "go" {
					return true
				}
			}
		}
	}
	return false
}

func hookSymbolExempt(symbol string) bool {
	lower := strings.ToLower(symbol)
	if _, exists := hookCommentMarkers[lower]; exists {
		return true
	}
	_, keyword := hookGoKeywords[symbol]
	return keyword
}

func pureHookSymbolAlternatives(pattern string, mode hookPatternMode) ([]string, bool) {
	switch mode {
	case hookPatternFixed:
		if hookIdentifierPattern.MatchString(pattern) {
			return []string{pattern}, true
		}
		return nil, false
	case hookPatternBRE:
		return parseHookBREExpression(pattern)
	case hookPatternERE:
		return parseHookEREExpression(pattern)
	default:
		return nil, false
	}
}

func parseHookEREExpression(expression string) ([]string, bool) {
	for {
		inner, wrapped, valid := unwrapHookEREGroup(expression)
		if !valid {
			return nil, false
		}
		if !wrapped {
			break
		}
		expression = inner
	}

	parts, valid := splitHookEREAlternatives(expression)
	if !valid {
		return nil, false
	}
	if len(parts) == 1 {
		if hookIdentifierPattern.MatchString(expression) {
			return []string{expression}, true
		}
		return nil, false
	}
	return parseHookAlternativeParts(parts, parseHookEREExpression)
}

func unwrapHookEREGroup(expression string) (string, bool, bool) {
	if !strings.HasPrefix(expression, "(") {
		return expression, false, true
	}
	innerStart := 1
	if strings.HasPrefix(expression, "(?:") {
		innerStart = 3
	} else if strings.HasPrefix(expression, "(?") {
		return "", false, false
	}

	depth := 0
	for index := 0; index < len(expression); index++ {
		switch expression[index] {
		case '\\':
			if index+1 >= len(expression) {
				return "", false, false
			}
			index++
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return "", false, false
			}
			if depth == 0 {
				if index != len(expression)-1 {
					return expression, false, true
				}
				return expression[innerStart:index], true, true
			}
		}
	}
	return "", false, false
}

func splitHookEREAlternatives(expression string) ([]string, bool) {
	depth := 0
	start := 0
	parts := make([]string, 0, 2)
	for index := 0; index < len(expression); index++ {
		switch expression[index] {
		case '\\':
			if index+1 >= len(expression) {
				return nil, false
			}
			index++
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, false
			}
		case '|':
			if depth == 0 {
				parts = append(parts, expression[start:index])
				start = index + 1
			}
		}
	}
	if depth != 0 {
		return nil, false
	}
	parts = append(parts, expression[start:])
	return parts, true
}

func parseHookBREExpression(expression string) ([]string, bool) {
	for {
		inner, wrapped, valid := unwrapHookBREGroup(expression)
		if !valid {
			return nil, false
		}
		if !wrapped {
			break
		}
		expression = inner
	}

	parts, valid := splitHookBREAlternatives(expression)
	if !valid {
		return nil, false
	}
	if len(parts) == 1 {
		if hookIdentifierPattern.MatchString(expression) {
			return []string{expression}, true
		}
		return nil, false
	}
	return parseHookAlternativeParts(parts, parseHookBREExpression)
}

func unwrapHookBREGroup(expression string) (string, bool, bool) {
	if !strings.HasPrefix(expression, `\(`) {
		return expression, false, true
	}
	depth := 0
	for index := 0; index < len(expression); index++ {
		if expression[index] != '\\' || index+1 >= len(expression) {
			continue
		}
		next := expression[index+1]
		switch next {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return "", false, false
			}
			if depth == 0 {
				if index+2 != len(expression) {
					return expression, false, true
				}
				return expression[2:index], true, true
			}
		}
		index++
	}
	return "", false, false
}

func splitHookBREAlternatives(expression string) ([]string, bool) {
	depth := 0
	start := 0
	parts := make([]string, 0, 2)
	for index := 0; index < len(expression); index++ {
		if expression[index] != '\\' || index+1 >= len(expression) {
			continue
		}
		next := expression[index+1]
		switch next {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, false
			}
		case '|':
			if depth == 0 {
				parts = append(parts, expression[start:index])
				start = index + 2
			}
		}
		index++
	}
	if depth != 0 {
		return nil, false
	}
	parts = append(parts, expression[start:])
	return parts, true
}

func parseHookAlternativeParts(parts []string, parse func(string) ([]string, bool)) ([]string, bool) {
	var symbols []string
	for _, part := range parts {
		if part == "" {
			return nil, false
		}
		partSymbols, valid := parse(part)
		if !valid {
			return nil, false
		}
		symbols = append(symbols, partSymbols...)
	}
	return symbols, len(symbols) > 0
}
