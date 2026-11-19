package cli

import "strings"

// hookShellRedirect is one redirection attached to a simple command.
type hookShellRedirect struct {
	op     string // ">", ">>", ">|", "&>", "&>>", "<", "<<", "<<-", "<<<"
	target string // file, heredoc delimiter, or here-string word
}

// hookShellCommand is one simple command of a shell command line.
type hookShellCommand struct {
	argv       []string
	redirects  []hookShellRedirect
	heredoc    string // body of the last heredoc, when any
	pipeIn     bool   // stdin comes from a preceding "|"
	pipeOut    bool   // stdout goes to a following "|"
	background bool   // ended with "&"
	depth      int    // subshell nesting depth
	dynamic    bool   // a word holds $VAR, $(...) or `...`
}

func (command hookShellCommand) name() string {
	if len(command.argv) == 0 {
		return ""
	}
	name := command.argv[0]
	if index := strings.LastIndexByte(name, '/'); index >= 0 {
		name = name[index+1:]
	}
	return name
}

func (command hookShellCommand) isOutputRedirect(redirect hookShellRedirect) bool {
	switch redirect.op {
	case ">", ">>", ">|", "&>", "&>>":
		return true
	}
	return false
}

// hookShellSplitter splits a command line into simple commands. It is a
// policy reader, not a shell: it keeps quoting, escapes, comments,
// operators, redirects and heredoc bodies apart, and reports false when
// the line cannot be read (unbalanced quotes, an unterminated heredoc).
type hookShellSplitter struct {
	src      string
	index    int
	commands []hookShellCommand
	current  hookShellCommand
	word     strings.Builder
	started  bool
	dynamic  bool
	depth    int
	pipeNext bool
	pending  string // redirect op waiting for its target word
	heredocs []hookPendingHeredoc
}

type hookPendingHeredoc struct {
	command   int // index into commands, or -1 for current
	delimiter string
	stripTabs bool
}

func splitHookShell(src string) ([]hookShellCommand, bool) {
	splitter := hookShellSplitter{src: src}
	splitter.current.depth = 0
	for splitter.index < len(src) {
		if !splitter.step() {
			return nil, false
		}
	}
	if !splitter.endWord() || splitter.pending != "" {
		return nil, false
	}
	splitter.endCommand("")
	if len(splitter.heredocs) > 0 {
		return nil, false
	}
	return splitter.commands, true
}

func (s *hookShellSplitter) step() bool {
	char := s.src[s.index]
	switch char {
	case ' ', '\t':
		s.index++
		return s.endWord()
	case '\\':
		if s.index+1 < len(s.src) && s.src[s.index+1] == '\n' {
			s.index += 2
			return true
		}
		if s.index+1 < len(s.src) {
			s.word.WriteByte(s.src[s.index+1])
			s.started = true
		}
		s.index += 2
		return true
	case '\'':
		end := strings.IndexByte(s.src[s.index+1:], '\'')
		if end < 0 {
			return false
		}
		s.word.WriteString(s.src[s.index+1 : s.index+1+end])
		s.started = true
		s.index += end + 2
		return true
	case '"':
		return s.doubleQuoted()
	case '`':
		end := strings.IndexByte(s.src[s.index+1:], '`')
		if end < 0 {
			return false
		}
		s.word.WriteString(s.src[s.index : s.index+end+2])
		s.started, s.dynamic = true, true
		s.index += end + 2
		return true
	case '$':
		return s.dollar()
	case '#':
		if s.started {
			s.word.WriteByte(char)
			s.index++
			return true
		}
		end := strings.IndexByte(s.src[s.index:], '\n')
		if end < 0 {
			s.index = len(s.src)
		} else {
			s.index += end
		}
		return true
	case '\n':
		s.index++
		if !s.endWord() {
			return false
		}
		s.endCommand(";")
		return s.readHeredocBodies()
	case ';':
		s.index++
		if !s.endWord() {
			return false
		}
		s.endCommand(";")
		return true
	case '|':
		s.index++
		if !s.endWord() {
			return false
		}
		if s.index < len(s.src) && s.src[s.index] == '|' {
			s.index++
			s.endCommand("||")
			return true
		}
		if s.index < len(s.src) && s.src[s.index] == '&' {
			s.index++
		}
		s.endCommand("|")
		return true
	case '&':
		if s.index+1 < len(s.src) && s.src[s.index+1] == '>' {
			return s.redirect()
		}
		s.index++
		if !s.endWord() {
			return false
		}
		if s.index < len(s.src) && s.src[s.index] == '&' {
			s.index++
			s.endCommand("&&")
			return true
		}
		s.endCommand("&")
		return true
	case '(':
		if s.started {
			s.word.WriteByte(char)
			s.index++
			return true
		}
		s.index++
		s.depth++
		s.current.depth = s.depth
		return true
	case ')':
		s.index++
		if !s.endWord() {
			return false
		}
		s.endCommand(";")
		if s.depth > 0 {
			s.depth--
		}
		s.current.depth = s.depth
		return true
	case '<', '>':
		return s.redirect()
	default:
		s.word.WriteByte(char)
		s.started = true
		s.index++
		return true
	}
}

func (s *hookShellSplitter) doubleQuoted() bool {
	s.index++
	s.started = true
	for s.index < len(s.src) {
		char := s.src[s.index]
		switch char {
		case '"':
			s.index++
			return true
		case '\\':
			if s.index+1 < len(s.src) {
				next := s.src[s.index+1]
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					s.word.WriteByte(next)
				} else if next != '\n' {
					s.word.WriteByte(char)
					s.word.WriteByte(next)
				}
				s.index += 2
				continue
			}
			s.index++
		case '$', '`':
			s.dynamic = true
			s.word.WriteByte(char)
			s.index++
		default:
			s.word.WriteByte(char)
			s.index++
		}
	}
	return false
}

func (s *hookShellSplitter) dollar() bool {
	s.started = true
	if s.index+1 < len(s.src) && s.src[s.index+1] == '(' {
		depth := 0
		for position := s.index + 1; position < len(s.src); position++ {
			switch s.src[position] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					s.word.WriteString(s.src[s.index : position+1])
					s.dynamic = true
					s.index = position + 1
					return true
				}
			}
		}
		return false
	}
	if s.index+1 < len(s.src) {
		next := s.src[s.index+1]
		if next == '{' || next == '_' || next == '@' || next == '*' || next == '?' || next == '#' ||
			(next >= 'A' && next <= 'Z') || (next >= 'a' && next <= 'z') || (next >= '0' && next <= '9') {
			s.dynamic = true
		}
	}
	s.word.WriteByte('$')
	s.index++
	return true
}

func (s *hookShellSplitter) redirect() bool {
	// A word made only of digits right before the operator is a file descriptor.
	if s.started && !s.dynamic && allHookDigits(s.word.String()) {
		s.word.Reset()
		s.started = false
	} else if !s.endWord() {
		return false
	}
	if s.pending != "" {
		return false
	}
	rest := s.src[s.index:]
	var op string
	switch {
	case strings.HasPrefix(rest, "&>>"):
		op = "&>>"
	case strings.HasPrefix(rest, "&>"):
		op = "&>"
	case strings.HasPrefix(rest, "<<<"):
		op = "<<<"
	case strings.HasPrefix(rest, "<<-"):
		op = "<<-"
	case strings.HasPrefix(rest, "<<"):
		op = "<<"
	case strings.HasPrefix(rest, "<&"), strings.HasPrefix(rest, "<>"):
		op = rest[:2]
	case strings.HasPrefix(rest, ">>"):
		op = ">>"
	case strings.HasPrefix(rest, ">|"):
		op = ">|"
	case strings.HasPrefix(rest, ">&"):
		op = ">&"
	case strings.HasPrefix(rest, "<"):
		op = "<"
	default:
		op = ">"
	}
	s.index += len(op)
	if op == ">&" || op == "<&" {
		// >&2, >&-: a descriptor copy, not a file. ">& file" writes the file.
		position := s.index
		for position < len(s.src) && s.src[position] == ' ' {
			position++
		}
		end := position
		for end < len(s.src) && (s.src[end] >= '0' && s.src[end] <= '9' || s.src[end] == '-') {
			end++
		}
		if end > position && (end == len(s.src) || strings.IndexByte(" \t\n;|&)", s.src[end]) >= 0) {
			s.index = end
			return true
		}
		if op == "<&" {
			return false
		}
		op = "&>"
	}
	if op == "<>" {
		op = ">"
	}
	s.pending = op
	return true
}

func (s *hookShellSplitter) endWord() bool {
	if !s.started {
		return true
	}
	word := s.word.String()
	s.word.Reset()
	s.started = false
	if s.dynamic {
		s.current.dynamic = true
	}
	s.dynamic = false
	if s.pending != "" {
		redirect := hookShellRedirect{op: s.pending, target: word}
		s.pending = ""
		if redirect.op == "<<" || redirect.op == "<<-" {
			s.heredocs = append(s.heredocs, hookPendingHeredoc{
				command:   -1,
				delimiter: word,
				stripTabs: redirect.op == "<<-",
			})
		}
		s.current.redirects = append(s.current.redirects, redirect)
		return true
	}
	s.current.argv = append(s.current.argv, word)
	return true
}

func (s *hookShellSplitter) endCommand(separator string) {
	command := s.current
	command.pipeIn = s.pipeNext
	command.pipeOut = separator == "|"
	command.background = separator == "&"
	s.pipeNext = separator == "|"
	if len(command.argv) > 0 || len(command.redirects) > 0 {
		for index := range s.heredocs {
			if s.heredocs[index].command == -1 {
				s.heredocs[index].command = len(s.commands)
			}
		}
		s.commands = append(s.commands, command)
	} else if separator != "|" {
		s.pipeNext = false
	}
	s.current = hookShellCommand{depth: s.depth}
}

func (s *hookShellSplitter) readHeredocBodies() bool {
	for len(s.heredocs) > 0 {
		heredoc := s.heredocs[0]
		var body strings.Builder
		found := false
		for s.index < len(s.src) {
			end := strings.IndexByte(s.src[s.index:], '\n')
			var line string
			if end < 0 {
				line = s.src[s.index:]
				s.index = len(s.src)
			} else {
				line = s.src[s.index : s.index+end]
				s.index += end + 1
			}
			check := line
			if heredoc.stripTabs {
				check = strings.TrimLeft(check, "\t")
			}
			if check == heredoc.delimiter {
				found = true
				break
			}
			body.WriteString(line)
			body.WriteByte('\n')
		}
		if !found {
			return false
		}
		if heredoc.command >= 0 && heredoc.command < len(s.commands) {
			s.commands[heredoc.command].heredoc = body.String()
		}
		s.heredocs = s.heredocs[1:]
	}
	return true
}
