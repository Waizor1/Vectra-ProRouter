// Package uci reads OpenWrt UCI files (/etc/config/*) the way uci itself
// parses them. It only reads: every change goes through the uci command, one
// argv per value, so nothing the router is told can inject a statement.
package uci

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Section is one `config <type> [<name>]` block. An anonymous section has no
// name; uci addresses it as @<type>[<index among its type>] (Ref).
type Section struct {
	Type    string
	Name    string
	Index   int // among the sections of the same type, in file order
	Options map[string]string
	Lists   map[string][]string
}

// Ref is how the uci command addresses the section.
func (s Section) Ref() string {
	if s.Name != "" {
		return s.Name
	}
	return fmt.Sprintf("@%s[%d]", s.Type, s.Index)
}

// Get is an option's value, "" when it is not set.
func (s Section) Get(option string) string { return s.Options[option] }

// File is a parsed UCI file.
type File struct {
	Sections []Section
}

// Named returns the section named name, or nil. uci merges repeated
// declarations of a named section; so does this.
func (f *File) Named(name string) *Section {
	for i := range f.Sections {
		if f.Sections[i].Name == name {
			return &f.Sections[i]
		}
	}
	return nil
}

// OfType returns the sections of one type, in file order.
func (f *File) OfType(typ string) []Section {
	var out []Section
	for _, s := range f.Sections {
		if s.Type == typ {
			out = append(out, s)
		}
	}
	return out
}

// Load reads and parses a UCI file.
func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Parse parses UCI text. Anything uci would refuse — an unknown statement, an
// option outside a section, a wrong word count, an unterminated quote — is an
// error, never a guess.
func Parse(src string) (*File, error) {
	stmts, err := Statements(src)
	if err != nil {
		return nil, err
	}
	f := &File{}
	var cur *Section
	counts := map[string]int{}
	for _, w := range stmts {
		switch {
		case w[0] == "package" && len(w) == 2:
		case w[0] == "config" && (len(w) == 2 || len(w) == 3):
			name := ""
			if len(w) == 3 {
				name = w[2]
			}
			if s := f.Named(name); name != "" && s != nil {
				cur = s
				continue
			}
			f.Sections = append(f.Sections, Section{Type: w[1], Name: name, Index: counts[w[1]],
				Options: map[string]string{}, Lists: map[string][]string{}})
			counts[w[1]]++
			cur = &f.Sections[len(f.Sections)-1]
		case (w[0] == "option" || w[0] == "list") && len(w) == 3:
			if cur == nil {
				return nil, fmt.Errorf("%s outside a section: %q", w[0], strings.Join(w, " "))
			}
			if w[0] == "option" {
				cur.Options[w[1]] = w[2] // the last one counts, as in uci
			} else {
				cur.Lists[w[1]] = append(cur.Lists[w[1]], w[2])
			}
		default:
			return nil, fmt.Errorf("not a uci statement: %q", strings.Join(w, " "))
		}
	}
	return f, nil
}

// Statements splits UCI text into statements of words by uci's quoting
// rules: '…' is literal, "…" and bare words take backslash escapes, adjacent
// parts join into one word, a quoted part may run over lines, and # outside
// quotes starts a comment. An unterminated quote is an error.
func Statements(src string) ([][]string, error) {
	var stmts [][]string
	var words []string
	var word strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(src); i++ {
		switch c := src[i]; c {
		case '\n':
			endWord()
			if len(words) > 0 {
				stmts, words = append(stmts, words), nil
			}
		case ' ', '\t', '\r':
			endWord()
		case '#':
			endWord()
			for i+1 < len(src) && src[i+1] != '\n' {
				i++
			}
		case '\'':
			j := strings.IndexByte(src[i+1:], '\'')
			if j < 0 {
				return nil, errors.New("unterminated '")
			}
			word.WriteString(src[i+1 : i+1+j])
			inWord, i = true, i+1+j
		case '"':
			inWord = true
			for i++; i < len(src) && src[i] != '"'; i++ {
				if src[i] == '\\' && i+1 < len(src) {
					if i++; src[i] == '\n' {
						continue // a line continuation
					}
				}
				word.WriteByte(src[i])
			}
			if i >= len(src) {
				return nil, errors.New(`unterminated "`)
			}
		case '\\':
			inWord = true
			if i+1 < len(src) {
				if i++; src[i] != '\n' {
					word.WriteByte(src[i])
				}
			}
		default:
			inWord = true
			word.WriteByte(c)
		}
	}
	endWord()
	if len(words) > 0 {
		stmts = append(stmts, words)
	}
	return stmts, nil
}
