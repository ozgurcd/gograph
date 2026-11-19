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

// Strict gograph-first policy (hook-guard --strict). Inside a gograph root
// (a module root with a .gograph index) it denies, besides symbol searches:
//   - shell writes to .go files (redirects, sed -i, perl -i, tee, cp, mv,
//     install, ln, dd of=, truncate): use the agent's edit tool;
//   - region reads of working-tree .go files (cat, head, tail, less, more,
//     nl, sed, awk, perl -n), unless only counted or hashed;
//   - text searches over Go source that carry no reason comment
//     ("# gograph-cannot: <reason>");
//   - edits of .go files while no gograph session is open.
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

var hookGoEmitters = map[string]bool{
	"cat": true, "git": true, "sort": true, "uniq": true, "head": true,
	"tail": true, "sed": true, "awk": true, "cut": true, "tr": true, "nl": true,
}

type hookStrictOptions struct {
	allowRegionReads bool // the agent has no file-read tool (Codex)
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
func evaluateHookStrictShell(command, cwd string, options hookStrictOptions) string {
	commands, ok := splitHookShell(command)
	if !ok {
		return ""
	}
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
		if reason := hookStrictWrite(local, current, argv); reason != "" {
			return reason
		}
		piped := index+1 < len(commands) && current.pipeOut
		var sink string
		if piped {
			sink = commands[index+1].name()
		}
		if !options.allowRegionReads {
			if reason := hookStrictRegionRead(local, current, argv, sink); reason != "" {
				return reason
			}
		}
		upstreamGo := current.pipeIn && index > 0 && hookEmitsGo(commands[index-1])
		if reason := hookStrictSearch(command, local, current, argv, upstreamGo, sink, hasReason); reason != "" {
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

func hookStrictWrite(ctx hookStrictContext, command hookShellCommand, argv []string) string {
	for _, redirect := range command.redirects {
		if command.isOutputRedirect(redirect) && hookIsGoPath(redirect.target) && ctx.insideRoot(redirect.target) {
			return hookStrictWriteMessage(redirect.target, "an output redirect")
		}
	}
	if len(argv) == 0 {
		return ""
	}
	name := command.name()
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
	case "tee", "truncate":
		targets = hookGoOperands(argv)
	case "cp", "mv", "install", "ln":
		operands := hookNonOptionOperands(argv)
		if len(operands) >= 2 && hookIsGoPath(operands[len(operands)-1]) {
			targets = operands[len(operands)-1:]
		}
	case "dd":
		for _, argument := range argv[1:] {
			if value, ok := strings.CutPrefix(argument, "of="); ok && hookIsGoPath(value) {
				targets = append(targets, value)
			}
		}
	}
	for _, target := range targets {
		if ctx.insideRoot(target) {
			return hookStrictWriteMessage(target, name)
		}
	}
	return ""
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

func hookNonOptionOperands(argv []string) []string {
	var operands []string
	options := true
	for _, argument := range argv[1:] {
		if options && argument == "--" {
			options = false
			continue
		}
		if options && strings.HasPrefix(argument, "-") && argument != "-" {
			continue
		}
		operands = append(operands, argument)
	}
	return operands
}

func hookStrictWriteMessage(target, via string) string {
	return fmt.Sprintf(`%s .go write — %s writes %s.
  Edit Go source with the agent's edit tool (Edit/Write, apply_patch,
  replace_file_content), not a shell writer.`, hookStrictPrefix, via, target)
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
  For a declaration body: gograph source <pkg.Symbol>
  For context around a symbol: gograph context <Symbol>
  For an arbitrary region: the agent's file-read tool with offset/limit.
  Counts and checksums (| wc, | sha256sum) and git show/diff stay allowed.`, hookStrictPrefix, name, target)
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
  First ask gograph (gograph query/context/callers/usages/strings/literals).
  If gograph cannot answer, state why in the command itself:
    <command>  # gograph-cannot: <why gograph cannot answer this>`, hookStrictPrefix)
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
	case "rg", "ag":
		return append([]string{"rg"}, argv[1:]...), false, true
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
