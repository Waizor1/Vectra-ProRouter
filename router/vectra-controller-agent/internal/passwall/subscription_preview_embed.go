package passwall

import _ "embed"

// subscriptionPreviewLuaSource is a PassWall-synced wrapper that loads the
// runtime subscribe.lua from the router and appends a read-only preview entrypoint.
//
//go:embed subscription_preview.lua
var subscriptionPreviewLuaSource string
