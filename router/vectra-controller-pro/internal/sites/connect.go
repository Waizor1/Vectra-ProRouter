package sites

import (
	"errors"
	"strings"
)

// ParseConnectDomain accepts bounded DNS rule names, including underscore
// labels used by service records. It accepts no URL, address or geo category.
func ParseConnectDomain(s string) (Site, error) {
	if len(s) > 253 || strings.ContainsAny(s, "/:@?#") {
		return Site{}, errors.New("invalid domain")
	}
	parsed, err := Parse(s)
	if err == nil && parsed.Domain && parsed.Service == "" {
		return parsed, nil
	}
	s = strings.ToLower(foldWidth(s))
	s = trimWildcard(strings.TrimSuffix(s, "."))
	if !strings.Contains(s, "_") {
		return Site{}, errors.New("invalid domain")
	}
	labels := strings.Split(s, ".")
	ascii := make([]string, len(labels))
	uni := make([]string, len(labels))
	for i, label := range labels {
		if strings.Contains(label, "_") {
			if label == "" || len(label) > 63 {
				return Site{}, errors.New("invalid label")
			}
			for _, r := range label {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
					return Site{}, errors.New("invalid label")
				}
			}
			ascii[i] = label
			uni[i] = label
		} else {
			u, a, e := domainLabel(label)
			if e != nil {
				return Site{}, errors.New("invalid label")
			}
			ascii[i] = a
			uni[i] = u
		}
	}
	if last := ascii[len(ascii)-1]; strings.Trim(last, "0123456789") == "" {
		return Site{}, errors.New("invalid domain")
	}
	host := strings.Join(ascii, ".")
	if len(host) > 253 {
		return Site{}, errors.New("invalid domain")
	}
	return Site{Display: strings.Join(uni, "."), Domain: true, ASCII: host}, nil
}
