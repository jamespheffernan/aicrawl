package archive

import (
	"fmt"
	"strings"
)

type sqlToken struct {
	text  string
	depth int
}

func ValidateReadOnlySQL(query string) (string, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return "", fmt.Errorf("sql query is empty")
	}
	trimmed = strings.TrimSuffix(trimmed, ";")
	tokens, err := sqlTokens(trimmed)
	if err != nil {
		return "", err
	}
	if len(tokens) == 0 {
		return "", fmt.Errorf("sql query is empty")
	}
	for _, token := range tokens {
		if forbiddenSQLToken(token.text) {
			return "", fmt.Errorf("only read-only SELECT, WITH, and safe PRAGMA statements are allowed")
		}
	}
	lowered := strings.ToLower(strings.TrimSpace(trimmed))
	switch tokens[0].text {
	case "select":
		return trimmed, nil
	case "with":
		if withHasTopLevelSelect(tokens) {
			return trimmed, nil
		}
	case "pragma":
		if safePragma(lowered) {
			return trimmed, nil
		}
	}
	return "", fmt.Errorf("only read-only SELECT, WITH, and safe PRAGMA statements are allowed")
}

func forbiddenSQLToken(token string) bool {
	switch token {
	case "insert", "update", "delete", "replace", "create", "drop", "alter",
		"vacuum", "attach", "detach", "reindex", "analyze":
		return true
	default:
		return false
	}
}

func withHasTopLevelSelect(tokens []sqlToken) bool {
	for i := 1; i < len(tokens); i++ {
		if tokens[i].depth == 0 && tokens[i].text == "select" {
			return true
		}
	}
	return false
}

func sqlTokens(query string) ([]sqlToken, error) {
	var tokens []sqlToken
	depth := 0
	for i := 0; i < len(query); {
		c := query[i]
		switch {
		case isSQLSpace(c):
			i++
		case c == ';':
			return nil, fmt.Errorf("only one read-only SQL statement is allowed")
		case c == '-' && i+1 < len(query) && query[i+1] == '-':
			i = skipSQLLineComment(query, i+2)
		case c == '/' && i+1 < len(query) && query[i+1] == '*':
			next, err := skipSQLBlockComment(query, i+2)
			if err != nil {
				return nil, err
			}
			i = next
		case c == '\'' || c == '"' || c == '`':
			next, err := skipSQLQuoted(query, i, c)
			if err != nil {
				return nil, err
			}
			i = next
		case c == '[':
			next, err := skipSQLBracketQuoted(query, i+1)
			if err != nil {
				return nil, err
			}
			i = next
		case c == '(':
			depth++
			i++
		case c == ')':
			if depth == 0 {
				return nil, fmt.Errorf("sql query has unbalanced parentheses")
			}
			depth--
			i++
		case isSQLIdentStart(c):
			start := i
			i++
			for i < len(query) && isSQLIdentPart(query[i]) {
				i++
			}
			tokens = append(tokens, sqlToken{text: strings.ToLower(query[start:i]), depth: depth})
		default:
			i++
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("sql query has unbalanced parentheses")
	}
	return tokens, nil
}

func skipSQLLineComment(query string, i int) int {
	for i < len(query) && query[i] != '\n' && query[i] != '\r' {
		i++
	}
	return i
}

func skipSQLBlockComment(query string, i int) (int, error) {
	for i+1 < len(query) {
		if query[i] == '*' && query[i+1] == '/' {
			return i + 2, nil
		}
		i++
	}
	return 0, fmt.Errorf("sql query has an unterminated block comment")
}

func skipSQLQuoted(query string, i int, quote byte) (int, error) {
	i++
	for i < len(query) {
		if query[i] == quote {
			if i+1 < len(query) && query[i+1] == quote {
				i += 2
				continue
			}
			return i + 1, nil
		}
		i++
	}
	return 0, fmt.Errorf("sql query has an unterminated quoted value")
}

func skipSQLBracketQuoted(query string, i int) (int, error) {
	for i < len(query) {
		if query[i] == ']' {
			return i + 1, nil
		}
		i++
	}
	return 0, fmt.Errorf("sql query has an unterminated quoted identifier")
}

func isSQLSpace(c byte) bool {
	switch c {
	case ' ', '\n', '\r', '\t', '\f':
		return true
	default:
		return false
	}
}

func isSQLIdentStart(c byte) bool {
	return c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func isSQLIdentPart(c byte) bool {
	return isSQLIdentStart(c) || ('0' <= c && c <= '9')
}

func safePragma(lowered string) bool {
	body := strings.TrimSpace(strings.TrimPrefix(lowered, "pragma "))
	if body == "" || strings.Contains(body, "=") {
		return false
	}
	allowed := []struct {
		name  string
		arg   bool
		noArg bool
	}{
		{name: "user_version", noArg: true},
		{name: "database_list", noArg: true},
		{name: "table_info", arg: true},
		{name: "index_list", arg: true},
		{name: "index_info", arg: true},
		{name: "integrity_check", arg: true, noArg: true},
		{name: "quick_check", arg: true, noArg: true},
		{name: "foreign_key_check", arg: true, noArg: true},
	}
	for _, pragma := range allowed {
		if body == pragma.name {
			return pragma.noArg
		}
		if !strings.HasPrefix(body, pragma.name) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(body, pragma.name))
		if rest == "" {
			return pragma.noArg
		}
		if !pragma.arg || !strings.HasPrefix(rest, "(") || !strings.HasSuffix(rest, ")") {
			return false
		}
		arg := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(rest, "("), ")"))
		return arg != ""
	}
	return false
}
