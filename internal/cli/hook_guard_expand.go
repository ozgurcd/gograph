package cli

import (
	"path/filepath"
	"regexp"
	"strings"
)

// The shell rules judge simple commands. expandHookCommands rewrites the
// split command line so commands hidden behind shell syntax are judged too:
//   - leading keywords (do, then, if, while, !, time, {) and VAR=value
//     assignments are dropped, so `for ...; do grep ...` judges grep;
//   - `for v in words` records v, and a later "$v" / "${v}" word becomes
//     those words;
//   - `sh -c 'script'`, `eval words` and a shell reading a heredoc judge
//     the inner script;
//   - `xargs cmd` and `find ... -exec cmd {} +` judge cmd, with a stand-in
//     .go operand when the listed files can be Go source;
//   - python, perl and ruby programs (-c/-e or a heredoc) judge the shell
//     commands they run (subprocess, os.system, system, backticks).

var hookShellKeywords = map[string]bool{
	"do": true, "then": true, "else": true, "elif": true, "if": true, "while": true,
	"until": true, "!": true, "time": true, "{": true, "}": true, "done": true, "fi": true,
}

var hookAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

const hookExpandDepth = 4

func expandHookCommands(commands []hookShellCommand) []hookShellCommand {
	return expandHookCommandsAt(commands, map[string][]string{}, 0)
}

func expandHookCommandsAt(commands []hookShellCommand, loopVars map[string][]string, depth int) []hookShellCommand {
	var out []hookShellCommand
	for index, command := range commands {
		command.argv = hookStripShellPrefix(command.argv)
		if len(command.argv) == 0 {
			if len(command.redirects) > 0 {
				out = append(out, command)
			}
			continue
		}
		if command.argv[0] == "for" {
			if len(command.argv) >= 3 && command.argv[2] == "in" {
				loopVars[command.argv[1]] = append([]string(nil), command.argv[3:]...)
			}
			continue
		}
		command.argv = hookSubstituteLoopVars(command.argv, loopVars)
		if depth < hookExpandDepth {
			if inner, ok := hookInnerScript(command); ok {
				if nested, split := splitHookShell(inner); split {
					out = append(out, expandHookCommandsAt(nested, loopVars, depth+1)...)
				} else {
					out = append(out, hookQuoteBlindSplit(inner)...)
				}
				continue
			}
			if programs := hookProgramShellCalls(command); len(programs) > 0 {
				for _, program := range programs {
					if nested, split := splitHookShell(program); split {
						out = append(out, expandHookCommandsAt(nested, loopVars, depth+1)...)
					}
				}
				continue
			}
		}
		var upstream *hookShellCommand
		if command.pipeIn && index > 0 {
			upstream = &commands[index-1]
		}
		switch command.name() {
		case "xargs":
			if wrapped, ok := hookXargsCommand(command, upstream); ok {
				out = append(out, wrapped)
			}
			continue
		case "find":
			out = append(out, command)
			out = append(out, hookFindExecCommands(command)...)
			continue
		}
		out = append(out, command)
	}
	return out
}

func hookStripShellPrefix(argv []string) []string {
	for len(argv) > 0 && (hookShellKeywords[argv[0]] || hookAssignment.MatchString(argv[0])) {
		argv = argv[1:]
	}
	if len(argv) > 0 && (argv[0] == "env" || argv[0] == "command" || argv[0] == "builtin" || argv[0] == "nice") {
		argv = argv[1:]
		for len(argv) > 0 && (hookAssignment.MatchString(argv[0]) || strings.HasPrefix(argv[0], "-")) {
			argv = argv[1:]
		}
	}
	return argv
}

func hookSubstituteLoopVars(argv []string, loopVars map[string][]string) []string {
	if len(loopVars) == 0 {
		return argv
	}
	var out []string
	for _, word := range argv {
		name := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(word, "${"), "$"), "}")
		if values, ok := loopVars[name]; ok && (word == "$"+name || word == "${"+name+"}") {
			out = append(out, values...)
			continue
		}
		out = append(out, word)
	}
	return out
}

var hookShells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}

// hookInnerScript returns the script of `sh -c script`, `eval words`, or a
// shell that reads its program from a heredoc.
func hookInnerScript(command hookShellCommand) (string, bool) {
	argv := command.argv
	name := command.name()
	if name == "eval" {
		return strings.Join(argv[1:], " "), len(argv) > 1
	}
	if !hookShells[name] {
		return "", false
	}
	for index := 1; index < len(argv); index++ {
		argument := argv[index]
		if !strings.HasPrefix(argument, "-") || argument == "-" {
			if argument == "-" {
				continue
			}
			return "", false // a script file operand
		}
		if strings.Contains(strings.TrimLeft(argument, "-"), "c") && !strings.HasPrefix(argument, "--") {
			if index+1 < len(argv) {
				return argv[index+1], true
			}
			return "", false
		}
	}
	if command.heredoc != "" {
		return command.heredoc, true
	}
	return "", false
}

// hookQuoteBlindSplit reads a script the splitter rejects (an unbalanced
// quote) by splitting on ; | & and newlines and dropping quote characters,
// so a malformed line cannot hide a command.
func hookQuoteBlindSplit(script string) []hookShellCommand {
	var commands []hookShellCommand
	fields := strings.FieldsFunc(script, func(r rune) bool { return r == ';' || r == '|' || r == '&' || r == '\n' })
	for _, field := range fields {
		words := strings.Fields(strings.NewReplacer(`"`, "", `'`, "", "`", "").Replace(field))
		words = hookStripShellPrefix(words)
		if len(words) > 0 {
			commands = append(commands, hookShellCommand{argv: words})
		}
	}
	return commands
}

// hookStandInGo is the operand that stands for files a lister passes on.
const hookStandInGo = "hook-listed-file.go"

var hookFileListers = map[string]bool{"find": true, "fd": true, "ls": true, "rg": true, "git": true}

// hookListsGo reports whether upstream lists file names that can be Go
// source: a lister with a .go selector, or with no name filter at all.
func hookListsGo(upstream *hookShellCommand) bool {
	if upstream == nil || !hookFileListers[upstream.name()] {
		return false
	}
	argv := upstream.argv
	switch upstream.name() {
	case "rg":
		if !hookContains(argv, "--files") {
			return false
		}
	case "git":
		if len(argv) < 2 || argv[1] != "ls-files" {
			return false
		}
	}
	filtered := false
	for index, argument := range argv[1:] {
		if strings.Contains(argument, ".go") {
			return true
		}
		switch argument {
		case "-name", "-iname", "-path", "-g", "--glob", "-e", "--extension", "-t", "--type":
			filtered = true
			if index+2 < len(argv) && strings.Contains(argv[index+2], "go") {
				return true
			}
		}
	}
	return !filtered
}

func hookContains(argv []string, word string) bool {
	for _, argument := range argv {
		if argument == word {
			return true
		}
	}
	return false
}

// hookXargsCommand returns the command xargs runs. Its file operands come
// from upstream, so a stand-in .go operand is added when upstream lists Go
// files; otherwise the command is not a search of the tree.
func hookXargsCommand(command hookShellCommand, upstream *hookShellCommand) (hookShellCommand, bool) {
	argv := command.argv[1:]
	for len(argv) > 0 && strings.HasPrefix(argv[0], "-") {
		option := argv[0]
		argv = argv[1:]
		switch option {
		case "-n", "-I", "-P", "-L", "-d", "-E", "-s", "-a", "--max-args", "--max-procs", "--delimiter", "--arg-file":
			if len(argv) > 0 {
				argv = argv[1:]
			}
		}
	}
	if len(argv) == 0 || !hookListsGo(upstream) {
		return hookShellCommand{}, false
	}
	wrapped := command
	wrapped.argv = append(append([]string(nil), argv...), hookStandInGo)
	wrapped.pipeIn = false
	return wrapped, true
}

// hookFindExecCommands returns the commands find -exec/-execdir/-ok run,
// with {} replaced by a stand-in .go file under the first start directory
// when the find can match Go files.
func hookFindExecCommands(command hookShellCommand) []hookShellCommand {
	argv := command.argv
	start := "."
	if len(argv) > 1 && !strings.HasPrefix(argv[1], "-") && argv[1] != "(" && argv[1] != "!" {
		start = argv[1]
	}
	matchesGo := hookListsGo(&command)
	var out []hookShellCommand
	for index := 1; index < len(argv); index++ {
		switch argv[index] {
		case "-exec", "-execdir", "-ok", "-okdir":
		default:
			continue
		}
		var inner []string
		index++
		for ; index < len(argv) && argv[index] != ";" && argv[index] != "+" && argv[index] != `\;`; index++ {
			word := argv[index]
			if word == "{}" {
				if !matchesGo {
					inner = nil
					break
				}
				word = filepath.Join(start, hookStandInGo)
			}
			inner = append(inner, word)
		}
		if len(inner) > 0 {
			out = append(out, hookShellCommand{argv: inner, depth: command.depth})
		}
	}
	return out
}

var (
	hookPythonShellCall = regexp.MustCompile(`(?:subprocess\.(?:run|call|check_call|check_output|Popen|getoutput|getstatusoutput)|os\.(?:system|popen))\(\s*(\[[^\]]*\]|"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')`)
	hookScriptShellCall = regexp.MustCompile("`([^`]*)`|qx[({]([^)}]*)[)}]|(?:system|exec)\\s*\\(?\\s*(?:\"((?:[^\"\\\\]|\\\\.)*)\"|'((?:[^'\\\\]|\\\\.)*)')")
	hookQuotedItem      = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'`)
)

// hookProgramShellCalls returns the shell commands a python, perl or ruby
// program runs, when the program is given inline (-c/-e) or as a heredoc.
func hookProgramShellCalls(command hookShellCommand) []string {
	name := command.name()
	python := strings.HasPrefix(name, "python")
	if !python && name != "perl" && name != "ruby" {
		return nil
	}
	program := command.heredoc
	for index := 1; index < len(command.argv); index++ {
		argument := command.argv[index]
		if (python && argument == "-c" || !python && (argument == "-e" || argument == "-E")) && index+1 < len(command.argv) {
			program = command.argv[index+1]
			break
		}
	}
	if program == "" {
		return nil
	}
	var calls []string
	if python {
		for _, match := range hookPythonShellCall.FindAllStringSubmatch(program, -1) {
			argument := match[1]
			if strings.HasPrefix(argument, "[") {
				var words []string
				for _, item := range hookQuotedItem.FindAllStringSubmatch(argument, -1) {
					words = append(words, hookShellQuote(item[1]+item[2]))
				}
				calls = append(calls, strings.Join(words, " "))
				continue
			}
			calls = append(calls, argument[1:len(argument)-1])
		}
		return calls
	}
	for _, match := range hookScriptShellCall.FindAllStringSubmatch(program, -1) {
		for _, group := range match[1:] {
			if group != "" {
				calls = append(calls, group)
				break
			}
		}
	}
	return calls
}
