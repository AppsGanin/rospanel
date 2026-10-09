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
// That takes skipping string literals, quoted identifiers and comments, and the one
// statement whose semicolons are not ours: CREATE [TEMP|TEMPORARY] TRIGGER, whose
// body is a list of "statement;" closed by END. It ends the way SQLite itself
// decides a trigger is complete (sqlite3_complete): at "; END ;", an END standing
// alone between two semicolons. Keywords used as names — a table called trigger, a
// column called begin — change nothing here, so they cannot hide a statement.
func CheckSQL(sql string) error {
	l := lexer{s: sql}
	var stmt []string    // the current statement's tokens, upper-cased
	var segment []string // inside a trigger: the tokens since its last semicolon
	trigger := false
	for {
		tok, ok := l.next()
		if !ok {
			break
		}
		if l.err != nil {
			return l.err
		}
		if tok == ";" {
			if trigger && !(len(segment) == 1 && segment[0] == "END") {
				segment = segment[:0] // a separator inside the trigger body
				continue
			}
			stmt, segment, trigger = stmt[:0], segment[:0], false
			continue
		}
		if tok == "SQLITE_DBPAGE" { // raw pages of the file: a plugin could corrupt it for the host to read
			return fmt.Errorf("%w: sqlite_dbpage", ErrForbidden)
		}
		if len(stmt) == 0 && forbidden[tok] {
			return fmt.Errorf("%w: %s (the host manages pragmas, attachments and transactions)", ErrForbidden, tok)
		}
		stmt = append(stmt, tok)
		segment = append(segment, tok)
		if !trigger && isCreateTrigger(stmt) {
			trigger = true
		}
	}
	return l.err
}

// isCreateTrigger reports whether a statement begins CREATE [TEMP|TEMPORARY] TRIGGER.
func isCreateTrigger(stmt []string) bool {
	switch {
	case len(stmt) == 2:
		return stmt[0] == "CREATE" && stmt[1] == "TRIGGER"
	case len(stmt) == 3:
		return stmt[0] == "CREATE" && (stmt[1] == "TEMP" || stmt[1] == "TEMPORARY") && stmt[2] == "TRIGGER"
	}
	return false
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
		case c == '$' || c == '@' || c == ':' || c == '#':
			l.skipVariable()
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

// skipVariable moves past a bound parameter ($a, @a, :a, #a) the way SQLite's
// tokenizer does — including its Tcl form $a(...), which runs to the ")" through
// quotes and semicolons. Read as a name and a quoted string instead, it would let
// `$a(');PRAGMA ...;--'` hide a statement SQLite runs.
func (l *lexer) skipVariable() {
	l.i++ // the sigil
	n := 0
	for l.i < len(l.s) {
		c := l.s[l.i]
		switch {
		case isIDChar(c):
			n++
			l.i++
		case c == '(' && n > 0:
			for l.i++; l.i < len(l.s) && !isSpace(l.s[l.i]) && l.s[l.i] != ')'; l.i++ {
			}
			if l.i < len(l.s) && l.s[l.i] == ')' {
				l.i++
			} else {
				l.err = errors.New("pdb: unterminated ( in a parameter")
			}
			return
		case c == ':' && l.peek(1) == ':':
			l.i += 2
		default:
			return
		}
	}
}

// isWordStart and isIDChar follow SQLite's identifier characters: a "$" may go on
// a name but not start one (it starts a parameter).
func isWordStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isWordPart(c byte) bool { return isIDChar(c) }

func isIDChar(c byte) bool { return isWordStart(c) || c == '$' || (c >= '0' && c <= '9') }

// isSpace is SQLite's sqlite3Isspace.
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == '\v'
}
