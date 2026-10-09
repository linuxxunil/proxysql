package sqlguard

import (
	"fmt"
	"strings"
	"unicode"
)

// Classify returns the statement type: SELECT, INSERT, UPDATE, DELETE,
// CREATE, ALTER, DROP, TRUNCATE, WITH (unresolved), EXPLAIN, UTILITY, etc.
func Classify(sql string) string {
	s := stripLeadingComments(sql)
	up := strings.ToUpper(s)

	first := firstWord(up)
	switch first {
	case "SELECT", "INSERT", "UPDATE", "DELETE",
		"CREATE", "ALTER", "DROP", "TRUNCATE",
		"GRANT", "REVOKE", "EXPLAIN", "SHOW", "SET",
		"COPY", "VACUUM", "LISTEN", "NOTIFY", "PREPARE", "EXECUTE":
		if first == "WITH_FALLTHROUGH" {
			break
		}
		// WITH and EXPLAIN need deeper inspection below; others are final.
		if first != "" {
			// For EXPLAIN <stmt>, report EXPLAIN so default policy denies it.
			return first
		}
	case "WITH":
		return classifyWith(up)
	case "TABLE", "VALUES":
		return "SELECT" // TABLE t / VALUES (...) behave like SELECT
	}
	if first == "" {
		return ""
	}
	// BEGIN/COMMIT/ROLLBACK and everything else -> UTILITY (denied by default)
	switch first {
	case "BEGIN", "START", "COMMIT", "ROLLBACK", "SAVEPOINT":
		return "UTILITY"
	}
	return first
}

func classifyWith(up string) string {
	// Find first top-level DML keyword outside parens/quotes.
	kw := scanTopLevelKeyword(up, []string{"INSERT", "UPDATE", "DELETE", "SELECT", "MERGE"})
	if kw == "" {
		return "WITH"
	}
	return kw
}

// Allowed reports whether stmt is in the allowlist (case-insensitive).
func Allowed(stmt string, allowlist []string) bool {
	for _, a := range allowlist {
		if strings.EqualFold(strings.TrimSpace(a), stmt) {
			return true
		}
	}
	return false
}

// CheckRawSQL enforces parameterized queries when allowRaw==false.
// It requires len(params) == max($n) found outside string literals.
func CheckRawSQL(sql string, nParams int, allowRaw bool) error {
	max := maxPlaceholder(sql)
	if allowRaw {
		// Still validate: params must not exceed placeholders claim,
		// but bare SQL without params is fine.
		if nParams > 0 && max == 0 {
			return fmt.Errorf("params supplied but sql has no $n placeholders")
		}
		if max > 0 && nParams != max {
			return fmt.Errorf("params count %d != placeholders $%d", nParams, max)
		}
		return nil
	}
	if max == 0 && nParams == 0 {
		return nil // e.g. SELECT 1
	}
	if max == 0 && nParams > 0 {
		return fmt.Errorf("params supplied but sql has no $n placeholders (raw sql not allowed)")
	}
	if nParams != max {
		return fmt.Errorf("params count %d != placeholders $%d (raw sql not allowed)", nParams, max)
	}
	return nil
}

// ContainsStringLiteral reports true if sql has a single-quoted or
// dollar-quoted string literal outside comments (double-quoted identifiers
// don't count). Used by strict mode to force $n params instead of inlined
// strings. Numeric literals are intentionally NOT flagged (LIMIT, SELECT 1,
// type precision etc. need them); that is a documented limitation.
func ContainsStringLiteral(sql string) bool {
	i := 0
	for i < len(sql) {
		switch {
		case sql[i] == '\'':
			return true // found one (skip logic unnecessary for boolean)
		case sql[i] == '"':
			i = skipQuoted(sql, i, '"')
		case strings.HasPrefix(sql[i:], "--"):
			if idx := strings.Index(sql[i:], "\n"); idx >= 0 {
				i += idx + 1
			} else {
				return false
			}
		case strings.HasPrefix(sql[i:], "/*"):
			if end := strings.Index(sql[i:], "*/"); end >= 0 {
				i += end + 2
			} else {
				return false
			}
		case sql[i] == '$':
			if _, ok := dollarTag(sql[i:]); ok {
				return true
			}
			i++
		default:
			i++
		}
	}
	return false
}

// HasMultiStatements reports true if sql contains 2+ statements.
func HasMultiStatements(sql string) bool {
	stmts := splitStatements(sql)
	nonEmpty := 0
	for _, s := range stmts {
		if strings.TrimSpace(stripLeadingComments(s)) != "" {
			nonEmpty++
		}
	}
	return nonEmpty > 1
}

func firstWord(s string) string {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	i := 0
	for i < len(s) && (unicode.IsLetter(rune(s[i])) || s[i] == '_') {
		i++
	}
	return s[:i]
}

func stripLeadingComments(s string) string {
	for {
		t := strings.TrimLeftFunc(s, unicode.IsSpace)
		if strings.HasPrefix(t, "--") {
			if idx := strings.Index(t, "\n"); idx >= 0 {
				s = t[idx+1:]
				continue
			}
			return ""
		}
		if strings.HasPrefix(t, "/*") {
			if end := strings.Index(t, "*/"); end >= 0 {
				s = t[end+2:]
				continue
			}
			return ""
		}
		return t
	}
}

// maxPlaceholder finds max $n outside '...', "...", `...`, and $tag$...$tag$.
func maxPlaceholder(sql string) int {
	max := 0
	i := 0
	for i < len(sql) {
		switch {
		case sql[i] == '\'':
			i = skipQuoted(sql, i, '\'')
		case sql[i] == '"':
			i = skipQuoted(sql, i, '"')
		case strings.HasPrefix(sql[i:], "--"):
			if idx := strings.Index(sql[i:], "\n"); idx >= 0 {
				i += idx + 1
			} else {
				return max
			}
		case strings.HasPrefix(sql[i:], "/*"):
			if end := strings.Index(sql[i:], "*/"); end >= 0 {
				i += end + 2
			} else {
				return max
			}
		case sql[i] == '$':
			if tag, ok := dollarTag(sql[i:]); ok {
				// dollar-quoted string: skip to closing tag
				end := strings.Index(sql[i+len(tag):], tag)
				if end < 0 {
					return max
				}
				i += len(tag) + end + len(tag)
				continue
			}
			j := i + 1
			n := 0
			for j < len(sql) && sql[j] >= '0' && sql[j] <= '9' {
				n = n*10 + int(sql[j]-'0')
				j++
			}
			if j > i+1 {
				if n > max {
					max = n
				}
				i = j
			} else {
				i++
			}
		default:
			i++
		}
	}
	return max
}

func skipQuoted(s string, i int, q byte) int {
	i++ // open quote
	for i < len(s) {
		if s[i] == q {
			if q == '\'' && i+1 < len(s) && s[i+1] == '\'' {
				i += 2 // escaped ''
				continue
			}
			return i + 1
		}
		i++
	}
	return i
}

func dollarTag(s string) (string, bool) {
	// $tag$ where tag is [A-Za-z_][A-Za-z0-9_]*
	if len(s) < 2 || s[0] != '$' {
		return "", false
	}
	j := 1
	for j < len(s) && s[j] != '$' {
		c := s[j]
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || j > 1 && c >= '0' && c <= '9') {
			return "", false
		}
		j++
	}
	if j >= len(s) || s[j] != '$' {
		return "", false
	}
	return s[:j+1], true
}

func splitStatements(sql string) []string {
	var parts []string
	start := 0
	i := 0
	for i < len(sql) {
		switch {
		case sql[i] == '\'':
			i = skipQuoted(sql, i, '\'')
		case sql[i] == '"':
			i = skipQuoted(sql, i, '"')
		case strings.HasPrefix(sql[i:], "--"):
			if idx := strings.Index(sql[i:], "\n"); idx >= 0 {
				i += idx + 1
			} else {
				i = len(sql)
			}
		case strings.HasPrefix(sql[i:], "/*"):
			if end := strings.Index(sql[i:], "*/"); end >= 0 {
				i += end + 2
			} else {
				i = len(sql)
			}
		case sql[i] == '$':
			if tag, ok := dollarTag(sql[i:]); ok {
				end := strings.Index(sql[i+len(tag):], tag)
				if end < 0 {
					i = len(sql)
				} else {
					i += len(tag) + end + len(tag)
				}
			} else {
				i++
			}
		case sql[i] == ';':
			parts = append(parts, sql[start:i])
			start = i + 1
			i++
		default:
			i++
		}
	}
	parts = append(parts, sql[start:])
	return parts
}

func scanTopLevelKeyword(up string, kws []string) string {
	depth := 0
	i := 0
	inS, inD := false, false
	for i < len(up) {
		c := up[i]
		if inS {
			if c == '\'' {
				if i+1 < len(up) && up[i+1] == '\'' {
					i += 2
					continue
				}
				inS = false
			}
			i++
			continue
		}
		if inD {
			if c == '"' {
				inD = false
			}
			i++
			continue
		}
		switch c {
		case '\'':
			inS = true
		case '"':
			inD = true
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 && (c == '_' || c >= 'A' && c <= 'Z') {
				j := i
				for j < len(up) && (up[j] == '_' || up[j] >= 'A' && up[j] <= 'Z' || up[j] >= '0' && up[j] <= '9') {
					j++
				}
				word := up[i:j]
				for _, k := range kws {
					if word == k {
						return k
					}
				}
				i = j
				continue
			}
		}
		i++
	}
	return ""
}
