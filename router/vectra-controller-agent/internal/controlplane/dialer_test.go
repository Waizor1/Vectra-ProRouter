package controlplane

import "testing"

func TestNewClientBuildsUsableClientWithFwmark(t *testing.T) {
	c := NewClient(Options{BaseURL: "http://example", Fwmark: 0x564354})
	if c.httpClient == nil {
		t.Fatal("expected a usable http client when a fwmark is configured")
	}
}

func TestNewClientBuildsUsableClientWithoutFwmark(t *testing.T) {
	c := NewClient(Options{BaseURL: "http://example"})
	if c.httpClient == nil {
		t.Fatal("expected a usable http client without a fwmark")
	}
	// Without a fwmark the client must dial normally — no custom transport is
	// installed, so the standard library default is used.
	if c.httpClient.Transport != nil {
		t.Fatalf("fwmark 0 must not install a custom transport, got %T", c.httpClient.Transport)
	}
}
