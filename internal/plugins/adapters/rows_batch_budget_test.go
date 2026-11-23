package adapters_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPluginLibraryRowsBatchesShelvesAndDeduplicatesRequestReads(t *testing.T) {
	ctx := context.Background()
	h := newKindTestHarness(t, 2*time.Second, nil)

	const pluginID = "community.example.shelves"
	shelves := []any{
		map[string]any{"key": "newest", "label": "Newest", "sort": "released", "direction": "desc"},
		map[string]any{"key": "added", "label": "Recently Added", "sort": "added", "direction": "desc"},
		map[string]any{"key": "top-rated", "label": "Top Rated", "sort": "rating", "direction": "desc"},
		map[string]any{"key": "by-title", "label": "Alphabetical", "sort": "title", "direction": "asc"},
	}
	// Add 24 tag-filtered shelves (including 18 multi-value OR shelves) to mirror
	// a large declared-shelf library.
	for i := 0; i < 24; i++ {
		var vals []string
		if i < 18 {
			vals = []string{fmt.Sprintf("tag-%02d", i%12), fmt.Sprintf("tag-%02d", (i+3)%12), "popular"}
		} else {
			vals = []string{fmt.Sprintf("tag-%02d", i%12)}
		}
		shelves = append(shelves, map[string]any{
			"key":       fmt.Sprintf("shelf-%02d", i),
			"label":     fmt.Sprintf("Shelf %02d", i),
			"sort":      "released",
			"direction": "desc",
			"filters":   map[string][]string{"tags": vals},
		})
	}

	manifest := map[string]any{
		"id":      pluginID,
		"version": "1.0.0",
		"name":    "Dense Shelves Plugin",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Single-level content plugin with 28 declared shelves",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"contributes": map[string]any{
				"mediaKinds": []any{
					map[string]any{
						"key":   "clip",
						"label": "Clips",
						"media": "video",
						"library": map[string]any{
							"name":    "Clips",
							"card":    "landscape",
							"shelves": shelves,
						},
						"levels": []any{
							map[string]any{"key": "clip", "label": "Clip", "playable": true},
						},
						"fields": []any{
							map[string]any{"key": "tags", "type": "strings", "label": "Tags", "indexed": true},
							map[string]any{"key": "cast", "type": "people", "label": "Cast", "indexed": true},
						},
						"present": map[string]any{
							"vela": map[string]any{"type": "movies"},
						},
						"browse": map[string]any{
							"sorts":  []string{"title", "added", "released", "rating"},
							"facets": []string{"tags", "cast"},
							"search": true,
						},
						"sync": map[string]any{"mode": "push"},
					},
				},
			},
		},
	}

	installStudioManifest(t, h.mgr, manifest, nil)
	if err := h.mgr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var lib core.Library
	for _, l := range mustListLibs(t, h.libs) {
		if l.PluginID == pluginID {
			lib = l
		}
	}
	if lib.ID == 0 {
		t.Fatal("expected plugin library")
	}

	items := make([]core.KindItem, 0, 120)
	for i := 0; i < 120; i++ {
		tags := []string{fmt.Sprintf("tag-%02d", i%12), fmt.Sprintf("tag-%02d", (i+5)%12)}
		if i%2 == 0 {
			tags = append(tags, "popular")
		}
		tagsRaw, _ := json.Marshal(tags)
		items = append(items, core.KindItem{
			ID:       fmt.Sprintf("clip-%03d", i),
			Level:    "clip",
			Title:    fmt.Sprintf("Clip %03d", i),
			AddedAt:  int64(1_700_000_000 + i*100),
			Released: int64(1_600_000_000 + i*1000),
			Rating:   float64(i%10) + 0.5,
			Playable: true,
			Art:      []core.KindArtRef{{Role: "poster", URL: fmt.Sprintf("https://img.example.test/%03d.jpg", i)}},
			Fields:   map[string]json.RawMessage{"tags": tagsRaw},
		})
	}
	if _, err := h.items.KindUpsert(ctx, pluginID, "clip", items); err != nil {
		t.Fatalf("KindUpsert: %v", err)
	}

	exp := h.experienceFor(t, "jellyfin")
	owner := &core.Viewer{ID: 1, IsOwner: true}
	libIDStr := strconv.FormatInt(int64(lib.ID), 10)

	// Warm caches (KindRecord, empty plugin_item_parts) like a steady-state server.
	if _, err := exp.PluginLibraryRows(ctx, owner, core.Device{Product: "Vela"}, libIDStr); err != nil {
		t.Fatalf("warmup PluginLibraryRows: %v", err)
	}

	// Measure the full /rows request sequence: PluginLibraryRows + UserData + PageArtworkTitles.
	reqCtx := frontend.WithRequestMemo(reqmemo.With(ctx))
	reqCtx, stats := store.WithQueryStats(reqCtx)

	rows, err := exp.PluginLibraryRows(reqCtx, owner, core.Device{Product: "Vela"}, libIDStr)
	if err != nil {
		t.Fatalf("PluginLibraryRows: %v", err)
	}
	if len(rows) != 28 {
		t.Fatalf("PluginLibraryRows len = %d, want 28", len(rows))
	}

	var keys []core.RatingKey
	seen := map[core.RatingKey]struct{}{}
	for _, row := range rows {
		if len(row.Items) == 0 {
			t.Fatalf("row %q unexpectedly empty", row.ID)
		}
		for _, it := range row.Items {
			if _, ok := seen[it.Key]; !ok {
				seen[it.Key] = struct{}{}
				keys = append(keys, it.Key)
			}
		}
	}
	if _, err := exp.UserData(reqCtx, owner, keys); err != nil {
		t.Fatalf("UserData: %v", err)
	}
	artTitles, err := exp.PageArtworkTitles(reqCtx, owner, keys)
	if err != nil {
		t.Fatalf("PageArtworkTitles: %v", err)
	}
	if len(artTitles) != len(keys) {
		t.Fatalf("PageArtworkTitles len = %d, want %d", len(artTitles), len(keys))
	}

	// Bounded ceiling check: 28 declared shelves + UserData + PageArtworkTitles
	// must stay O(1) in SQL statements (well below 40; unbatched was ~1,200).
	const ceiling = 40
	if got := stats.Queries(); got > ceiling {
		t.Fatalf("28-shelf /rows sequence executed %d SQL queries (ceiling %d); top statements: %+v", got, ceiling, stats.Statements()[:min(5, len(stats.Statements()))])
	}
	t.Logf("28-shelf /rows + UserData + PageArtworkTitles: queries=%d rows=%d uniqueKeys=%d", stats.Queries(), stats.Rows(), len(keys))
}
