// Package dictcheck resolves a dotted key against the frontend dictionaries the
// way i18next does — by walking the nesting.
//
// It exists for the parity tests. Those used to look for "<leaf>:" anywhere in the
// file, which answers "does this word appear somewhere" rather than "does this key
// resolve": a leaf named `update` under `nodes` would satisfy a check for
// `audit.sec.update`, and the panel would still render a raw key. Leaf names repeat
// across sections often enough for that to be a live risk rather than a theoretical
// one.
//
// Only the frontend dictionaries are parsed, and only the shape they are written
// in: a nested object literal of string values. That is enough, and a real
// TypeScript parse is not worth pulling in.
package dictcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Dict is a parsed dictionary: nested maps whose leaves are strings.
type Dict map[string]any

// Load parses web/src/i18n/<name> relative to the repository root, which is found
// by walking up from dir until go.mod is seen.
func Load(dir, name string) (Dict, error) {
	root, err := repoRoot(dir)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(root, "web", "src", "i18n", name))
	if err != nil {
		return nil, err
	}
	return Parse(string(b)), nil
}

func repoRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, "go.mod")); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("no go.mod above %s", dir)
		}
		abs = parent
	}
}

// Resolve returns the value at a dotted path, and whether it resolved to a string.
func (d Dict) Resolve(dotted string) (string, bool) {
	var cur any = d
	for _, part := range strings.Split(dotted, ".") {
		m, ok := cur.(Dict)
		if !ok {
			return "", false
		}
		cur, ok = m[part]
		if !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	return s, ok
}

// Parse reads a dictionary source into nested maps.
//
// Strings are consumed before comments, not after: a value like
// "http://example" carries // inside a string, and stripping comments first would
// swallow the rest of that line and desynchronise everything below it.
func Parse(src string) Dict {
	out := Dict{}
	stack := []Dict{out}
	pending := ""
	for i, n := 0, len(src); i < n; {
		c := src[i]
		switch {
		case c == '/' && i+1 < n && src[i+1] == '/':
			if j := strings.IndexByte(src[i:], '\n'); j >= 0 {
				i += j
			} else {
				i = n
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			if j := strings.Index(src[i:], "*/"); j >= 0 {
				i += j + 2
			} else {
				i = n
			}
		case c == '"' || c == '\'' || c == '`':
			val, next := scanString(src, i)
			i = next
			if pending != "" {
				stack[len(stack)-1][pending] = val
				pending = ""
			}
		case c == '{':
			if pending != "" {
				child := Dict{}
				stack[len(stack)-1][pending] = child
				stack = append(stack, child)
				pending = ""
			} else {
				// The root `const ru = {`, and any brace with no key in front of it:
				// keep writing into the current map rather than orphaning a new one.
				stack = append(stack, stack[len(stack)-1])
			}
			i++
		case c == '}':
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			i++
		case isIdentStart(rune(c)):
			j := i
			for j < n && isIdentPart(rune(src[j])) {
				j++
			}
			word := src[i:j]
			k := j
			for k < n && (src[k] == ' ' || src[k] == '\t' || src[k] == '\n' || src[k] == '\r') {
				k++
			}
			if k < n && src[k] == ':' {
				pending, i = word, k+1
			} else {
				// An identifier that is not a key clears any pending one: this is the
				// annotation in `const en: Dict = {`, which would otherwise nest the
				// whole file under a key named "en".
				pending, i = "", j
			}
		default:
			i++
		}
	}
	return out
}

func scanString(src string, i int) (string, int) {
	quote := src[i]
	var b strings.Builder
	for j := i + 1; j < len(src); {
		switch {
		case src[j] == '\\' && j+1 < len(src):
			b.WriteByte(src[j+1])
			j += 2
		case src[j] == quote:
			return b.String(), j + 1
		default:
			b.WriteByte(src[j])
			j++
		}
	}
	return b.String(), len(src)
}

func isIdentStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_' || r == '$'
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || unicode.IsDigit(r)
}
