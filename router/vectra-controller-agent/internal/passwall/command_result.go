package passwall

import "strings"

var benignSubscribeStderrLines = map[string]struct{}{
	"tr: write error: Broken pipe":     {},
	"head: standard output: I/O error": {},
}

func NormalizeCommandResult(result CommandResult) CommandResult {
	if !isSubscribeStartCommand(result.Command) || strings.TrimSpace(result.Stderr) == "" {
		return result
	}

	result.Stderr = filterBenignSubscribeStderr(result.Stderr)
	return result
}

func isSubscribeStartCommand(command string) bool {
	fields := strings.Fields(command)
	return len(fields) >= 3 &&
		fields[0] == "lua" &&
		fields[1] == "/usr/share/passwall2/subscribe.lua" &&
		fields[2] == "start"
}

func filterBenignSubscribeStderr(stderr string) string {
	lines := strings.Split(stderr, "\n")
	filtered := make([]string, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if _, ok := benignSubscribeStderrLines[trimmed]; ok {
			continue
		}
		if trimmed != "" {
			filtered = append(filtered, line)
		}
	}

	return strings.TrimSpace(strings.Join(filtered, "\n"))
}
