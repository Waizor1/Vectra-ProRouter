package passwall

import (
	"strings"
	"testing"
)

func TestSubscriptionPreviewLuaSupportsLua51StringLoading(t *testing.T) {
	if !strings.Contains(subscriptionPreviewLuaSource, "local loader = loadstring or load") {
		t.Fatal("subscription preview helper must prefer loadstring for Lua 5.1 routers")
	}
	if strings.Contains(subscriptionPreviewLuaSource, "local chunk, err = load(\"arg = {}") {
		t.Fatal("subscription preview helper must not call Lua 5.1 load() directly with a string")
	}
}
