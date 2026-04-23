package passwall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type SubscriptionPreviewFingerprint struct {
	Fingerprint string `json:"fingerprint"`
}

type SubscriptionPreviewEntry struct {
	SubscriptionID          string                         `json:"subscriptionId"`
	SubscriptionKey         string                         `json:"subscriptionKey"`
	Remark                  string                         `json:"remark"`
	URLHash                 string                         `json:"urlHash"`
	Enabled                 bool                           `json:"enabled"`
	AccessMode              string                         `json:"accessMode"`
	UserAgent               *string                        `json:"userAgent,omitempty"`
	FetchState              string                         `json:"fetchState"`
	HTTPStatus              *int                           `json:"httpStatus,omitempty"`
	PayloadMode             string                         `json:"payloadMode"`
	PayloadNodeCount        *int                           `json:"payloadNodeCount,omitempty"`
	ResolvedPayloadNodeCount *int                          `json:"resolvedPayloadNodeCount,omitempty"`
	PayloadFingerprints     []SubscriptionPreviewFingerprint `json:"payloadFingerprints"`
	CheckedAt               string                         `json:"checkedAt"`
}

type SubscriptionInspectResult struct {
	CheckedAt          string                    `json:"checkedAt"`
	SubscriptionDigest string                    `json:"subscriptionDigest"`
	Entries            []SubscriptionPreviewEntry `json:"entries"`
}

type subscriptionPreviewHelperOutput struct {
	CheckedAt string                           `json:"checkedAt"`
	Entries   []subscriptionPreviewHelperEntry `json:"entries"`
}

type subscriptionPreviewHelperEntry struct {
	SubscriptionID string                          `json:"subscriptionId"`
	Remark         string                          `json:"remark"`
	URL            string                          `json:"url"`
	Enabled        bool                            `json:"enabled"`
	AccessMode     string                          `json:"accessMode"`
	UserAgent      *string                         `json:"userAgent,omitempty"`
	FetchState     string                          `json:"fetchState"`
	HTTPStatus     *int                            `json:"httpStatus,omitempty"`
	PayloadMode    string                          `json:"payloadMode"`
	PayloadNodeCount *int                          `json:"payloadNodeCount,omitempty"`
	ResolvedNodes  []subscriptionPreviewHelperNode `json:"resolvedNodes"`
	CheckedAt      string                          `json:"checkedAt"`
}

type subscriptionPreviewHelperNode struct {
	Label     string         `json:"label,omitempty"`
	Protocol  string         `json:"protocol,omitempty"`
	Address   string         `json:"address,omitempty"`
	Port      *int           `json:"port,omitempty"`
	Username  string         `json:"username,omitempty"`
	Password  string         `json:"password,omitempty"`
	Transport string         `json:"transport,omitempty"`
	TLS       *bool          `json:"tls,omitempty"`
	Extras    map[string]any `json:"extras,omitempty"`
}

func InspectSubscriptions(ctx context.Context, backend UCIBackend) (SubscriptionInspectResult, error) {
	if backend == nil {
		backend = ExecBackend{}
	}

	imported, err := NewImporter(backend).Import(ctx, "check_in")
	if err != nil {
		return SubscriptionInspectResult{}, fmt.Errorf("import live passwall config: %w", err)
	}

	preview, err := runSubscriptionPreviewHelper(ctx, backend)
	if err != nil {
		return SubscriptionInspectResult{}, err
	}

	entries := make([]SubscriptionPreviewEntry, 0, len(preview.Entries))
	for _, entry := range preview.Entries {
		urlHash := BuildSubscriptionURLHash(entry.URL)
		checkedAt := normalizePreviewTimestamp(entry.CheckedAt, preview.CheckedAt)
		payloadFingerprints := make([]SubscriptionPreviewFingerprint, 0, len(entry.ResolvedNodes))
		for _, node := range entry.ResolvedNodes {
			payloadFingerprints = append(payloadFingerprints, SubscriptionPreviewFingerprint{
				Fingerprint: BuildSubscriptionPreviewNodeFingerprint(node),
			})
		}

		entries = append(entries, SubscriptionPreviewEntry{
			SubscriptionID:           strings.TrimSpace(entry.SubscriptionID),
			SubscriptionKey:          BuildSubscriptionSemanticKey(entry.Remark, entry.URL),
			Remark:                   defaultTrimmed(entry.Remark, "subscription"),
			URLHash:                  urlHash,
			Enabled:                  entry.Enabled,
			AccessMode:               normalizePreviewAccessMode(entry.AccessMode),
			UserAgent:                trimStringPointer(entry.UserAgent),
			FetchState:               normalizePreviewFetchState(entry.FetchState),
			HTTPStatus:               entry.HTTPStatus,
			PayloadMode:              normalizePreviewPayloadMode(entry.PayloadMode),
			PayloadNodeCount:         entry.PayloadNodeCount,
			ResolvedPayloadNodeCount: intPointer(len(payloadFingerprints)),
			PayloadFingerprints:      payloadFingerprints,
			CheckedAt:                checkedAt,
		})
	}

	sort.Slice(entries, func(left, right int) bool {
		return entries[left].SubscriptionKey < entries[right].SubscriptionKey
	})

	return SubscriptionInspectResult{
		CheckedAt:          normalizePreviewTimestamp(preview.CheckedAt, time.Now().UTC().Format(time.RFC3339)),
		SubscriptionDigest: ComputeSubscriptionPreviewDigest(imported.Config.Subscriptions),
		Entries:            entries,
	}, nil
}

func runSubscriptionPreviewHelper(
	ctx context.Context,
	backend UCIBackend,
) (subscriptionPreviewHelperOutput, error) {
	tempDir, err := os.MkdirTemp("", "vectra-sub-preview-*")
	if err != nil {
		return subscriptionPreviewHelperOutput{}, fmt.Errorf("create subscription preview temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	scriptPath := filepath.Join(tempDir, "subscription-preview.lua")
	if err := os.WriteFile(scriptPath, []byte(subscriptionPreviewLuaSource), 0o700); err != nil {
		return subscriptionPreviewHelperOutput{}, fmt.Errorf("write subscription preview helper: %w", err)
	}

	result, err := backend.Run(ctx, "lua", scriptPath)
	if err != nil {
		if strings.TrimSpace(result.Stderr) != "" {
			return subscriptionPreviewHelperOutput{}, fmt.Errorf(
				"run subscription preview helper: %w (%s)",
				err,
				strings.TrimSpace(result.Stderr),
			)
		}
		return subscriptionPreviewHelperOutput{}, fmt.Errorf("run subscription preview helper: %w", err)
	}

	var payload subscriptionPreviewHelperOutput
	if decodeErr := json.Unmarshal([]byte(result.Stdout), &payload); decodeErr != nil {
		return subscriptionPreviewHelperOutput{}, fmt.Errorf(
			"decode subscription preview helper output: %w",
			decodeErr,
		)
	}

	return payload, nil
}

func BuildSubscriptionURLHash(url string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(url)))
	return hex.EncodeToString(sum[:])
}

func BuildSubscriptionSemanticKey(remark string, url string) string {
	normalizedRemark := strings.ToLower(strings.TrimSpace(remark))
	if normalizedRemark == "" {
		normalizedRemark = "subscription"
	}
	return normalizedRemark + "::" + BuildSubscriptionURLHash(url)
}

func ComputeSubscriptionPreviewDigest(settings SubscriptionSettings) string {
	itemsByKey := make(map[string]map[string]any)
	keys := make([]string, 0, len(settings.Items))
	for _, item := range settings.Items {
		key := BuildSubscriptionSemanticKey(item.Remark, item.URL)
		if _, exists := itemsByKey[key]; exists {
			continue
		}
		itemsByKey[key] = map[string]any{
			"subscriptionKey": key,
			"remark":          strings.TrimSpace(item.Remark),
			"urlHash":         BuildSubscriptionURLHash(item.URL),
			"enabled":         item.Enabled,
			"addMode":         defaultTrimmed(item.AddMode, "2"),
			"extras":          normalizePreviewExtras(item.Extras),
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	items := make([]any, 0, len(keys))
	for _, key := range keys {
		items = append(items, itemsByKey[key])
	}

	payload := map[string]any{
		"filterKeywordMode": defaultTrimmed(settings.FilterKeywordMode, "0"),
		"discardList":       normalizeStringList(settings.DiscardList),
		"keepList":          normalizeStringList(settings.KeepList),
		"typePreferences": map[string]any{
			"shadowsocks": defaultTrimmed(settings.TypePreferences.Shadowsocks, ""),
			"trojan":      defaultTrimmed(settings.TypePreferences.Trojan, ""),
			"vmess":       defaultTrimmed(settings.TypePreferences.Vmess, ""),
			"vless":       defaultTrimmed(settings.TypePreferences.Vless, ""),
			"hysteria2":   defaultTrimmed(settings.TypePreferences.Hysteria2, ""),
		},
		"domainStrategy": defaultTrimmed(settings.DomainStrategy, "auto"),
		"items":          items,
	}

	return hashStableValue(payload)
}

func BuildSubscriptionPreviewNodeFingerprint(node subscriptionPreviewHelperNode) string {
	payload := map[string]any{
		"label":     normalizePreviewText(node.Label),
		"protocol":  normalizePreviewLowerText(node.Protocol),
		"address":   normalizePreviewLowerText(node.Address),
		"port":      normalizePreviewPort(node.Port),
		"username":  normalizePreviewText(node.Username),
		"password":  normalizePreviewText(node.Password),
		"transport": normalizePreviewLowerText(node.Transport),
		"tls":       node.TLS,
		"extras":    normalizePreviewNodeExtras(node.Extras),
	}

	return hashStableValue(payload)
}

func normalizePreviewNodeExtras(extras map[string]any) map[string]any {
	normalized := make(map[string]any)
	for key, value := range extras {
		if key == "add_mode" || key == "group" {
			continue
		}
		switch typed := value.(type) {
		case string:
			trimmed := strings.TrimSpace(typed)
			if trimmed != "" {
				normalized[key] = trimmed
			}
		case bool:
			normalized[key] = typed
		case float64:
			normalized[key] = typed
		case int:
			normalized[key] = typed
		case []string:
			list := normalizeStringList(typed)
			if len(list) > 0 {
				normalized[key] = list
			}
		case []any:
			list := make([]string, 0, len(typed))
			valid := true
			for _, entry := range typed {
				raw, ok := entry.(string)
				if !ok {
					valid = false
					break
				}
				trimmed := strings.TrimSpace(raw)
				if trimmed != "" {
					list = append(list, trimmed)
				}
			}
			if valid && len(list) > 0 {
				normalized[key] = list
			}
		case nil:
			normalized[key] = nil
		}
	}
	return normalized
}

func normalizePreviewExtras(extras map[string]any) map[string]any {
	normalized := make(map[string]any)
	for key, value := range extras {
		switch typed := value.(type) {
		case string:
			trimmed := strings.TrimSpace(typed)
			if trimmed != "" {
				normalized[key] = trimmed
			}
		case bool:
			normalized[key] = typed
		case float64:
			normalized[key] = typed
		case int:
			normalized[key] = typed
		case []string:
			list := normalizeStringList(typed)
			if len(list) > 0 {
				normalized[key] = list
			}
		case []any:
			list := make([]string, 0, len(typed))
			valid := true
			for _, entry := range typed {
				raw, ok := entry.(string)
				if !ok {
					valid = false
					break
				}
				trimmed := strings.TrimSpace(raw)
				if trimmed != "" {
					list = append(list, trimmed)
				}
			}
			if valid && len(list) > 0 {
				normalized[key] = list
			}
		case nil:
			normalized[key] = nil
		}
	}
	return normalized
}

func normalizeStringList(values []string) []string {
	list := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			list = append(list, trimmed)
		}
	}
	return list
}

func normalizePreviewFetchState(value string) string {
	switch value {
	case "ok", "disabled", "http_error", "network_error", "parse_error":
		return value
	default:
		return "network_error"
	}
}

func normalizePreviewPayloadMode(value string) string {
	switch value {
	case "plain-lines", "base64-lines", "ssd-json", "single-link", "unknown":
		return value
	default:
		return "unknown"
	}
}

func normalizePreviewAccessMode(value string) string {
	switch strings.TrimSpace(value) {
	case "direct":
		return "direct"
	case "proxy":
		return "proxy"
	default:
		return "auto"
	}
}

func normalizePreviewTimestamp(primary string, fallback string) string {
	trimmed := strings.TrimSpace(primary)
	if trimmed != "" {
		return trimmed
	}
	return fallback
}

func normalizePreviewText(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func normalizePreviewLowerText(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return strings.ToLower(trimmed)
}

func normalizePreviewPort(port *int) any {
	if port == nil || *port <= 0 {
		return nil
	}
	return *port
}

func trimStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func intPointer(value int) *int {
	return &value
}

func defaultTrimmed(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed != "" {
		return trimmed
	}
	return fallback
}

func hashStableValue(value any) string {
	encoded := stableJSONString(value)
	sum := sha256.Sum256([]byte(encoded))
	return hex.EncodeToString(sum[:])
}

func stableJSONString(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return mustJSON(typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return mustJSON(typed)
	case []string:
		values := make([]string, 0, len(typed))
		for _, entry := range typed {
			values = append(values, stableJSONString(entry))
		}
		return "[" + strings.Join(values, ",") + "]"
	case []any:
		values := make([]string, 0, len(typed))
		for _, entry := range typed {
			values = append(values, stableJSONString(entry))
		}
		return "[" + strings.Join(values, ",") + "]"
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, mustJSON(key)+":"+stableJSONString(typed[key]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		return mustJSON(typed)
	}
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("stable json marshal: %v", err))
	}
	return string(encoded)
}
