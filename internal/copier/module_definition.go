package copier

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// normalizeProgrammableDefinition removes the source header before the caller
// adds a CREATE OR ALTER header using the object's current metadata name.
func normalizeProgrammableDefinition(definition, objectType string) string {
	scanner := moduleSQLScanner{sql: definition}
	for token := scanner.next(); token != ""; token = scanner.next() {
		if !strings.EqualFold(token, "CREATE") && !strings.EqualFold(token, "ALTER") {
			continue
		}
		if end := programmableHeaderEnd(scanner, token, objectType); end > 0 {
			// Only remove the preamble and header. SET/GO lines, comments, and
			// string literals inside the body must remain untouched.
			return strings.TrimSpace(definition[end:])
		}
		break
	}
	return strings.TrimSpace(definition)
}

func programmableHeaderEnd(scanner moduleSQLScanner, verb, objectType string) int {
	token := scanner.next()
	if strings.EqualFold(verb, "CREATE") && strings.EqualFold(token, "OR") {
		if !strings.EqualFold(scanner.next(), "ALTER") {
			return 0
		}
		token = scanner.next()
	}
	if objectType == "PROCEDURE" && strings.EqualFold(token, "PROC") {
		token = "PROCEDURE"
	}
	if !strings.EqualFold(token, objectType) {
		return 0
	}
	if !isModuleIdentifier(scanner.next()) {
		return 0
	}
	end := scanner.pos
	if scanner.next() == "." {
		if !isModuleIdentifier(scanner.next()) {
			return 0
		}
		end = scanner.pos
	}
	return end
}

func isModuleIdentifier(token string) bool {
	if token == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(token)
	return unicode.IsLetter(r) || strings.ContainsRune(`_["@#`, r)
}

// moduleSQLScanner reads just enough T-SQL to locate a module header. Quoted
// tokens stay intact so keywords inside identifiers or strings cannot match a
// header. Comments (including nested block comments) act as whitespace.
type moduleSQLScanner struct {
	sql string
	pos int
}

func (s *moduleSQLScanner) next() string {
	for s.pos < len(s.sql) {
		rest := s.sql[s.pos:]
		r, size := utf8.DecodeRuneInString(rest)
		switch {
		case unicode.IsSpace(r) || r == '\ufeff':
			s.pos += size
		case strings.HasPrefix(rest, "--"):
			s.pos += 2
			for s.pos < len(s.sql) && s.sql[s.pos] != '\n' && s.sql[s.pos] != '\r' {
				s.pos++
			}
		case strings.HasPrefix(rest, "/*"):
			s.pos += 2
			depth := 1
			for s.pos < len(s.sql) && depth > 0 {
				switch {
				case strings.HasPrefix(s.sql[s.pos:], "/*"):
					depth++
					s.pos += 2
				case strings.HasPrefix(s.sql[s.pos:], "*/"):
					depth--
					s.pos += 2
				default:
					s.pos++
				}
			}
		default:
			return s.readToken()
		}
	}
	return ""
}

func (s *moduleSQLScanner) readToken() string {
	start := s.pos
	switch delimiter := s.sql[s.pos]; delimiter {
	case '[', '"', '\'':
		if delimiter == '[' {
			delimiter = ']'
		}
		s.pos++
		for s.pos < len(s.sql) {
			ch := s.sql[s.pos]
			s.pos++
			if ch != delimiter {
				continue
			}
			if s.pos < len(s.sql) && s.sql[s.pos] == delimiter {
				s.pos++ // Doubled delimiters escape a quote or closing bracket.
				continue
			}
			return s.sql[start:s.pos]
		}
		return "" // Unterminated quoted token.
	default:
		for s.pos < len(s.sql) {
			r, size := utf8.DecodeRuneInString(s.sql[s.pos:])
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("_@$#", r) {
				if s.pos == start {
					s.pos += size // Return punctuation as a separate token.
				}
				break
			}
			s.pos += size
		}
		return s.sql[start:s.pos]
	}
}
