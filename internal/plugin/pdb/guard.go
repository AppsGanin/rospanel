package pdb

import (
	"errors"
	"fmt"
	"strings"
)

// ErrForbidden is returned for SQL a plugin may not run.
var ErrForbidden = errors.New("pdb: statement not allowed")

// forbidden are the statements a plugin may not start. The connection settings that
// hold a plugin to its file and its quota are themselves SQL a plugin could undo —
// `PRAGMA max_page_count = 1000000` lifts the quota (verified 2026-10-06) — and
// transactions belong to the host, which rolls back whatever a call leaves open.
// ATTACH and VACUUM INTO are also refused by the connection's attach limit; they
// are listed so the error says why.
var forbidden = map[string]bool{
	"PRAGMA": true, "ATTACH": true, "DETACH": true, "VACUUM": true,
	"BEGIN": true, "COMMIT": true, "END": true, "ROLLBACK": true, "SAVEPOINT": true, "RELEASE": true,
	"EXPLAIN": true, // EXPLAIN PRAGMA … would hide a pragma behind a harmless keyword
}

// CheckSQL refuses SQL containing a statement that starts with a forbidden keyword.
//
// It is a lexer, not a parser: it only has to find where each statement starts.
// That takes skipping string literals, quoted identifiers and comments, and
// knowing that inside CREATE TRIGGER … BEGIN … END the semicolons separate the
// body's statements (which SQLite already limits to INSERT/UPDATE/DELETE/SELECT)
// rather than ours — and that CASE … END nests inside such a body.
func CheckSQL(sql string) error {
	l := lexer{s: sql}
	var stmt []string // keyword-ish tokens of the current statement, upper-cased
	trigger, depth := false, 0
	for {
		tok, ok := l.next()
		if !ok {
			break
		}
		if l.err != nil {
			return l.err
		}
		if tok == ";" {
			if trigger && depth > 0 {
				continue // a separator inside the trigger body
			}
			stmt, trigger, depth = stmt[:0], false, 0
			continue
		}
		if len(stmt) == 0 && forbidden[tok] {
			return fmt.Errorf("%w: %s (the host manages pragmas, attachments and transactions)", ErrForbidden, tok)
		}
		stmt = append(stmt, tok)
		if len(stmt) <= 3 && tok == "TRIGGER" && stmt[0] == "CREATE" {
			trigger = true
		}
		if !trigger {
			continue
		}
		switch tok {
		case "BEGIN", "CASE":
			depth++
		case "END":
			if depth > 0 {
				depth--
			}
		}
	}
	return l.err
}

// lexer yields upper-cased words and ";" — everything else (literals, identifiers
// in quotes, punctuation, comments) is skipped.
type lexer struct {
	s   string
	i   int
	err error
}

func (l *lexer) next() (string, bool) {
	for l.i < len(l.s) {
		c := l.s[l.i]
		switch {
		case c == ';':
			l.i++
			return ";", true
		case c == '\'' || c == '"' || c == '`':
			l.skipQuoted(c, c)
		case c == '[':
			l.skipQuoted('[', ']')
		case c == '-' && l.peek(1) == '-':
			for l.i < len(l.s) && l.s[l.i] != '\n' {
				l.i++
			}
		case c == '/' && l.peek(1) == '*':
			end := strings.Index(l.s[l.i+2:], "*/")
			if end < 0 {
				l.i = len(l.s) // SQLite treats an unterminated comment as running to the end
			} else {
				l.i += end + 4
			}
		case isWordStart(c):
			start := l.i
			for l.i < len(l.s) && isWordPart(l.s[l.i]) {
				l.i++
			}
			return strings.ToUpper(l.s[start:l.i]), true
		default:
			l.i++
		}
		if l.err != nil {
			return "", true
		}
	}
	return "", false
}

func (l *lexer) peek(off int) byte {
	if l.i+off < len(l.s) {
		return l.s[l.i+off]
	}
	return 0
}

// skipQuoted moves past a quoted token; a doubled closing quote is an escape.
func (l *lexer) skipQuoted(open, close byte) {
	l.i++ // the opening quote
	for l.i < len(l.s) {
		if l.s[l.i] == close {
			if open == close && l.peek(1) == close {
				l.i += 2
				continue
			}
			l.i++
			return
		}
		l.i++
	}
	l.err = fmt.Errorf("pdb: unterminated %c in SQL", open)
}

func isWordStart(c byte) bool {
	return c == '_' || c == '$' || c == '@' || c == ':' || c == '?' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isWordPart(c byte) bool { return isWordStart(c) || (c >= '0' && c <= '9') }
