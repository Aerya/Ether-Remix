package pluginitems

import (
	"context"
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

func TestSavePartsStableSeqAndDeleteCleanup(t *testing.T) {
	ctx := context.Background()
	svc, _ := openTestService(t, Options{})
	decl := plugins.MediaKindMeta{
		Key:   "audiobooks",
		Label: "Audiobooks",
		Media: "audiobook",
		Levels: []plugins.KindLevel{
			{Key: "book", Label: "Book", Playable: true},
		},
	}
	if _, err := svc.UpsertKind(ctx, "plug.audio", 10, decl, false); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	if _, err := svc.KindUpsert(ctx, "plug.audio", "audiobooks", []core.KindItem{{
		ID:       "dune",
		Level:    "book",
		Title:    "Dune",
		Playable: true,
	}}); err != nil {
		t.Fatalf("KindUpsert: %v", err)
	}
	ref := core.NewItemRef("plug.audio", "audiobooks", "dune")
	fi0, fi1, fi2 := 0, 1, 2

	first, err := svc.SaveParts(ctx, ref, []core.KindItemPart{
		{Name: "Part 1", MIME: "audio/mpeg", Size: 1000, DurationSec: 600, FileIndex: &fi0},
		{Name: "Part 2", MIME: "audio/mpeg", Size: 2000, DurationSec: 700, FileIndex: &fi1},
		{Name: "Part 3", MIME: "audio/mpeg", Size: 3000, DurationSec: 800, FileIndex: &fi2},
	}, "resolve")
	if err != nil {
		t.Fatalf("SaveParts(1): %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("len(first) = %d, want 3", len(first))
	}
	if first[0].Key == first[1].Key || first[1].Key == first[2].Key || !first[0].Key.IsPluginKey() {
		t.Fatalf("invalid part keys: %+v", first)
	}
	if first[1].FileIndex == nil || *first[1].FileIndex != 1 {
		t.Fatalf("part 1 FileIndex = %v, want 1", first[1].FileIndex)
	}

	// Re-resolve with updated metadata and 2 parts: preserves seq for 0..1 and prunes 2.
	second, err := svc.SaveParts(ctx, ref, []core.KindItemPart{
		{Name: "Chapter 1", MIME: "audio/mp4", Size: 1100, DurationSec: 610, FileIndex: &fi0},
		{Name: "Chapter 2", MIME: "audio/mp4", Size: 2200, DurationSec: 710, FileIndex: &fi1},
	}, "resolve")
	if err != nil {
		t.Fatalf("SaveParts(2): %v", err)
	}
	if len(second) != 2 || second[0].Seq != first[0].Seq || second[1].Seq != first[1].Seq {
		t.Fatalf("seq did not stay stable: first=%+v second=%+v", first, second)
	}
	if second[0].Name != "Chapter 1" || second[0].MIME != "audio/mp4" {
		t.Fatalf("metadata not updated: %+v", second[0])
	}

	// Pruned part key is gone; surviving keys look up via GetPartByKey & GetPartsByKeys.
	if _, _, _, err := svc.GetPartByKey(ctx, first[2].Key); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("pruned part lookup err = %v, want ErrNotFound", err)
	}
	p0, parent, libID, err := svc.GetPartByKey(ctx, second[0].Key)
	if err != nil || p0.Name != "Chapter 1" || parent.ID != "dune" || libID != 10 {
		t.Fatalf("GetPartByKey = (%+v, %+v, %d, %v)", p0, parent, libID, err)
	}
	batched, err := svc.GetPartsByKeys(ctx, []core.RatingKey{second[0].Key, second[1].Key})
	if err != nil || len(batched) != 2 {
		t.Fatalf("GetPartsByKeys = (%+v, %v)", batched, err)
	}
	counts, err := svc.CountPartsMany(ctx, []core.ItemRef{ref})
	if err != nil || counts[ref] != 2 {
		t.Fatalf("CountPartsMany = (%+v, %v)", counts, err)
	}

	// DeleteItems cleans up plugin_item_parts.
	if err := svc.DeleteItems(ctx, "plug.audio", "audiobooks", []string{"dune"}); err != nil {
		t.Fatalf("DeleteItems: %v", err)
	}
	remaining, err := svc.ListParts(ctx, ref)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("parts left after DeleteItems: %+v, err=%v", remaining, err)
	}
}
