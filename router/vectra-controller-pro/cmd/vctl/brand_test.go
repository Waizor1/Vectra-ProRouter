package main

import (
	"context"
	"encoding/json"
	"testing"

	"vectra-controller-pro/internal/brand"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/state"
)

func TestTheRouterKeepsTheStrongestWordOnItsBrand(t *testing.T) {
	var st state.PersistedState
	if !noteBrand(&st, brand.Vectra, brand.SourceClaim, "") || st.Brand != "vectra" || st.BrandSource != "claim" {
		t.Fatalf("after the claim: %+v", st)
	}
	if !noteBrand(&st, brand.BloopCat, brand.SourceSubscription, "BloopCat_supbot") ||
		st.Brand != "bloopcat" || st.BrandSource != "subscription" || st.BrandSupport != "BloopCat_supbot" {
		t.Fatalf("after the subscription: %+v", st)
	}
	if noteBrand(&st, brand.Vectra, brand.SourceClaim, "") || st.Brand != "bloopcat" {
		t.Fatalf("a claim undid the subscription: %+v", st)
	}
	if noteBrand(&st, brand.BloopCat, brand.SourceSubscription, "BloopCat_supbot") {
		t.Fatal("the same word again is not a change")
	}
}

func TestReleaseForgetsTheBrand(t *testing.T) {
	st := state.PersistedState{Brand: "bloopcat", BrandSource: "subscription", BrandSupport: "BloopCat_supbot"}
	forgetBrand(&st)
	if st.Brand != "" || st.BrandSource != "" || st.BrandSupport != "" {
		t.Fatalf("%+v", st)
	}
}

func TestTheSupportBotComesAndGoesWithItsBrand(t *testing.T) {
	var st state.PersistedState
	noteBrand(&st, brand.BloopCat, brand.SourceSubscription, "BloopCat_supbot")
	// The same brand, a new support bot: a change, kept.
	if !noteBrand(&st, brand.BloopCat, brand.SourceSubscription, "BloopCat_help") || st.BrandSupport != "BloopCat_help" {
		t.Fatalf("a new support bot was not taken: %+v", st)
	}
	// A support bot only ever comes from the subscription.
	if noteBrand(&st, brand.BloopCat, brand.SourceClaim, "claim_supbot") || st.BrandSupport != "BloopCat_help" {
		t.Fatalf("a claim set the support bot: %+v", st)
	}
	// Another brand from the subscription replaces it, and the old brand's support bot goes with it.
	if !noteBrand(&st, brand.Vectra, brand.SourceSubscription, "") || st.Brand != "vectra" || st.BrandSupport != "" {
		t.Fatalf("the old brand's support bot outlived it: %+v", st)
	}
	// An unknown brand changes nothing, support bot included.
	if noteBrand(&st, brand.ID("acme"), brand.SourceSubscription, "acme_bot") || st.Brand != "vectra" || st.BrandSupport != "" {
		t.Fatalf("an unknown brand changed the state: %+v", st)
	}
}

// What the panel says at the claim is remembered across a restart; an
// unknown partner id, or none, changes nothing.
func TestTheClaimAnswerNamesTheBrandAndTheRouterRemembersIt(t *testing.T) {
	panel := newClaimPanel(t, controlplane.ClaimInfo{})
	dir := t.TempDir()
	d := newClaimDaemon(t, dir, panel.URL)

	d.adoptClaimInfo(context.Background(), controlplane.ClaimInfo{Brand: "acme"})
	d.adoptClaimInfo(context.Background(), controlplane.ClaimInfo{})
	if d.st.Brand != "" || d.st.BrandSource != "" {
		t.Fatalf("an unknown or absent brand was taken: %+v", d.st)
	}

	d.adoptClaimInfo(context.Background(), controlplane.ClaimInfo{Brand: "bloopcat"})
	if d.st.Brand != "bloopcat" || d.st.BrandSource != "claim" {
		t.Fatalf("the claim answer's brand was not taken: %+v", d.st)
	}
	onDisk, err := state.Load(d.cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Brand != "bloopcat" || onDisk.BrandSource != "claim" {
		t.Fatalf("the brand was not persisted: %+v", onDisk)
	}

	// An answer that names no brand does not forget it.
	d.adoptClaimInfo(context.Background(), controlplane.ClaimInfo{BotUsername: "VectraBot"})
	if d.st.Brand != "bloopcat" {
		t.Fatalf("an answer without a brand forgot it: %+v", d.st)
	}
}

func TestTheClaimAnswerNeverUndoesWhatTheSubscriptionSaid(t *testing.T) {
	panel := newClaimPanel(t, controlplane.ClaimInfo{})
	d := newClaimDaemon(t, t.TempDir(), panel.URL)
	d.learnBrand(brand.BloopCat, brand.SourceSubscription, "BloopCat_supbot")

	d.adoptClaimInfo(context.Background(), controlplane.ClaimInfo{Brand: "vectra"})
	if d.st.Brand != "bloopcat" || d.st.BrandSource != "subscription" || d.st.BrandSupport != "BloopCat_supbot" {
		t.Fatalf("the panel's claim answer overrode the subscription: %+v", d.st)
	}
}

// The owner unbinds the router: its brand was theirs. An answer that
// releases carries no news for the router, a stale brand included.
func TestAReleasingAnswerLeavesTheRouterWithoutABrand(t *testing.T) {
	panel := newClaimPanel(t, controlplane.ClaimInfo{})
	d := newClaimDaemon(t, t.TempDir(), panel.URL)
	d.st.ClaimOwner = &controlplane.ClaimOwner{Label: "Иван П."}
	d.learnBrand(brand.BloopCat, brand.SourceSubscription, "BloopCat_supbot")

	released := d.adoptClaimInfo(context.Background(), controlplane.ClaimInfo{
		Released: true, Owner: json.RawMessage(`null`), Brand: "bloopcat",
	})
	if !released {
		t.Fatal("the router did not release itself")
	}
	if d.st.Brand != "" || d.st.BrandSource != "" || d.st.BrandSupport != "" {
		t.Fatalf("the previous owner's brand survived the release: %+v", d.st)
	}

	// The next owner's word is taken as if nothing was held, whatever the old source was.
	d.adoptClaimInfo(context.Background(), controlplane.ClaimInfo{Brand: "vectra"})
	if d.st.Brand != "vectra" || d.st.BrandSource != "claim" {
		t.Fatalf("the next owner's brand was not taken: %+v", d.st)
	}
}

// A new owner takes the router over in one answer that also names their
// brand: the old owner's state goes first, the new owner's brand stays.
func TestTheNewOwnersBrandSurvivesTheReleaseOfTheOldOne(t *testing.T) {
	d, _ := connectTestDaemon(t)
	d.learnBrand(brand.BloopCat, brand.SourceSubscription, "BloopCat_supbot")

	released := d.adoptClaimInfo(context.Background(), controlplane.ClaimInfo{
		Owner: json.RawMessage(`{"ownerRef":"new-owner","label":"new"}`), Brand: "vectra",
	})
	if !released {
		t.Fatal("the owner change did not release the router")
	}
	if d.st.Brand != "vectra" || d.st.BrandSource != "claim" || d.st.BrandSupport != "" {
		t.Fatalf("after the owner change: %+v", d.st)
	}
}
