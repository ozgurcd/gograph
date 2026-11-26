package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ozgurcd/gograph/internal/rootfind"
	"github.com/ozgurcd/gograph/internal/session"
)

// Opt-in gograph-first rules for hook-guard. Each rule has its own flag and
// --strict enables all of them. Inside a gograph root (a module root with a
// .gograph index) they deny, besides symbol searches:
//   - --deny-go-writes: text tools used as a .go editor (sed -i, perl -i,
//     awk -i inplace, and a redirect or tee into a .go file fed by a text
//     tool such as echo, printf, cat or a heredoc), unless the command
//     carries a reason comment; moves, copies and program output
//     (go run ./gen > x.go) stay allowed;
//   - --deny-region-reads: region reads of working-tree .go files (cat,
//     head, tail, less, more, nl, sed, awk, perl -n), unless only counted
//     or hashed;
//   - --require-reason: text searches over Go source that carry no reason
//     comment ("# gograph-cannot: <reason>");
//   - --require-session: edits of .go files while no gograph session is open.
// Outside every gograph root the call is allowed. An unknown working
// directory (cd into $VAR, cd -, popd, a cd in a pipe, subshell or
// background job) keeps the blocking judgement.

var hookReasonPattern = regexp.MustCompile(`#\s*gograph-cannot:\s*\S`)

const hookStrictPrefix = "gograph-guard: blocked"

var hookRegionReaders = map[string]bool{
	"cat": true, "head": true, "tail": true, "less": true, "more": true,
	"nl": true, "bat": true, "sed": true, "awk": true, "gawk": true, "perl": true,
}

var hookCountSinks = map[string]bool{
	"wc": true, "md5": true, "md5sum": true, "shasum": true, "sha1sum": true,
	"sha256sum": true, "sha512sum": true, "cksum": true, "b2sum": true,
}

// hookTextWriters are the tools whose output into a .go file is a hand edit
// (--deny-go-writes). Program output (go run, generators) is not.
var hookTextWriters = map[string]bool{
	"echo": true, "printf": true, "cat": true, "sed": true, "awk": true, "gawk": true,
	"perl": true, "tr": true, "cut": true, "sort": true, "uniq": true, "head": true,
	"tail": true, "nl": true, "paste": true, "rev": true, "column": true, "fmt": true,
}

// hookGitEmitsSource are the git subcommands whose output is file content.
var hookGitEmitsSource = map[string]bool{"show": true, "diff": true, "cat-file": true}

var hookGoEmitters = map[string]bool{
	"cat": true, "git": true, "sort": true, "uniq": true, "head": true,
	"tail": true, "sed": true, "awk": true, "cut": true, "tr": true, "nl": true,
	"strings": true,
}

// hookGuardOptions selects the opt-in rules and how a denial is delivered.
type hookGuardOptions struct {
	denyGoWrites     bool
	denyRegionReads  bool
	requireReason    bool
	requireSession   bool
	ask              bool // ask the user instead of denying, where the agent can
	allowRegionReads bool // the agent has no file-read tool (Codex)
}

func (options hookGuardOptions) shellRules() bool {
	return options.denyGoWrites || options.denyRegionReads || options.requireReason
}

type hookStrictContext struct {
	dir      string
	dirKnown bool
}

func (ctx hookStrictContext) resolve(path string) (string, bool) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), true
	}
	if !ctx.dirKnown {
		return "", false
	}
	return filepath.Join(ctx.dir, path), true
}

// insideRoot reports whether path is inside a gograph root. An unresolvable
// path (unknown directory) counts as inside.
func (ctx hookStrictContext) insideRoot(path string) bool {
	resolved, ok := ctx.resolve(path)
	if !ok {
		return true
	}
	start := resolved
	if hookGoValue(path) || strings.ContainsAny(filepath.Base(path), "*?[") {
		start = filepath.Dir(resolved)
	}
	_, inside := rootfind.FindRootFrom(start)
	return inside
}

func (ctx hookStrictContext) dirInsideRoot() bool {
	if !ctx.dirKnown {
		return true
	}
	_, inside := rootfind.FindRootFrom(ctx.dir)
	return inside
}

// evaluateHookStrictShell returns a deny reason, or "" to allow.
func evaluateHookStrictShell(command, cwd string, options hookGuardOptions) string {
	commands, ok := splitHookShell(command)
	if !ok {
		commands = hookQuoteBlindSplit(command)
	}
	commands = expandHookCommands(commands)
	hasReason := hookReasonPattern.MatchString(command)
	ctx := hookStrictContext{dir: cwd, dirKnown: cwd != ""}
	for index, current := range commands {
		if next, handled := hookStrictChangeDir(ctx, current); handled {
			ctx = next
			continue
		}
		local := ctx
		argv := current.argv
		if current.name() == "git" {
			local, argv = hookStrictGitDir(ctx, argv)
		}
		if options.denyGoWrites && !hasReason {
			var upstream string
			if current.pipeIn && index > 0 {
				upstream = commands[index-1].name()
			}
			if reason := hookStrictWrite(local, current, argv, upstream); reason != "" {
				return reason
			}
		}
		piped := index+1 < len(commands) && current.pipeOut
		var sink string
		if piped {
			sink = commands[index+1].name()
		}
		if options.denyRegionReads && !options.allowRegionReads && !hasReason {
			if reason := hookStrictRegionRead(local, current, argv, sink); reason != "" {
				return reason
			}
		}
		upstreamGo := current.pipeIn && index > 0 && hookEmitsGo(commands[index-1])
		if reason := hookStrictSearch(command, local, current, argv, upstreamGo, sink, hasReason || !options.requireReason); reason != "" {
			return reason
		}
	}
	return ""
}

// hookStrictChangeDir applies cd/pushd/popd to the working directory.
func hookStrictChangeDir(ctx hookStrictContext, command hookShellCommand) (hookStrictContext, bool) {
	switch command.name() {
	case "cd", "pushd":
	case "popd":
		return hookStrictContext{}, true
	default:
		return ctx, false
	}
	if command.pipeIn || command.pipeOut || command.background || command.depth > 0 || command.dynamic {
		return hookStrictContext{}, true
	}
	var target string
	for _, argument := range command.argv[1:] {
		if strings.HasPrefix(argument, "-") && argument != "-" {
			continue
		}
		target = argument
		break
	}
	switch target {
	case "":
		home, err := os.UserHomeDir()
		if err != nil {
			return hookStrictContext{}, true
		}
		target = home
	case "-":
		return hookStrictContext{}, true
	}
	if strings.HasPrefix(target, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return hookStrictContext{}, true
		}
		target = filepath.Join(home, target[2:])
	}
	resolved, ok := ctx.resolve(target)
	if !ok {
		return hookStrictContext{}, true
	}
	real, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return hookStrictContext{}, true // a failed cd
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return hookStrictContext{}, true
	}
	return hookStrictContext{dir: real, dirKnown: true}, true
}

// hookStrictGitDir applies `git -C <dir>` and strips global options.
func hookStrictGitDir(ctx hookStrictContext, argv []string) (hookStrictContext, []string) {
	index := 1
	for index < len(argv) {
		argument := argv[index]
		if argument == "-C" && index+1 < len(argv) {
			resolved, ok := ctx.resolve(argv[index+1])
			if ok {
				ctx = hookStrictContext{dir: resolved, dirKnown: true}
			} else {
				ctx = hookStrictContext{}
			}
			index += 2
			continue
		}
		if argument == "-c" && index+1 < len(argv) {
			index += 2
			continue
		}
		if strings.HasPrefix(argument, "-") {
			index++
			continue
		}
		break
	}
	return ctx, append([]string{"git"}, argv[index:]...)
}

func hookIsGoPath(word string) bool {
	return strings.HasSuffix(word, ".go") && !strings.Contains(word, ":")
}

func hookGoOperands(argv []string) []string {
	var paths []string
	for _, argument := range argv[1:] {
		if strings.HasPrefix(argument, "-") {
			if index := strings.IndexByte(argument, '='); index >= 0 && hookIsGoPath(argument[index+1:]) {
				continue // --include=*.go is a selector, not a file operand
			}
			continue
		}
		if hookIsGoPath(argument) {
			paths = append(paths, argument)
		}
	}
	return paths
}

// hookStrictWrite denies a text tool used as a .go editor: an in-place
// sed/perl/awk edit, or a redirect or tee into a .go file whose content comes
// from a text tool (a bare redirect counts). upstream names the command
// piping into this one.
func hookStrictWrite(ctx hookStrictContext, command hookShellCommand, argv []string, upstream string) string {
	name := command.name()
	if name == "" || hookTextWriters[name] {
		for _, redirect := range command.redirects {
			if command.isOutputRedirect(redirect) && hookIsGoPath(redirect.target) && ctx.insideRoot(redirect.target) {
				return hookStrictWriteMessage(redirect.target, "an output redirect from "+hookWriterName(name))
			}
		}
	}
	if len(argv) == 0 {
		return ""
	}
	var targets []string
	switch name {
	case "sed":
		if hookHasInPlaceFlag(argv, false) {
			targets = hookGoOperands(argv)
		}
	case "perl":
		if hookHasInPlaceFlag(argv, true) {
			targets = hookGoOperands(argv)
		}
	case "awk", "gawk":
		if hookAwkInPlace(argv) {
			targets = hookGoOperands(argv)
		}
	case "tee":
		if upstream == "" || hookTextWriters[upstream] {
			targets = hookGoOperands(argv)
		}
	}
	for _, target := range targets {
		if ctx.insideRoot(target) {
			return hookStrictWriteMessage(target, name)
		}
	}
	return ""
}

func hookWriterName(name string) string {
	if name == "" {
		return "the shell"
	}
	return name
}

// hookAwkInPlace reports gawk's "-i inplace" / "--include=inplace".
func hookAwkInPlace(argv []string) bool {
	for index, argument := range argv[1:] {
		switch {
		case (argument == "-i" || argument == "--include") && index+2 < len(argv) && strings.HasPrefix(argv[index+2], "inplace"):
			return true
		case strings.HasPrefix(argument, "--include=inplace") || strings.HasPrefix(argument, "-iinplace"):
			return true
		}
	}
	return false
}

func hookHasInPlaceFlag(argv []string, perl bool) bool {
	for _, argument := range argv[1:] {
		if argument == "--in-place" || strings.HasPrefix(argument, "--in-place=") {
			return true
		}
		if !strings.HasPrefix(argument, "-") || strings.HasPrefix(argument, "--") {
			continue
		}
		cluster := argument[1:]
		if perl {
			// -i, -pi, -pi.bak, -i.orig; stop at -e/-E whose value follows.
			for _, option := range cluster {
				if option == 'i' {
					return true
				}
				if option == 'e' || option == 'E' || option == 'M' || option == 'I' {
					break
				}
			}
			continue
		}
		if strings.HasPrefix(cluster, "i") || strings.Contains(strings.SplitN(cluster, "e", 2)[0], "i") {
			return true
		}
	}
	return false
}

func hookStrictWriteMessage(target, via string) string {
	return fmt.Sprintf(`%s .go write — %s writes %s.
  Edit Go source with the agent's edit tool, not a text tool.
  Moves, copies and program output (go run ./gen > x.go) are allowed.
  If a text tool is needed (for example one rename across many files),
  state why in the command itself:
    <command>  # gograph-cannot: <why the edit tool cannot do this>`, hookStrictPrefix, via, target)
}

func hookStrictRegionRead(ctx hookStrictContext, command hookShellCommand, argv []string, sink string) string {
	name := command.name()
	if !hookRegionReaders[name] || hookCountSinks[sink] {
		return ""
	}
	if (name == "sed" || name == "perl") && hookHasInPlaceFlag(argv, name == "perl") {
		return "" // a write, judged by hookStrictWrite
	}
	if name == "perl" && !hookPerlReadsLines(argv) {
		return ""
	}
	targets := hookGoOperands(argv)
	for _, redirect := range command.redirects {
		if redirect.op == "<" && hookIsGoPath(redirect.target) {
			targets = append(targets, redirect.target)
		}
	}
	for _, target := range targets {
		if ctx.insideRoot(target) {
			return fmt.Sprintf(`%s .go region read — %s reads %s.
  To find a declaration: gograph query <Symbol>
  For a declaration body: gograph source <pkg.Symbol>
  For context around a symbol: gograph context <Symbol>
  For an arbitrary region: the Read tool (the agent's file-read tool) with offset/limit.
  Counts and checksums (| wc, | sha256sum) and git show/diff stay allowed.
  If gograph and the Read tool cannot serve it, state why in the command:
    <command>  # gograph-cannot: <reason>`, hookStrictPrefix, name, target)
		}
	}
	return ""
}

func hookPerlReadsLines(argv []string) bool {
	for _, argument := range argv[1:] {
		if strings.HasPrefix(argument, "-") && !strings.HasPrefix(argument, "--") &&
			(strings.ContainsRune(argument, 'n') || strings.ContainsRune(argument, 'p')) {
			return true
		}
	}
	return false
}

func hookEmitsGo(command hookShellCommand) bool {
	name := command.name()
	if !hookGoEmitters[name] {
		return false
	}
	if name == "git" && (len(command.argv) < 2 || !hookGitEmitsSource[command.argv[1]]) {
		return false // git log, blame, status, ... print history, not source
	}
	for _, argument := range command.argv[1:] {
		if strings.HasSuffix(argument, ".go") {
			return true // includes git show rev:path.go
		}
	}
	for _, redirect := range command.redirects {
		if redirect.op == "<" && hookIsGoPath(redirect.target) {
			return true
		}
	}
	return false
}

func hookStrictSearch(command string, ctx hookStrictContext, current hookShellCommand, argv []string, upstreamGo bool, sink string, hasReason bool) string {
	searchArgv, recursive, ok := hookSearchArgv(current, argv)
	if !ok {
		return ""
	}
	invocation, ok := parseHookSearchInvocation(searchArgv)
	if !ok {
		return ""
	}
	if recursive {
		invocation.recursive = true
	}
	for _, redirect := range current.redirects {
		if redirect.op == "<" {
			invocation.inputPaths = append(invocation.inputPaths, redirect.target)
		}
	}
	if upstreamGo && len(invocation.paths) == 0 {
		invocation.inputPaths = append(invocation.inputPaths, "stdin.go")
	}
	if current.pipeIn && !upstreamGo && len(invocation.paths) == 0 && len(invocation.inputPaths) == 0 {
		return "" // reads the output of a command that does not print Go source
	}
	inside := ctx.dirInsideRoot()
	if ctx.dirKnown && !upstreamGo {
		inside = hookSearchesIndexedRepository(invocation, ctx.dir)
	}
	if !inside || hookIsCountSearch(searchArgv) || hookCountSinks[sink] {
		return ""
	}
	decision := classifyHookSearchInvocation(invocation)
	if decision.block {
		return hookSymbolSearchMessage(command, decision.symbols[0])
	}
	if hasReason || !hookSearchesGo(invocation) {
		return ""
	}
	return fmt.Sprintf(`%s text search over Go source without a reason.
  A text search over Go source must be preceded by a one-line statement of why gograph cannot answer it.
  First ask gograph (gograph query/context/callers/usages/strings/literals).
  If gograph cannot answer, state why in the command itself:
    <command>  # gograph-cannot: <reason>`, hookStrictPrefix)
}

// hookSearchArgv maps grep, rg, ag and git grep to a grep/rg argv.
func hookSearchArgv(current hookShellCommand, argv []string) ([]string, bool, bool) {
	if len(argv) == 0 {
		return nil, false, false
	}
	switch current.name() {
	case "grep", "egrep", "fgrep":
		out := append([]string{"grep"}, argv[1:]...)
		if current.name() == "egrep" {
			out = append([]string{"grep", "-E"}, argv[1:]...)
		} else if current.name() == "fgrep" {
			out = append([]string{"grep", "-F"}, argv[1:]...)
		}
		return out, false, true
	case "rg":
		return append([]string{"rg"}, argv[1:]...), false, true
	case "ag":
		out := []string{"rg"}
		for _, argument := range argv[1:] {
			if argument == "--go" {
				out = append(out, "-t", "go") // ag's file-type flag
				continue
			}
			out = append(out, argument)
		}
		return out, false, true
	case "git":
		if len(argv) >= 2 && argv[1] == "grep" {
			return append([]string{"grep"}, argv[2:]...), true, true
		}
	}
	return nil, false, false
}

func hookIsCountSearch(argv []string) bool {
	for _, argument := range argv[1:] {
		if argument == "--count" || argument == "--count-matches" || argument == "--files-with-matches" ||
			argument == "--files-without-match" {
			return true
		}
		if strings.HasPrefix(argument, "-") && !strings.HasPrefix(argument, "--") &&
			(strings.ContainsRune(argument, 'c') || strings.ContainsRune(argument, 'l') || strings.ContainsRune(argument, 'L')) {
			return true
		}
	}
	return false
}

// evaluateHookStrictEdit denies an edit of a .go file inside a gograph root
// while no gograph session is open there.
func evaluateHookStrictEdit(paths []string, cwd string) string {
	for _, path := range paths {
		if !hookIsGoPath(path) {
			continue
		}
		if !filepath.IsAbs(path) {
			if cwd == "" {
				continue
			}
			path = filepath.Join(cwd, path)
		}
		root, ok := rootfind.FindRootFrom(filepath.Dir(path))
		if !ok {
			continue
		}
		ids, err := session.ActiveCallerSessionIDs(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gograph-guard: warning — could not read sessions in %s (%v); allowing the edit\n", root, err)
			continue
		}
		if len(ids) == 0 {
			return fmt.Sprintf(`%s .go edit without a gograph session — %s.
  Open one first: gograph session create
  Then plan the change: gograph plan <Symbol> --with-context`, hookStrictPrefix, path)
		}
	}
	return ""
}

// hookApplyPatchPaths extracts the file paths an apply_patch body touches.
func hookApplyPatchPaths(patch string) []string {
	var paths []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
			if path, ok := strings.CutPrefix(line, prefix); ok {
				paths = append(paths, strings.TrimSpace(path))
			}
		}
	}
	return paths
}
