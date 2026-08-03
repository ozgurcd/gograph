package cli

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestEvaluateHookCommandRegexAlternation(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantExit   int
		wantSymbol string
	}{
		{
			name:       "grep BRE escaped alternation single quoted",
			command:    `grep -rn 'runHookGuard\|evaluateHookCommand' internal/cli/*.go`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "grep BRE escaped alternation double quoted",
			command:    `grep -rn "runHookGuard\|hookGuardInput" internal/cli/*.go`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "grep ERE bare alternation",
			command:    `grep -Ern 'runHookGuard|evaluateHookCommand' --include='*.go' .`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "grep long ERE flag bare alternation",
			command:    `grep --extended-regexp -rn 'evaluateHookCommand|runHookGuard' --include='*.go' .`,
			wantExit:   2,
			wantSymbol: "evaluateHookCommand",
		},
		{
			name:       "ripgrep bare alternation",
			command:    `rg 'runHookGuard|evaluateHookCommand' -g '*.go' internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "ripgrep grouped alternation",
			command:    `rg '(evaluateHookCommand|runHookGuard)' --glob '*.go' internal/cli`,
			wantExit:   2,
			wantSymbol: "evaluateHookCommand",
		},
		{
			name:       "ripgrep noncapturing grouped alternation",
			command:    `rg '(?:runHookGuard|evaluateHookCommand)' --glob='*.go' internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:     "grep BRE bare pipe is literal",
			command:  `grep -rn 'runHookGuard|evaluateHookCommand' internal/cli/*.go`,
			wantExit: 0,
		},
		{
			name:     "grep fixed string literal pipe",
			command:  `grep -Frn 'runHookGuard|evaluateHookCommand' internal/cli/*.go`,
			wantExit: 0,
		},
		{
			name:     "grep long fixed string escaped pipe",
			command:  `grep --fixed-strings -rn 'runHookGuard\|evaluateHookCommand' internal/cli/*.go`,
			wantExit: 0,
		},
		{
			name:     "ripgrep fixed string literal pipe",
			command:  `rg -F 'runHookGuard|evaluateHookCommand' -g '*.go' internal/cli`,
			wantExit: 0,
		},
		{
			name:     "ripgrep long fixed string literal pipe",
			command:  `rg --fixed-strings 'runHookGuard|evaluateHookCommand' --glob '*.go' internal/cli`,
			wantExit: 0,
		},
		{
			name:     "ripgrep escaped pipe is literal",
			command:  `rg 'runHookGuard\|evaluateHookCommand' -g '*.go' internal/cli`,
			wantExit: 0,
		},
		{
			name:     "grep BRE mixed symbol and non-symbol alternatives",
			command:  `grep -rn 'runHookGuard\|error:.*' internal/cli/*.go`,
			wantExit: 0,
		},
		{
			name:     "grep ERE mixed symbol and non-symbol alternatives",
			command:  `grep -Ern 'runHookGuard|error:.*' --include='*.go' .`,
			wantExit: 0,
		},
		{
			name:     "ripgrep mixed symbol and non-symbol alternatives",
			command:  `rg '(runHookGuard|error:.*)' -g '*.go' internal/cli`,
			wantExit: 0,
		},
		{
			name:     "ripgrep mixed regexp flags",
			command:  `rg -e runHookGuard -e 'error:.*' -g '*.go' internal/cli`,
			wantExit: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertHookGuardDecision(t, tt.command, tt.wantExit, tt.wantSymbol)
		})
	}
}

func TestEvaluateHookCommandPatternAndValueFlags(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantSymbol string
	}{
		{
			name:       "grep repeated short regexp flags",
			command:    `grep -rn -e runHookGuard -e evaluateHookCommand --include='*.go' .`,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "grep repeated long regexp flags",
			command:    `grep -rn --regexp=runHookGuard --regexp hookGuardInput --include='*.go' .`,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "ripgrep repeated short regexp flags",
			command:    `rg -e runHookGuard -e evaluateHookCommand -g '*.go' .`,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "ripgrep repeated long regexp flags",
			command:    `rg --regexp=evaluateHookCommand --regexp runHookGuard --glob '*.go' .`,
			wantSymbol: "evaluateHookCommand",
		},
		{
			name:       "ripgrep short glob before pattern",
			command:    `rg -g '*.go' runHookGuard .`,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "ripgrep short glob after pattern",
			command:    `rg runHookGuard -g '*.go' .`,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "ripgrep long glob before pattern",
			command:    `rg --glob '*.go' evaluateHookCommand .`,
			wantSymbol: "evaluateHookCommand",
		},
		{
			name:       "ripgrep long glob after pattern",
			command:    `rg evaluateHookCommand --glob='*.go' .`,
			wantSymbol: "evaluateHookCommand",
		},
		{
			name:       "grep separated context value",
			command:    `grep -rn -C 2 runHookGuard internal/cli/*.go`,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "grep long context value",
			command:    `grep -rn --context 2 evaluateHookCommand internal/cli/*.go`,
			wantSymbol: "evaluateHookCommand",
		},
		{
			name:       "ripgrep separated after-context value",
			command:    `rg -n -A 2 runHookGuard -g '*.go' .`,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "ripgrep inline before-context value",
			command:    `rg -n --before-context=2 evaluateHookCommand --glob '*.go' .`,
			wantSymbol: "evaluateHookCommand",
		},
		{
			name:       "ripgrep byte offset flag",
			command:    `rg -bn runHookGuard -g '*.go' internal/cli`,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "ripgrep attached max depth",
			command:    `rg -d2 evaluateHookCommand -g '*.go' .`,
			wantSymbol: "evaluateHookCommand",
		},
		{
			name:       "ripgrep separated max depth",
			command:    `rg -d 2 runHookGuard -g '*.go' .`,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "grep numeric context shorthand",
			command:    `grep -2n 'runHookGuard\|evaluateHookCommand' internal/cli/*.go`,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "ripgrep line-buffered pipeline search",
			command:    `rg --line-buffered 'runHookGuard|evaluateHookCommand' -g '*.go' internal/cli`,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "ripgrep pretty output alias",
			command:    `rg --pretty 'runHookGuard|evaluateHookCommand' -g '*.go' internal/cli`,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "ripgrep passthrough output alias",
			command:    `rg --passthrough 'runHookGuard|evaluateHookCommand' -g '*.go' internal/cli`,
			wantSymbol: "runHookGuard",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertHookGuardDecision(t, tt.command, 2, tt.wantSymbol)
		})
	}
}

func TestEvaluateHookCommandDistinguishesRegexPipesFromShellPipelines(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantExit   int
		wantSymbol string
	}{
		{
			name:       "BRE alternation followed by shell pipeline",
			command:    `grep -rn 'runHookGuard\|evaluateHookCommand' internal/cli/*.go | head -1`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "ERE alternation followed by shell pipeline",
			command:    `grep -Ern 'evaluateHookCommand|runHookGuard' internal/cli/*.go | sed -n '1p'`,
			wantExit:   2,
			wantSymbol: "evaluateHookCommand",
		},
		{
			name:       "identifier search followed by ripgrep pipeline",
			command:    `grep -rn runHookGuard internal/cli/*.go | rg evaluateHookCommand`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:     "fixed literal pipe followed by shell pipeline",
			command:  `grep -Frn 'runHookGuard|evaluateHookCommand' internal/cli/*.go | head -1`,
			wantExit: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertHookGuardDecision(t, tt.command, tt.wantExit, tt.wantSymbol)
		})
	}
}

func TestEvaluateHookCommandDirectCommandScope(t *testing.T) {
	// The hook is a workflow-steering aid, not a shell security boundary. Its
	// documented policy applies to direct grep/rg Bash commands; shell wrappers
	// and commands whose first program is not grep/rg remain out of scope.
	tests := []struct {
		name       string
		command    string
		wantExit   int
		wantSymbol string
	}{
		{
			name:       "direct grep",
			command:    `grep -rn runHookGuard internal/cli/*.go`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "direct ripgrep with leading whitespace",
			command:    `  rg evaluateHookCommand -g '*.go' .`,
			wantExit:   2,
			wantSymbol: "evaluateHookCommand",
		},
		{
			name:     "command wrapper is out of scope",
			command:  `command grep -rn runHookGuard internal/cli/*.go`,
			wantExit: 0,
		},
		{
			name:     "environment wrapper is out of scope",
			command:  `env rg runHookGuard -g '*.go' .`,
			wantExit: 0,
		},
		{
			name:     "shell wrapper is out of scope",
			command:  `sh -c "grep -rn runHookGuard internal/cli/*.go"`,
			wantExit: 0,
		},
		{
			name:     "grep later in pipeline is out of scope",
			command:  `printf '%s\n' runHookGuard | grep runHookGuard`,
			wantExit: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertHookGuardDecision(t, tt.command, tt.wantExit, tt.wantSymbol)
		})
	}
}

func TestEvaluateHookCommandFailOpenAndScope(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantExit   int
		wantSymbol string
	}{
		{
			name:     "grep ERE escaped pipe is literal",
			command:  `grep -Ern 'runHookGuard\|evaluateHookCommand' internal/cli/*.go`,
			wantExit: 0,
		},
		{
			name:       "shell escaped ripgrep alternation",
			command:    `rg runHookGuard\|evaluateHookCommand -g '*.go' internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "output redirection is not a non-Go target",
			command:    `rg runHookGuard -g '*.go' . > report.md`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:     "quoted greater-than remains regex text",
			command:  `rg 'runHookGuard>evaluateHookCommand' -g '*.go' .`,
			wantExit: 0,
		},
		{
			name:     "explicit non-Go file",
			command:  `rg runHookGuard README.md`,
			wantExit: 0,
		},
		{
			name:     "explicit exempt directory",
			command:  `rg runHookGuard .github/workflows`,
			wantExit: 0,
		},
		{
			name:       "mixed Go and non-Go globs still cover Go",
			command:    `rg runHookGuard -g '*.go' -g '*.md' .`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "mixed Go and non-Go paths still cover Go",
			command:    `rg evaluateHookCommand internal/cli README.md`,
			wantExit:   2,
			wantSymbol: "evaluateHookCommand",
		},
		{
			name:       "exempt path cannot poison a Go path",
			command:    `rg runHookGuard docs internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:     "glob excludes every Go file",
			command:  `rg runHookGuard -g '!*.go' .`,
			wantExit: 0,
		},
		{
			name:     "unknown option fails open",
			command:  `rg --future-option runHookGuard .`,
			wantExit: 0,
		},
		{
			name:     "pattern file fails open",
			command:  `rg -f patterns.txt .`,
			wantExit: 0,
		},
		{
			name:     "unclosed quote fails open",
			command:  `rg 'runHookGuard -g '*.go' .`,
			wantExit: 0,
		},
		{
			name:     "dynamic expansion fails open",
			command:  `rg "$PATTERN" -g '*.go' .`,
			wantExit: 0,
		},
		{
			name:     "non-recursive grep on stdin",
			command:  `grep runHookGuard`,
			wantExit: 0,
		},
		{
			name:       "recursive grep is broad",
			command:    `grep --recursive runHookGuard .`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "grep filename flag preserves BRE alternation",
			command:    `grep -Hrn 'runHookGuard\|evaluateHookCommand' internal/cli/*.go`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "grep directory recursion mode is broad",
			command:    `grep -d recurse 'runHookGuard\|evaluateHookCommand' .`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "grep exclude-dir does not exclude Go files",
			command:    `grep -r --exclude-dir='*.go' 'runHookGuard\|evaluateHookCommand' internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "ripgrep no-fixed-strings restores alternation",
			command:    `rg --no-fixed-strings 'runHookGuard|evaluateHookCommand' -g '*.go' .`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "exempt alternative cannot poison a symbol",
			command:    `rg 'TODO|runHookGuard' -g '*.go' internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "exempt repeated pattern cannot poison a symbol",
			command:    `rg -e TODO -e evaluateHookCommand -g '*.go' internal/cli`,
			wantExit:   2,
			wantSymbol: "evaluateHookCommand",
		},
		{
			name:     "comment marker remains allowed",
			command:  `rg TODO -g '*.go' .`,
			wantExit: 0,
		},
		{
			name:     "ripgrep files mode is not a pattern search",
			command:  `rg --files runHookGuard`,
			wantExit: 0,
		},
		{
			name:     "help mode is not a pattern search",
			command:  `rg --help runHookGuard`,
			wantExit: 0,
		},
		{
			name:     "Go keyword remains allowed",
			command:  `rg type -g '*.go' .`,
			wantExit: 0,
		},
		{
			name:       "fixed mode still blocks separate symbol patterns",
			command:    `rg -F -e runHookGuard -e evaluateHookCommand -g '*.go' .`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "attached grep regexp preserves BRE alternation",
			command:    `grep -rn --regexp='runHookGuard\|evaluateHookCommand' --include='*.go' .`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "grep input redirection exposes Go target",
			command:    `grep -n 'runHookGuard\|evaluateHookCommand' < internal/cli/hook_guard.go`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "ripgrep input redirection exposes Go target",
			command:    `rg 'evaluateHookCommand|runHookGuard' < internal/cli/hook_guard.go`,
			wantExit:   2,
			wantSymbol: "evaluateHookCommand",
		},
		{
			name:       "explicit stdin descriptor exposes Go target",
			command:    `grep -n 'runHookGuard\|evaluateHookCommand' 0<internal/cli/hook_guard.go`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "unrelated file descriptor does not replace recursive scope",
			command:    `grep -rl 'runHookGuard\|evaluateHookCommand' 3<README.md`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "stdin redirection does not replace recursive grep scope",
			command:    `grep -rl 'runHookGuard\|evaluateHookCommand' < README.md`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "stdin operand uses redirected Go target",
			command:    `grep -n 'runHookGuard\|evaluateHookCommand' - < internal/cli/hook_guard.go`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "mixed stdin operand uses redirected Go target",
			command:    `rg 'evaluateHookCommand|runHookGuard' README.md - < internal/cli/hook_guard.go`,
			wantExit:   2,
			wantSymbol: "evaluateHookCommand",
		},
		{
			name:       "leading blank line finds first command",
			command:    "\nrg 'runHookGuard|evaluateHookCommand' -g '*.go' internal/cli",
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "leading comment finds first command",
			command:    "# inspect symbols\nrg 'runHookGuard|evaluateHookCommand' -g '*.go' internal/cli",
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertHookGuardDecision(t, tt.command, tt.wantExit, tt.wantSymbol)
		})
	}
}

func TestEvaluateHookCommandModeAndSelectionOrdering(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantExit   int
		wantSymbol string
	}{
		{
			name:     "ripgrep PCRE engine keeps fixed-string mode",
			command:  `rg -F -P 'runHookGuard|evaluateHookCommand' -g '*.go' internal/cli`,
			wantExit: 0,
		},
		{
			name:     "ripgrep long PCRE engine keeps fixed-string mode",
			command:  `rg --pcre2 -F 'runHookGuard|evaluateHookCommand' -g '*.go' internal/cli`,
			wantExit: 0,
		},
		{
			name:     "ripgrep disabling PCRE keeps fixed-string mode",
			command:  `rg -F --no-pcre2 'runHookGuard|evaluateHookCommand' -g '*.go' internal/cli`,
			wantExit: 0,
		},
		{
			name:     "ripgrep color consumes its separate value",
			command:  `rg --color always 'error:.*' internal/cli/hook_guard.go`,
			wantExit: 0,
		},
		{
			name:       "grep color accepts an attached value",
			command:    `grep --color=always -rn 'runHookGuard\|evaluateHookCommand' internal/cli/*.go`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "later ripgrep Go include wins",
			command:    `rg 'runHookGuard|evaluateHookCommand' -g '!*.go' -g '*.go' internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:     "later ripgrep Go exclusion wins",
			command:  `rg 'runHookGuard|evaluateHookCommand' -g '*.go' -g '!*.go' internal/cli`,
			wantExit: 0,
		},
		{
			name:       "later ripgrep Go type include wins",
			command:    `rg 'runHookGuard|evaluateHookCommand' -Tgo -tgo internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:     "later ripgrep Go type exclusion wins",
			command:  `rg 'runHookGuard|evaluateHookCommand' -tgo -Tgo internal/cli`,
			wantExit: 0,
		},
		{
			name:       "ripgrep all type includes Go",
			command:    `rg 'runHookGuard|evaluateHookCommand' -tall internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:     "ripgrep all type excludes Go",
			command:  `rg 'runHookGuard|evaluateHookCommand' -Tall internal/cli`,
			wantExit: 0,
		},
		{
			name:       "later ripgrep all type re-includes Go",
			command:    `rg 'runHookGuard|evaluateHookCommand' -Tgo -tall internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:     "negated brace glob excludes Go",
			command:  `rg 'runHookGuard|evaluateHookCommand' -g '!*.{go,md}' internal/cli`,
			wantExit: 0,
		},
		{
			name:     "later negated brace glob excludes Go",
			command:  `rg 'runHookGuard|evaluateHookCommand' -g '*.go' -g '!*.{go,md}' internal/cli`,
			wantExit: 0,
		},
		{
			name:       "later Go include wins after brace exclusion",
			command:    `rg 'runHookGuard|evaluateHookCommand' -g '!*.{go,md}' -g '*.go' internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "case-sensitive uppercase exclusion keeps lowercase Go",
			command:    `rg 'runHookGuard|evaluateHookCommand' -g '!*.GO' internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "case-sensitive uppercase brace exclusion keeps lowercase Go",
			command:    `rg 'runHookGuard|evaluateHookCommand' -g '!*.{GO,md}' internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:     "case-insensitive iglob excludes Go",
			command:  `rg 'runHookGuard|evaluateHookCommand' --iglob '!*.GO' internal/cli`,
			wantExit: 0,
		},
		{
			name:     "global case-insensitive glob excludes Go",
			command:  `rg 'runHookGuard|evaluateHookCommand' --glob-case-insensitive -g '!*.GO' internal/cli`,
			wantExit: 0,
		},
		{
			name:       "explicit case-sensitive glob mode keeps Go",
			command:    `rg 'runHookGuard|evaluateHookCommand' --no-glob-case-insensitive -g '!*.GO' internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "later case-sensitive toggle wins",
			command:    `rg 'runHookGuard|evaluateHookCommand' --glob-case-insensitive -g '!*.GO' --no-glob-case-insensitive internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:     "case-sensitive uppercase include omits lowercase Go",
			command:  `rg 'runHookGuard|evaluateHookCommand' -g '*.GO' internal/cli`,
			wantExit: 0,
		},
		{
			name:       "later grep Go include wins",
			command:    `grep -r --exclude='*.go' --include='*.go' 'runHookGuard\|evaluateHookCommand' internal/cli`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:     "later grep Go exclusion wins",
			command:  `grep -r --include='*.go' --exclude='*.go' 'runHookGuard\|evaluateHookCommand' internal/cli`,
			wantExit: 0,
		},
		{
			name:     "grep exclusion file is dynamic",
			command:  `grep -r --exclude-from=patterns.txt 'runHookGuard\|evaluateHookCommand' internal/cli`,
			wantExit: 0,
		},
		{
			name:     "ripgrep custom type definition fails open",
			command:  `rg --type-add 'foo:*.go' -tfoo 'runHookGuard|evaluateHookCommand' internal/cli`,
			wantExit: 0,
		},
		{
			name:     "invalid ripgrep include option fails open",
			command:  `rg --include='*.go' 'runHookGuard|evaluateHookCommand' internal/cli`,
			wantExit: 0,
		},
		{
			name:       "Go glob under dot-prefixed directory",
			command:    `rg 'runHookGuard|evaluateHookCommand' .config/pkg/*.go`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:       "Go path containing non-Go extension text",
			command:    `rg 'runHookGuard|evaluateHookCommand' foo.corpus/hook_guard.md.go`,
			wantExit:   2,
			wantSymbol: "runHookGuard",
		},
		{
			name:     "Go-looking temporary file is non-Go",
			command:  `rg 'runHookGuard|evaluateHookCommand' hook_guard.go.tmp`,
			wantExit: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertHookGuardDecision(t, tt.command, tt.wantExit, tt.wantSymbol)
		})
	}
}

func TestClassifyHookCommandReturnsEveryAlternative(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    []string
	}{
		{
			name:    "basic grep preserves and deduplicates branches",
			command: `grep -rn 'runHookGuard\|evaluateHookCommand\|runHookGuard' internal/cli/*.go`,
			want:    []string{"runHookGuard", "evaluateHookCommand"},
		},
		{
			name:    "nested ripgrep alternatives preserve source order",
			command: `rg '(runHookGuard|(?:evaluateHookCommand|runHookGuard))' -g '*.go' internal/cli`,
			want:    []string{"runHookGuard", "evaluateHookCommand"},
		},
		{
			name:    "exempt branch is omitted without hiding symbol",
			command: `rg '(TODO|evaluateHookCommand)' -g '*.go' internal/cli`,
			want:    []string{"evaluateHookCommand"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := classifyHookCommand(tt.command)
			if !decision.block {
				t.Fatalf("classifyHookCommand(%q) did not block", tt.command)
			}
			if !slices.Equal(decision.symbols, tt.want) {
				t.Fatalf("classifyHookCommand(%q) symbols = %q, want %q", tt.command, decision.symbols, tt.want)
			}
		})
	}
}

func assertHookGuardDecision(t *testing.T, command string, wantExit int, wantSymbol string) {
	t.Helper()

	firstExit, firstOutput := evaluateHookCommandForTest(t, command)
	secondExit, secondOutput := evaluateHookCommandForTest(t, command)
	if firstExit != secondExit || firstOutput != secondOutput {
		t.Fatalf("evaluateHookCommand(%q) is not deterministic:\nfirst: exit=%d output=%q\nsecond: exit=%d output=%q",
			command, firstExit, firstOutput, secondExit, secondOutput)
	}
	if firstExit != wantExit {
		t.Fatalf("evaluateHookCommand(%q) exit = %d, want %d\noutput:\n%s", command, firstExit, wantExit, firstOutput)
	}

	if wantExit == 0 {
		if firstOutput != "" {
			t.Fatalf("evaluateHookCommand(%q) allowed command but wrote output:\n%s", command, firstOutput)
		}
		return
	}

	if !strings.Contains(firstOutput, "gograph-guard: blocked grep") {
		t.Fatalf("evaluateHookCommand(%q) did not explain the block:\n%s", command, firstOutput)
	}
	for _, tool := range []string{"query", "context", "callers", "impact"} {
		wantSuggestion := fmt.Sprintf(`gograph_%s %q`, tool, wantSymbol)
		if !strings.Contains(firstOutput, wantSuggestion) {
			t.Errorf("evaluateHookCommand(%q) output missing deterministic suggestion %q:\n%s", command, wantSuggestion, firstOutput)
		}
	}
}

func evaluateHookCommandForTest(t *testing.T, command string) (int, string) {
	t.Helper()

	var output bytes.Buffer
	exitCode := evaluateHookCommandTo(command, &output)
	return exitCode, output.String()
}
