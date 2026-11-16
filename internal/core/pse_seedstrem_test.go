package core

import (
	"context"
	"testing"
)

func TestSeedstremRequiresPlaybackDespiteAvailabilityOverride(t *testing.T) {
	r := Release{StreamURL: "https://addon.example/private/stremio/play/0123456789abcdef0123456789abcdef01234567", StreamOnDemand: true}
	for _, p := range []string{IndexerPurposeConsole, IndexerPurposeTest, IndexerPurposeAvailability, IndexerPurposeOther, IndexerPurposeDetail} {
		ctx := WithOnDemandStreamsForAvailability(WithIndexerPurpose(context.Background(), p))
		if OnDemandAllowedAt(ctx, r, 0) {
			t.Fatalf("opened for %s", p)
		}
	}
	warm, promote := WithSpeculative(WithIndexerPurpose(context.Background(), IndexerPurposeWarmup))
	if OnDemandAllowedAt(warm, r, 0) {
		t.Fatal("unpromoted warmup opened")
	}
	promote()
	if !OnDemandAllowedAt(warm, r, 0) {
		t.Fatal("promoted play refused")
	}
	for _, p := range []string{IndexerPurposeConsole, IndexerPurposeTest, IndexerPurposeAvailability, IndexerPurposeDetail} {
		if OnDemandAllowedAt(WithIndexerPurpose(warm, p), r, 0) {
			t.Fatalf("playback child opened for %s", p)
		}
	}
	if !OnDemandAllowedAt(WithIndexerPurpose(warm, IndexerPurposeRescue), r, 0) {
		t.Fatal("play rescue refused")
	}
}
