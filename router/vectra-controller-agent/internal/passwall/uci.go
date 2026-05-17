package passwall

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

type UCISection struct {
	Name    string
	Type    string
	Options map[string][]string
}

type UCIBackend interface {
	Show(ctx context.Context, packageName string) ([]string, error)
	Batch(ctx context.Context, commands []string) error
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}

// UCIReverter is an optional capability that allows callers to discard
// uncommitted UCI staging for a package after a failed batch operation. uci
// batch leaves partial staging in /tmp/.uci/<package> if it errors before its
// final commit, and that staging will silently merge into the next caller's
// view. Implementations that can revert (notably ExecBackend) opt in by
// satisfying this interface; callers should type-assert and best-effort-call.
type UCIReverter interface {
	Revert(ctx context.Context, packageName string) error
}

type ExecBackend struct{}

func (ExecBackend) Show(ctx context.Context, packageName string) ([]string, error) {
	out, err := exec.CommandContext(ctx, "uci", "-q", "show", packageName).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("uci show %s: %w (%s)", packageName, err, strings.TrimSpace(string(out)))
	}

	return splitNonEmptyLines(string(out)), nil
}

func (ExecBackend) Batch(ctx context.Context, commands []string) error {
	cmd := exec.CommandContext(ctx, "uci", "batch")
	cmd.Stdin = strings.NewReader(strings.Join(commands, "\n") + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("uci batch: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (ExecBackend) Revert(ctx context.Context, packageName string) error {
	cmd := exec.CommandContext(ctx, "uci", "-q", "revert", packageName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("uci revert %s: %w (%s)", packageName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (ExecBackend) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := CommandResult{
		Command: joinCommand(name, args),
		Stdout:  strings.TrimSpace(stdout.String()),
		Stderr:  strings.TrimSpace(stderr.String()),
	}
	if err != nil {
		if result.Stderr == "" {
			result.Stderr = err.Error()
		}
		return result, fmt.Errorf("%s: %w", result.Command, err)
	}
	return result, nil
}

func ParseUCILines(lines []string) ([]UCISection, error) {
	normalizedLines, err := normalizeUCILines(lines)
	if err != nil {
		return nil, err
	}

	sections := make([]UCISection, 0, len(normalizedLines))
	indexByName := make(map[string]int)

	for _, rawLine := range normalizedLines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		if isSectionLine(line) {
			namePart, typePart, ok := strings.Cut(line, "=")
			if !ok {
				return nil, fmt.Errorf("invalid uci section line: %s", line)
			}
			packageName, sectionName, ok := strings.Cut(namePart, ".")
			if !ok || packageName == "" || sectionName == "" {
				return nil, fmt.Errorf("invalid uci section reference: %s", line)
			}
			sections = append(sections, UCISection{
				Name:    sectionName,
				Type:    strings.TrimSpace(typePart),
				Options: map[string][]string{},
			})
			indexByName[sectionName] = len(sections) - 1
			continue
		}

		left, right, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid uci option line: %s", line)
		}
		parts := strings.SplitN(left, ".", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid uci option reference: %s", line)
		}

		sectionIndex, ok := indexByName[parts[1]]
		if !ok {
			sections = append(sections, UCISection{
				Name:    parts[1],
				Options: map[string][]string{},
			})
			sectionIndex = len(sections) - 1
			indexByName[parts[1]] = sectionIndex
		}

		value := decodeUCIValue(right)
		sections[sectionIndex].Options[parts[2]] = append(sections[sectionIndex].Options[parts[2]], value)
	}

	return sections, nil
}

func normalizeUCILines(lines []string) ([]string, error) {
	normalized := make([]string, 0, len(lines))
	var pending strings.Builder
	var pendingQuote byte

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		if pendingQuote != 0 {
			pending.WriteString("\n")
			pending.WriteString(line)
			if logicalUCIValueClosed(pending.String(), pendingQuote) {
				normalized = append(normalized, pending.String())
				pending.Reset()
				pendingQuote = 0
			}
			continue
		}

		_, right, ok := strings.Cut(line, "=")
		if ok {
			if quote, multiline := detectMultilineQuotedValue(right); multiline {
				pending.WriteString(line)
				pendingQuote = quote
				continue
			}
		}

		normalized = append(normalized, line)
	}

	if pendingQuote != 0 {
		return nil, fmt.Errorf("unterminated uci quoted value: %s", pending.String())
	}

	return normalized, nil
}

func logicalUCIValueClosed(line string, quote byte) bool {
	_, right, ok := strings.Cut(line, "=")
	if !ok {
		return false
	}

	return hasClosingQuotedValue(right, quote)
}

func isSectionLine(line string) bool {
	if strings.Count(line, "=") != 1 {
		return false
	}
	right := line[strings.LastIndex(line, "=")+1:]
	right = strings.TrimSpace(right)
	return !strings.HasPrefix(right, "'") && !strings.HasPrefix(right, "\"")
}

func splitNonEmptyLines(input string) []string {
	lines := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func detectMultilineQuotedValue(raw string) (byte, bool) {
	raw = strings.TrimSpace(raw)
	if len(raw) == 0 {
		return 0, false
	}

	quote := raw[0]
	if quote != '\'' && quote != '"' {
		return 0, false
	}

	return quote, !hasClosingQuotedValue(raw, quote)
}

func hasClosingQuotedValue(raw string, quote byte) bool {
	raw = strings.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != quote {
		return false
	}

	escaped := false
	for i := 1; i < len(raw); i++ {
		char := raw[i]
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if char == quote && i == len(raw)-1 {
			return true
		}
	}

	return false
}

func decodeUCIValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 {
		if (raw[0] == '\'' && raw[len(raw)-1] == '\'') || (raw[0] == '"' && raw[len(raw)-1] == '"') {
			return raw[1 : len(raw)-1]
		}
	}
	return raw
}

func encodeUCIValue(raw string) string {
	return "'" + strings.ReplaceAll(raw, "'", "'\\''") + "'"
}

func joinCommand(name string, args []string) string {
	if len(args) == 0 {
		return name
	}
	return name + " " + strings.Join(args, " ")
}

func cloneOptions(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for key, values := range in {
		copied := make([]string, len(values))
		copy(copied, values)
		out[key] = copied
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
