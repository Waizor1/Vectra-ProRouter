// Package brand is whose router this is — the VPN service its owner pays for —
// and what the router calls itself for them: the name, the bot, support, the
// Wi-Fi network's name and the router's own address. The colours and the
// layout are the same for every brand.
package brand

import (
	"net/url"
	"regexp"
	"strings"
)

// ID names a brand: the partner id the panel knows it by.
type ID string

const (
	Vectra   ID = "vectra"
	BloopCat ID = "bloopcat"
)

// NeutralLANName is the router's address when it has no brand yet.
const NeutralLANName = "router.lan"

type Brand struct {
	ID         ID
	Name       string // what the UI calls the service
	SSIDPrefix string // the suggested network name's first part
	LANName    string // the router's own name on the LAN
	Site       string // a public name that also leads to the router; "" if none
	Bot        string // the service's Telegram bot (no "@"): where a claim code goes
	Support    string // its support bot (no "@"), until the subscription names one

	bots   []string // bot usernames its subscription's profile-web-page-url names
	titles []string // its subscription's whole profile-title, any case
}

var known = []Brand{
	{
		ID: Vectra, Name: "Vectra", SSIDPrefix: "Vectra", LANName: "vectra.lan",
		Site: "my.vectra-pro.net", Bot: "VectraConnect_bot", Support: "VectraConnect_support_bot",
		bots: []string{"VectraConnect_bot"}, titles: []string{"Vectra Connect"},
	},
	{
		ID: BloopCat, Name: "BloopCat", SSIDPrefix: "BloopCat", LANName: "bloopcat.lan",
		Bot: "BloopCat_bot", Support: "BloopCat_supbot",
		bots: []string{"BloopCat_bot"}, titles: []string{"BloopCat"},
	},
}

// Lookup is the brand with this id; false for "" (neutral) and unknown ids.
func Lookup(id ID) (Brand, bool) {
	for _, b := range known {
		if b.ID == id {
			return b, true
		}
	}
	return Brand{}, false
}

// Parse reads a brand id as UCI, the installer or the panel writes it.
func Parse(s string) (ID, bool) {
	b, ok := Lookup(ID(strings.ToLower(strings.TrimSpace(s))))
	return b.ID, ok
}

// FromSubscription: the brand a subscription's headers name — its bot in
// profile-web-page-url first, else its whole profile-title. A title is never
// matched by its start: a user in no squad gets the panel's global title,
// "BloopCat | TriadConnect", which names no brand.
func FromSubscription(webPageURL, title string) (ID, bool) {
	if bot := telegramName(webPageURL); bot != "" {
		for _, b := range known {
			for _, want := range b.bots {
				if strings.EqualFold(bot, want) {
					return b.ID, true
				}
			}
		}
	}
	t := strings.TrimSpace(title)
	for _, b := range known {
		for _, want := range b.titles {
			if strings.EqualFold(t, want) {
				return b.ID, true
			}
		}
	}
	return "", false
}

// SupportFromURL: the Telegram name a support-url leads to; "" otherwise.
func SupportFromURL(raw string) string { return telegramName(raw) }

var telegramUsername = regexp.MustCompile(`^[A-Za-z0-9_]{5,32}$`)

func telegramName(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	if host := strings.ToLower(u.Hostname()); host != "t.me" && host != "telegram.me" {
		return ""
	}
	name := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 2)[0]
	if !telegramUsername.MatchString(name) {
		return ""
	}
	return name
}

// Source is where the router learned its brand.
type Source string

const (
	SourceInstall      Source = "install"
	SourceClaim        Source = "claim"
	SourceSubscription Source = "subscription"
)

func rank(s Source) int {
	switch s {
	case SourceSubscription:
		return 3
	case SourceClaim:
		return 2
	case SourceInstall:
		return 1
	}
	return 0
}

// Adopt: whether a brand seen from one source replaces the one held. The
// subscription is what the owner actually pays for, so it outranks the
// panel's word at the claim, which outranks the installer's label; a later
// word from the same rank wins. With nothing held (never learned, or forgotten
// on unbinding) any known brand is taken, whatever source the old one had.
func Adopt(held ID, heldFrom Source, seen ID, from Source) bool {
	if _, ok := Lookup(seen); !ok {
		return false
	}
	if held == "" {
		return true
	}
	if seen == held {
		return rank(from) > rank(heldFrom)
	}
	return rank(from) >= rank(heldFrom)
}

var (
	parenthesized = regexp.MustCompile(`\([^)]*\)`)
	modelToken    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{3,15}$`)
	hasDigit      = regexp.MustCompile(`[0-9]`)
	hasLetter     = regexp.MustCompile(`[A-Za-z]`)
	// A memory size or a hardware revision is not the model's name.
	sizeOrRevision = regexp.MustCompile(`(?i)^(\d+(MB|GB|M|G)|(rev|ver)\d+)$`)
)

// ModelPrefix is the neutral network name's first part: the model's own name
// ("AX3000T" of "Xiaomi Mi Router AX3000T"), the last word of 4-16 letters
// and digits that has both, ignoring notes in parentheses and words that are
// a memory size ("256MB") or a revision ("Rev2"); else "Router".
func ModelPrefix(model string) string {
	fields := strings.Fields(parenthesized.ReplaceAllString(model, " "))
	for i := len(fields) - 1; i >= 0; i-- {
		f := fields[i]
		if modelToken.MatchString(f) && hasDigit.MatchString(f) && hasLetter.MatchString(f) && !sizeOrRevision.MatchString(f) {
			return f
		}
	}
	return "Router"
}
