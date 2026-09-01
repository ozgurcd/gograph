package sqlquery

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	StatusExact   = "exact"
	StatusPartial = "partial"
	StatusUnknown = "unknown"

	AccessRead  = "read"
	AccessWrite = "write"
	AccessDDL   = "ddl"

	maxCTEClassificationDepth = 64
)

var supportedVerbs = map[string]string{
	"SELECT":   AccessRead,
	"INSERT":   AccessWrite,
	"UPDATE":   AccessWrite,
	"DELETE":   AccessWrite,
	"MERGE":    AccessWrite,
	"CREATE":   AccessDDL,
	"ALTER":    AccessDDL,
	"DROP":     AccessDDL,
	"TRUNCATE": AccessDDL,
}

type TableRef struct {
	Name   string `json:"name"`
	Access string `json:"access"`
}

type Classification struct {
	Verb   string
	Access string
	Status string
	Tables []TableRef
}

type token struct {
	text   string
	upper  string
	quoted bool
}

type tokenRange struct {
	start int
	end   int
}

// ClassifyPostgreSQL conservatively classifies one static PostgreSQL statement.
// It is an inventory classifier, not a syntax validator. Unsupported or
// incomplete constructs retain the operation when possible and report partial.
func ClassifyPostgreSQL(query string) Classification {
	tokens, complete := lex(query)
	return classifyPostgreSQLTokens(tokens, complete, 0)
}

func classifyPostgreSQLTokens(tokens []token, complete bool, cteDepth int) Classification {
	result := Classification{Status: StatusUnknown, Tables: []TableRef{}}
	if len(tokens) == 0 {
		return result
	}

	main, ctes, cteRanges, withComplete := mainStatement(tokens)
	if main < 0 || main >= len(tokens) {
		return result
	}
	verb := keyword(tokens[main])
	access, ok := supportedVerbs[verb]
	if !ok {
		return result
	}
	result.Verb = verb
	result.Access = access
	result.Status = StatusExact
	if !complete || !withComplete {
		result.Status = StatusPartial
	}

	depths, balanced := tokenDepths(tokens)
	if !balanced {
		result.Status = StatusPartial
	}
	if hasTrailingStatement(tokens, depths, main) {
		result.Status = StatusPartial
	}
	statementEnd := primaryStatementEnd(tokens, depths, main)
	tokens = tokens[:statementEnd]
	depths = depths[:statementEnd]
	refs := make([]TableRef, 0, 4)
	seen := make(map[string]bool)
	add := func(name, tableAccess string) {
		if name == "" || isCTE(name, ctes) {
			return
		}
		key := name + "\x00" + tableAccess
		if seen[key] {
			return
		}
		seen[key] = true
		refs = append(refs, TableRef{Name: name, Access: tableAccess})
	}

	writeKeyword := -1
	writeResolved := true
	switch verb {
	case "INSERT", "MERGE":
		writeKeyword = findKeywordAtDepth(tokens, depths, main+1, depths[main], "INTO")
		writeResolved = addRelationAfter(tokens, writeKeyword, AccessWrite, add)
	case "UPDATE":
		writeKeyword = main
		writeResolved = addRelationAfter(tokens, main, AccessWrite, add)
	case "DELETE":
		writeKeyword = findKeywordAtDepth(tokens, depths, main+1, depths[main], "FROM")
		writeResolved = addRelationAfter(tokens, writeKeyword, AccessWrite, add)
	case "CREATE":
		writeResolved = collectCreateTarget(tokens, depths, main, add)
	case "ALTER":
		writeResolved = collectAlterTarget(tokens, depths, main, add)
	case "DROP":
		writeResolved = collectDropTargets(tokens, depths, main, add)
	case "TRUNCATE":
		writeResolved = collectTruncateTargets(tokens, depths, main, add)
	}
	if !writeResolved {
		result.Status = StatusPartial
	}
	for _, cteRange := range cteRanges {
		if cteDepth >= maxCTEClassificationDepth {
			result.Status = StatusPartial
			continue
		}
		cte := classifyPostgreSQLTokens(tokens[cteRange.start:cteRange.end], true, cteDepth+1)
		if cte.Verb == "" {
			continue
		}
		if cte.Status != StatusExact {
			result.Status = StatusPartial
		}
		if cte.Access == AccessDDL || cte.Access == AccessWrite && result.Access == AccessRead {
			result.Access = cte.Access
		}
		for _, table := range cte.Tables {
			add(table.Name, table.Access)
		}
	}

	for i := main; i < len(tokens); i++ {
		switch keyword(tokens[i]) {
		case "FROM":
			if i == writeKeyword {
				continue
			}
			if !collectRelationList(tokens, depths, i, add) {
				result.Status = StatusPartial
			}
		case "JOIN":
			if !addRelationAfter(tokens, i, AccessRead, add) {
				result.Status = StatusPartial
			}
		case "USING":
			if verb == "DELETE" || verb == "MERGE" {
				if !addRelationAfter(tokens, i, AccessRead, add) {
					result.Status = StatusPartial
				}
			}
		}
	}

	result.Tables = refs
	return result
}

func primaryStatementEnd(tokens []token, depths []int, main int) int {
	if main < 0 || main >= len(tokens) || len(depths) != len(tokens) {
		return len(tokens)
	}
	depth := depths[main]
	for i := main + 1; i < len(tokens); i++ {
		if depths[i] == depth && tokens[i].text == ";" {
			return i
		}
	}
	return len(tokens)
}

func hasTrailingStatement(tokens []token, depths []int, main int) bool {
	end := primaryStatementEnd(tokens, depths, main)
	for i := end + 1; i < len(tokens); i++ {
		if tokens[i].text != ";" {
			return true
		}
	}
	return false
}

func NormalizeVerb(value string) (string, error) {
	verb := strings.ToUpper(strings.TrimSpace(value))
	if _, ok := supportedVerbs[verb]; !ok {
		return "", fmt.Errorf("unsupported PostgreSQL SQL verb %q; expected SELECT, INSERT, UPDATE, DELETE, MERGE, CREATE, ALTER, DROP, or TRUNCATE", value)
	}
	return verb, nil
}

func NormalizeAccess(value string) (string, error) {
	access := strings.ToLower(strings.TrimSpace(value))
	switch access {
	case AccessRead, AccessWrite, AccessDDL:
		return access, nil
	default:
		return "", fmt.Errorf("unsupported SQL access %q; expected read, write, or ddl", value)
	}
}

func NormalizeTableSelector(value string) (string, error) {
	tokens, complete := lex(strings.TrimSpace(value))
	if !complete {
		return "", fmt.Errorf("invalid PostgreSQL table selector %q", value)
	}
	name, next, ok := qualifiedName(tokens, 0)
	if !ok || next != len(tokens) {
		return "", fmt.Errorf("invalid PostgreSQL table selector %q; expected table or schema.table", value)
	}
	return name, nil
}

func TableMatches(table, selector string) bool {
	tableParts := splitQualified(table)
	selectorParts := splitQualified(selector)
	if len(tableParts) == 0 || len(selectorParts) == 0 {
		return false
	}
	if len(selectorParts) == 1 {
		return identifierEqual(tableParts[len(tableParts)-1], selectorParts[0])
	}
	if len(tableParts) != len(selectorParts) {
		return false
	}
	for i := range tableParts {
		if !identifierEqual(tableParts[i], selectorParts[i]) {
			return false
		}
	}
	return true
}

func mainStatement(tokens []token) (int, map[string]bool, []tokenRange, bool) {
	i := 0
	for i < len(tokens) && tokens[i].text == ";" {
		i++
	}
	if i >= len(tokens) {
		return -1, nil, nil, true
	}
	if keyword(tokens[i]) == "EXPLAIN" {
		i++
		if i < len(tokens) && tokens[i].text == "(" {
			end, ok := matchingParen(tokens, i)
			if !ok {
				return -1, nil, nil, false
			}
			i = end + 1
		}
		for i < len(tokens) {
			switch keyword(tokens[i]) {
			case "ANALYZE", "VERBOSE":
				i++
			default:
				goto explained
			}
		}
	}

explained:
	if i >= len(tokens) || keyword(tokens[i]) != "WITH" {
		return i, map[string]bool{}, nil, true
	}
	ctes := make(map[string]bool)
	cteRanges := make([]tokenRange, 0, 2)
	i++
	if i < len(tokens) && keyword(tokens[i]) == "RECURSIVE" {
		i++
	}
	for {
		name, next, ok := qualifiedName(tokens, i)
		if !ok || strings.Contains(name, ".") {
			return firstVerb(tokens, i), ctes, cteRanges, false
		}
		ctes[name] = true
		i = next
		if i < len(tokens) && tokens[i].text == "(" {
			end, matched := matchingParen(tokens, i)
			if !matched {
				return firstVerb(tokens, i), ctes, cteRanges, false
			}
			i = end + 1
		}
		if i >= len(tokens) || keyword(tokens[i]) != "AS" {
			return firstVerb(tokens, i), ctes, cteRanges, false
		}
		i++
		if i < len(tokens) && keyword(tokens[i]) == "NOT" {
			i++
		}
		if i < len(tokens) && keyword(tokens[i]) == "MATERIALIZED" {
			i++
		}
		if i >= len(tokens) || tokens[i].text != "(" {
			return firstVerb(tokens, i), ctes, cteRanges, false
		}
		end, matched := matchingParen(tokens, i)
		if !matched {
			return firstVerb(tokens, i), ctes, cteRanges, false
		}
		cteRanges = append(cteRanges, tokenRange{start: i + 1, end: end})
		i = end + 1
		if i < len(tokens) && tokens[i].text == "," {
			i++
			continue
		}
		return i, ctes, cteRanges, true
	}
}

func firstVerb(tokens []token, start int) int {
	for i := max(start, 0); i < len(tokens); i++ {
		if _, ok := supportedVerbs[keyword(tokens[i])]; ok {
			return i
		}
	}
	return -1
}

func collectRelationList(tokens []token, depths []int, keywordIndex int, add func(string, string)) bool {
	if keywordIndex < 0 || keywordIndex >= len(tokens) {
		return false
	}
	baseDepth := depths[keywordIndex]
	resolved := addRelationAfter(tokens, keywordIndex, AccessRead, add)
	for i := keywordIndex + 1; i < len(tokens); i++ {
		if depths[i] < baseDepth {
			break
		}
		if depths[i] != baseDepth {
			continue
		}
		if isRelationBoundary(tokens[i]) {
			break
		}
		if tokens[i].text == "," {
			if !addRelationAfter(tokens, i, AccessRead, add) {
				resolved = false
			}
		}
	}
	return resolved
}

func addRelationAfter(tokens []token, keywordIndex int, access string, add func(string, string)) bool {
	if keywordIndex < 0 || keywordIndex+1 >= len(tokens) {
		return false
	}
	i := keywordIndex + 1
	for i < len(tokens) {
		switch keyword(tokens[i]) {
		case "ONLY", "LATERAL", "TABLE":
			i++
		default:
			goto target
		}
	}

target:
	if i >= len(tokens) {
		return false
	}
	if tokens[i].text == "(" {
		if keywordIndex+1 < len(tokens) && keyword(tokens[keywordIndex+1]) == "ONLY" {
			name, _, ok := qualifiedName(tokens, i+1)
			if ok {
				add(name, access)
				return true
			}
		}
		return true // subquery; nested FROM/JOIN tokens are scanned separately
	}
	name, next, ok := qualifiedName(tokens, i)
	if !ok {
		return false
	}
	if access == AccessRead && next < len(tokens) && tokens[next].text == "(" {
		return true // set-returning function, not a table identity
	}
	add(name, access)
	return true
}

func collectCreateTarget(tokens []token, depths []int, main int, add func(string, string)) bool {
	depth := depths[main]
	for i := main + 1; i < len(tokens) && depths[i] == depth; i++ {
		switch keyword(tokens[i]) {
		case "TABLE", "VIEW":
			return addDDLTarget(tokens, i, add)
		case "INDEX":
			on := findKeywordAtDepth(tokens, depths, i+1, depth, "ON")
			return addRelationAfter(tokens, on, AccessDDL, add)
		}
	}
	return true // CREATE SCHEMA/TYPE/FUNCTION has no table target
}

func collectAlterTarget(tokens []token, depths []int, main int, add func(string, string)) bool {
	depth := depths[main]
	table := findKeywordAtDepth(tokens, depths, main+1, depth, "TABLE")
	if table < 0 {
		return true
	}
	return addDDLTarget(tokens, table, add)
}

func collectDropTargets(tokens []token, depths []int, main int, add func(string, string)) bool {
	depth := depths[main]
	kindIndex := -1
	for i := main + 1; i < len(tokens) && depths[i] == depth; i++ {
		switch keyword(tokens[i]) {
		case "TABLE", "VIEW":
			kindIndex = i
		}
		if kindIndex >= 0 {
			break
		}
	}
	if kindIndex < 0 {
		return true
	}
	return collectDDLList(tokens, depths, kindIndex, add)
}

func collectTruncateTargets(tokens []token, depths []int, main int, add func(string, string)) bool {
	return collectDDLList(tokens, depths, main, add)
}

func addDDLTarget(tokens []token, keywordIndex int, add func(string, string)) bool {
	i := skipDDLModifiers(tokens, keywordIndex+1)
	if i >= len(tokens) {
		return false
	}
	name, _, ok := qualifiedName(tokens, i)
	if !ok {
		return false
	}
	add(name, AccessDDL)
	return true
}

func collectDDLList(tokens []token, depths []int, keywordIndex int, add func(string, string)) bool {
	baseDepth := depths[keywordIndex]
	i := skipDDLModifiers(tokens, keywordIndex+1)
	resolved := false
	for i < len(tokens) {
		if depths[i] != baseDepth || isRelationBoundary(tokens[i]) {
			break
		}
		name, next, ok := qualifiedName(tokens, i)
		if !ok {
			break
		}
		add(name, AccessDDL)
		resolved = true
		i = next
		if i >= len(tokens) || tokens[i].text != "," {
			break
		}
		i++
	}
	return resolved
}

func skipDDLModifiers(tokens []token, i int) int {
	for i < len(tokens) {
		switch keyword(tokens[i]) {
		case "IF", "NOT", "EXISTS", "ONLY", "TABLE", "MATERIALIZED":
			i++
		default:
			return i
		}
	}
	return i
}

func findKeywordAtDepth(tokens []token, depths []int, start, depth int, wanted string) int {
	for i := start; i < len(tokens); i++ {
		if depths[i] < depth {
			return -1
		}
		if depths[i] == depth && keyword(tokens[i]) == wanted {
			return i
		}
	}
	return -1
}

func tokenDepths(tokens []token) ([]int, bool) {
	depths := make([]int, len(tokens))
	depth := 0
	balanced := true
	for i, tok := range tokens {
		if tok.text == ")" {
			depth--
			if depth < 0 {
				depth = 0
				balanced = false
			}
		}
		depths[i] = depth
		if tok.text == "(" {
			depth++
		}
	}
	return depths, balanced && depth == 0
}

func matchingParen(tokens []token, start int) (int, bool) {
	if start < 0 || start >= len(tokens) || tokens[start].text != "(" {
		return -1, false
	}
	depth := 0
	for i := start; i < len(tokens); i++ {
		switch tokens[i].text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return -1, false
}

func qualifiedName(tokens []token, start int) (string, int, bool) {
	if start < 0 || start >= len(tokens) || !isIdentifier(tokens[start]) {
		return "", start, false
	}
	parts := []string{canonicalIdentifier(tokens[start])}
	i := start + 1
	for i+1 < len(tokens) && tokens[i].text == "." && isIdentifier(tokens[i+1]) {
		parts = append(parts, canonicalIdentifier(tokens[i+1]))
		i += 2
	}
	return strings.Join(parts, "."), i, true
}

func isIdentifier(tok token) bool {
	if tok.quoted {
		return true
	}
	if tok.text == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(tok.text)
	return r == '_' || unicode.IsLetter(r) || r >= utf8.RuneSelf
}

func canonicalIdentifier(tok token) string {
	if tok.quoted {
		return `"` + strings.ReplaceAll(tok.text, `"`, `""`) + `"`
	}
	return strings.ToLower(tok.text)
}

func keyword(tok token) string {
	if tok.quoted {
		return ""
	}
	return tok.upper
}

func isRelationBoundary(tok token) bool {
	switch keyword(tok) {
	case "WHERE", "GROUP", "ORDER", "HAVING", "LIMIT", "OFFSET", "FETCH", "FOR", "RETURNING", "UNION", "INTERSECT", "EXCEPT", "WINDOW", "SET", "VALUES", "WHEN", "CONFLICT", "DO":
		return true
	default:
		return tok.text == ";"
	}
}

func isCTE(name string, ctes map[string]bool) bool {
	if strings.Contains(name, ".") {
		return false
	}
	if ctes[name] {
		return true
	}
	for cte := range ctes {
		if identifierEqual(name, cte) {
			return true
		}
	}
	return false
}

func splitQualified(name string) []string {
	tokens, complete := lex(name)
	if !complete {
		return nil
	}
	canonical, next, ok := qualifiedName(tokens, 0)
	if !ok || next != len(tokens) {
		return nil
	}
	parts := make([]string, 0, 2)
	start := 0
	inQuote := false
	for i := 0; i < len(canonical); i++ {
		if canonical[i] == '"' {
			if inQuote && i+1 < len(canonical) && canonical[i+1] == '"' {
				i++
				continue
			}
			inQuote = !inQuote
			continue
		}
		if canonical[i] == '.' && !inQuote {
			parts = append(parts, canonical[start:i])
			start = i + 1
		}
	}
	return append(parts, canonical[start:])
}

func identifierEqual(left, right string) bool {
	lv, lq := identifierValue(left)
	rv, rq := identifierValue(right)
	if lq && rq {
		return lv == rv
	}
	if lq {
		return lv == strings.ToLower(lv) && lv == strings.ToLower(rv)
	}
	if rq {
		return rv == strings.ToLower(rv) && strings.ToLower(lv) == rv
	}
	return strings.EqualFold(lv, rv)
}

func identifierValue(value string) (string, bool) {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return strings.ReplaceAll(value[1:len(value)-1], `""`, `"`), true
	}
	return strings.ToLower(value), false
}

func lex(input string) ([]token, bool) {
	result := make([]token, 0, len(input)/4)
	complete := true
	for i := 0; i < len(input); {
		r, size := utf8.DecodeRuneInString(input[i:])
		if unicode.IsSpace(r) {
			i += size
			continue
		}
		if i+1 < len(input) && input[i:i+2] == "--" {
			i += 2
			for i < len(input) && input[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(input) && input[i:i+2] == "/*" {
			depth := 1
			i += 2
			for i < len(input) && depth > 0 {
				switch {
				case i+1 < len(input) && input[i:i+2] == "/*":
					depth++
					i += 2
				case i+1 < len(input) && input[i:i+2] == "*/":
					depth--
					i += 2
				default:
					_, n := utf8.DecodeRuneInString(input[i:])
					i += n
				}
			}
			if depth != 0 {
				complete = false
			}
			continue
		}
		if input[i] == '\'' {
			i++
			closed := false
			for i < len(input) {
				if input[i] == '\'' {
					if i+1 < len(input) && input[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				complete = false
			}
			continue
		}
		if input[i] == '"' {
			i++
			var value strings.Builder
			closed := false
			for i < len(input) {
				if input[i] == '"' {
					if i+1 < len(input) && input[i+1] == '"' {
						value.WriteByte('"')
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				r, n := utf8.DecodeRuneInString(input[i:])
				value.WriteRune(r)
				i += n
			}
			if !closed {
				complete = false
			}
			result = append(result, token{text: value.String(), quoted: true})
			continue
		}
		if input[i] == '$' {
			if end := dollarTagEnd(input, i); end > i {
				tag := input[i:end]
				closeAt := strings.Index(input[end:], tag)
				if closeAt < 0 {
					complete = false
					return result, complete
				}
				i = end + closeAt + len(tag)
				continue
			}
		}
		if isWordStart(r) {
			start := i
			i += size
			for i < len(input) {
				next, n := utf8.DecodeRuneInString(input[i:])
				if !isWordContinue(next) {
					break
				}
				i += n
			}
			value := input[start:i]
			result = append(result, token{text: value, upper: strings.ToUpper(value)})
			continue
		}
		if unicode.IsDigit(r) {
			start := i
			i += size
			for i < len(input) {
				next, n := utf8.DecodeRuneInString(input[i:])
				if !unicode.IsDigit(next) {
					break
				}
				i += n
			}
			value := input[start:i]
			result = append(result, token{text: value, upper: value})
			continue
		}
		result = append(result, token{text: string(r), upper: strings.ToUpper(string(r))})
		i += size
	}
	return result, complete
}

func dollarTagEnd(input string, start int) int {
	if start >= len(input) || input[start] != '$' {
		return -1
	}
	for i := start + 1; i < len(input); i++ {
		if input[i] == '$' {
			return i + 1
		}
		r, size := utf8.DecodeRuneInString(input[i:])
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return -1
		}
		i += size - 1
	}
	return -1
}

func isWordStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || r >= utf8.RuneSelf
}

func isWordContinue(r rune) bool {
	return isWordStart(r) || unicode.IsDigit(r) || r == '$'
}
