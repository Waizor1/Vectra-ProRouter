// Package routepolicy is the operator's route policy without PassWall2: the
// slots (WorldProxy, YouTube, Special, ... — which domains and addresses go
// through which node), the nodes, and the xray configuration made from them.
//
// The fleet's routers carry that policy as PassWall2's UCI configuration,
// written by the panel. This package reads it as it is (ImportPassWall) and
// makes from it what PassWall2's own generator made (Generate), so a router
// can drop PassWall2 and keep its routing to the byte where it matters.
package routepolicy

import (
	"fmt"
	"sort"
	"strings"
)

// Section is one UCI section: `config <Type> '<Name>'` and its options and
// lists, in file order.
type Section struct {
	Type    string
	Name    string
	Options map[string]string
	Lists   map[string][]string
	// Anonymous: the section had no name (UCI's @type[i] addressing).
	Anonymous bool
	// Keys are the option and list names in the order the file had them.
	// Export writes them in that order, then any others sorted.
	Keys []string
}

// Get is an option's value ("" when unset).
func (s Section) Get(key string) string { return s.Options[key] }

// ParseUCI reads a UCI configuration file (as /etc/config/* holds them, and
// as `uci export` prints them). A quoted value may run over several lines —
// PassWall2 keeps its domain lists that way.
func ParseUCI(text string) ([]Section, error) {
	stmts, err := uciStatements(text)
	if err != nil {
		return nil, err
	}
	var out []Section
	var cur *Section
	for _, st := range stmts {
		words, line := st.words, st.line
		switch words[0] {
		case "package":
			continue
		case "config":
			if len(words) < 2 || len(words) > 3 {
				return nil, fmt.Errorf("uci line %d: config needs a type and at most a name", line)
			}
			out = append(out, Section{Type: words[1], Options: map[string]string{}, Lists: map[string][]string{}})
			cur = &out[len(out)-1]
			if len(words) == 3 {
				cur.Name = words[2]
			} else {
				cur.Anonymous = true
				cur.Name = fmt.Sprintf("@%s[%d]", words[1], countType(out[:len(out)-1], words[1]))
			}
		case "option", "list":
			if cur == nil {
				return nil, fmt.Errorf("uci line %d: %s outside a section", line, words[0])
			}
			if len(words) != 3 {
				return nil, fmt.Errorf("uci line %d: %s needs a name and a value", line, words[0])
			}
			if _, o := cur.Options[words[1]]; !o {
				if _, l := cur.Lists[words[1]]; !l {
					cur.Keys = append(cur.Keys, words[1])
				}
			}
			if words[0] == "option" {
				cur.Options[words[1]] = words[2]
			} else {
				cur.Lists[words[1]] = append(cur.Lists[words[1]], words[2])
			}
		default:
			return nil, fmt.Errorf("uci line %d: unknown keyword %q", line, words[0])
		}
	}
	return out, nil
}

func countType(secs []Section, typ string) int {
	n := 0
	for _, s := range secs {
		if s.Type == typ {
			n++
		}
	}
	return n
}

type uciStmt struct {
	words []string
	line  int
}

// uciStatements splits UCI text into statements of words: bare,
// 'single-quoted' (UCI writes an embedded quote as quote, backslash, quote,
// quote), or "double-quoted"
// with backslash escapes. A newline ends a statement unless it is inside
// quotes; # starts a comment outside them.
func uciStatements(s string) ([]uciStmt, error) {
	var out []uciStmt
	var words []string
	var b strings.Builder
	inWord := false
	line, start := 1, 1
	flushWord := func() {
		if inWord {
			words = append(words, b.String())
			b.Reset()
			inWord = false
		}
	}
	endStmt := func() {
		flushWord()
		if len(words) > 0 {
			out = append(out, uciStmt{words: words, line: start})
		}
		words = nil
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if len(words) == 0 && !inWord {
			start = line
		}
		switch {
		case c == '\n':
			endStmt()
			line++
		case c == ' ' || c == '\t' || c == '\r':
			flushWord()
		case c == '\'':
			inWord = true
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, fmt.Errorf("uci line %d: unterminated single quote", line)
			}
			v := s[i+1 : i+1+j]
			line += strings.Count(v, "\n")
			b.WriteString(v)
			i += j + 1
		case c == '"':
			inWord = true
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				if s[i] == '\n' {
					line++
				}
				b.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, fmt.Errorf("uci line %d: unterminated double quote", line)
			}
		case c == '\\' && i+1 < len(s):
			inWord = true
			i++
			b.WriteByte(s[i])
		case c == '#' && !inWord:
			for i+1 < len(s) && s[i+1] != '\n' {
				i++
			}
		default:
			inWord = true
			b.WriteByte(c)
		}
	}
	endStmt()
	return out, nil
}

// Export writes sections as a UCI configuration file, the way uci commits
// one: a blank line and "config <type> '<name>'" per section (no name for an
// anonymous one), then its options and lists, every value single-quoted.
func Export(secs []Section) string {
	var b strings.Builder
	for _, s := range secs {
		b.WriteString("\nconfig " + s.Type)
		if !s.Anonymous && s.Name != "" {
			b.WriteString(" " + uciQuote(s.Name))
		}
		b.WriteString("\n")
		for _, k := range s.orderedKeys() {
			if v, ok := s.Options[k]; ok {
				b.WriteString("\toption " + k + " " + uciQuote(v) + "\n")
			}
			for _, v := range s.Lists[k] {
				b.WriteString("\tlist " + k + " " + uciQuote(v) + "\n")
			}
		}
	}
	return b.String()
}

// orderedKeys: Keys first, then the rest sorted — every option and list once.
func (s Section) orderedKeys() []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range s.Keys {
		if !seen[k] && (hasKey(s.Options, k) || len(s.Lists[k]) > 0) {
			seen[k] = true
			out = append(out, k)
		}
	}
	var rest []string
	for k := range s.Options {
		if !seen[k] {
			seen[k] = true
			rest = append(rest, k)
		}
	}
	for k, v := range s.Lists {
		if !seen[k] && len(v) > 0 {
			seen[k] = true
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func hasKey(m map[string]string, k string) bool { _, ok := m[k]; return ok }

// uciQuote single-quotes a value; a quote inside it is closed, escaped and
// reopened (quote, backslash, quote, quote), as uci writes it.
func uciQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}
