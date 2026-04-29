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

func TestSubscriptionPreviewLuaStripsRuntimeSubscribeShebang(t *testing.T) {
	if !strings.Contains(subscriptionPreviewLuaSource, "local function strip_shebang(content)") {
		t.Fatal("subscription preview helper must strip the runtime subscribe.lua shebang before prepending Lua source")
	}
	if !strings.Contains(subscriptionPreviewLuaSource, "source = strip_shebang(source)") {
		t.Fatal("subscription preview helper must sanitize the runtime subscribe.lua source before loading it")
	}
}

func TestSubscriptionPreviewLuaKeepsInjectedJsonInLoadedChunk(t *testing.T) {
	if !strings.Contains(subscriptionPreviewLuaSource, `local vectra_preview_json = require("luci.jsonc")`) {
		t.Fatal("subscription preview helper must require json inside the loaded Lua chunk")
	}
	if strings.Contains(subscriptionPreviewLuaSource, "io.write(json.stringify({") {
		t.Fatal("subscription preview helper must not depend on wrapper-local json inside the loaded Lua chunk")
	}
}
