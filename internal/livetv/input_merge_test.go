package livetv

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestChannelInputMergeAndProviderPriority(t *testing.T) {
	ctx := context.Background()
	s := testService(t, Options{})
	owner := &core.User{ID: 1, IsOwner: true}

	alphaM3U := strings.Join([]string{
		"#EXTM3U",
		`#EXTINF:-1 tvg-id="espn.us" tvg-chno="101" group-title="Sports",ESPN HD`,
		"https://alpha.invalid/live/espn-hd.m3u8",
		`#EXTINF:-1 tvg-id="espn.us" tvg-chno="102" group-title="Sports",US | ESPN 4K`,
		"https://alpha.invalid/live/espn-4k.m3u8",
		`#EXTINF:-1 tvg-id="espndeportes.us" tvg-chno="103" group-title="Sports",ESPN Deportes HD`,
		"https://alpha.invalid/live/espn-deportes.m3u8",
	}, "\n") + "\n"

	bravoM3U := strings.Join([]string{
		"#EXTM3U",
		`#EXTINF:-1 tvg-id="espn.us" tvg-chno="201" group-title="US Sports",US | ESPN SD`,
		"https://bravo.invalid/live/espn-sd.m3u8",
		`#EXTINF:-1 tvg-id="espn.us" tvg-chno="202" group-title="US Sports",US | ESPN FHD`,
		"https://bravo.invalid/live/espn-fhd-v1.m3u8",
	}, "\n") + "\n"

	if _, err := s.ImportSource(ctx, Source{
		ID:       "src-a",
		Name:     "Alpha TV",
		Playlist: alphaM3U,
		Auth:     &SourceAuth{Type: "headers", Headers: map[string]string{"X-Provider-Token": "alpha-secret"}},
	}); err != nil {
		t.Fatalf("import alpha: %v", err)
	}
	if _, err := s.ImportSource(ctx, Source{
		ID:       "src-b",
		Name:     "Bravo TV",
		Playlist: bravoM3U,
		Auth:     &SourceAuth{Type: "headers", Headers: map[string]string{"X-Provider-Token": "bravo-secret"}},
	}); err != nil {
		t.Fatalf("import bravo: %v", err)
	}

	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}

	// Set Bravo TV (#1) ahead of Alpha TV (#2) via the owner HTTP endpoint.
	if rec := call(http.MethodPut, "/sources/order", `{"ids":["src-b","src-a"]}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT /sources/order = %d: %s", rec.Code, rec.Body.String())
	} else {
		var ordered []Source
		if err := json.Unmarshal(rec.Body.Bytes(), &ordered); err != nil {
			t.Fatal(err)
		}
		if len(ordered) != 2 || ordered[0].ID != "src-b" || ordered[1].ID != "src-a" {
			t.Fatalf("ordered sources = %+v", ordered)
		}
	}

	stored, err := s.Channels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var target Channel
	for _, c := range stored {
		if c.SourceID == "src-a" && c.Name == "ESPN HD" {
			target = c
			break
		}
	}
	if target.ID == "" {
		t.Fatalf("target channel not found in %+v", stored)
	}

	// Preview validates regex errors and returns matched feeds in priority order.
	if bad := call(http.MethodPost, "/channels/merge-preview", `{"channelId":"`+target.ID+`","rules":[{"field":"name","op":"matches","value":"("}]}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid regex preview = %d: %s", bad.Code, bad.Body.String())
	}
	previewBody := `{"channelId":"` + target.ID + `","match":"all","rules":[{"field":"name","op":"matches","value":"(?i)^(?:us\\s*\\|\\s*)?espn\\b"},{"field":"name","op":"notMatches","value":"(?i)deportes"}]}`
	if rec := call(http.MethodPost, "/channels/merge-preview", previewBody); rec.Code != http.StatusOK {
		t.Fatalf("merge-preview = %d: %s", rec.Code, rec.Body.String())
	} else {
		var prev ChannelMergePreviewResult
		if err := json.Unmarshal(rec.Body.Bytes(), &prev); err != nil {
			t.Fatal(err)
		}
		if prev.MatchCount != 3 || prev.TotalFeeds != 4 {
			t.Fatalf("preview counts = %+v", prev)
		}
		if prev.Inputs[0].SourceName != "Bravo TV" || prev.Inputs[0].Quality != "FHD" || prev.Inputs[0].State != "unchecked" {
			t.Fatalf("preview first input = %+v", prev.Inputs[0])
		}
	}

	// Save merge rules on target channel (hideDuplicates defaults to true).
	target.Name = "ESPN"
	target.InputMerge = &ChannelInputMerge{
		Match: "all",
		Rules: []OwnerGroupRule{
			{Field: "name", Op: "matches", Value: `(?i)^(?:us\s*\|\s*)?espn\b`},
			{Field: "name", Op: "notMatches", Value: `(?i)deportes`},
		},
	}
	saved, err := s.SaveChannel(ctx, target)
	if err != nil {
		t.Fatalf("save target with merge rules: %v", err)
	}

	gotURLs := func(c Channel) []string {
		out := make([]string, len(c.StreamSources))
		for i, src := range c.StreamSources {
			out[i] = src.URL
		}
		return out
	}
	wantInitial := []string{
		"https://bravo.invalid/live/espn-fhd-v1.m3u8", // Bravo (#1), FHD
		"https://bravo.invalid/live/espn-sd.m3u8",     // Bravo (#1), SD
		"https://alpha.invalid/live/espn-4k.m3u8",     // Alpha (#2), 4K
		"https://alpha.invalid/live/espn-hd.m3u8",     // Alpha (#2), HD (target's own playlist feed via TvgName)
	}
	if !reflect.DeepEqual(gotURLs(saved), wantInitial) {
		t.Fatalf("merged StreamSources = %v, want %v", gotURLs(saved), wantInitial)
	}
	if saved.StreamURL != wantInitial[0] {
		t.Fatalf("active StreamURL = %q, want %q", saved.StreamURL, wantInitial[0])
	}

	// orderedSourceCandidates yields deterministic primary/backup failover, and
	// each input carries its own provider's credentials.
	candidates := s.orderedSourceCandidates(saved)
	candURLs := make([]string, len(candidates))
	for i, c := range candidates {
		candURLs[i] = c.URL
	}
	if !reflect.DeepEqual(candURLs, wantInitial) {
		t.Fatalf("orderedSourceCandidates = %v, want %v", candURLs, wantInitial)
	}
	if h, err := s.upstreamHeaders(ctx, saved, candidates[0]); err != nil || h.Get("X-Provider-Token") != "bravo-secret" {
		t.Fatalf("bravo merged input headers = %v, err=%v", h, err)
	}
	if h, err := s.upstreamHeaders(ctx, saved, candidates[3]); err != nil || h.Get("X-Provider-Token") != "alpha-secret" {
		t.Fatalf("alpha primary input headers = %v, err=%v", h, err)
	}

	// Matched duplicates are hidden as standalone channels (lineup + viewer apps),
	// NOT deleted from storage.
	allStored, err := s.Channels(ctx)
	if err != nil || len(allStored) != 5 {
		t.Fatalf("stored channels = %d (%v), want 5 kept in DB", len(allStored), err)
	}
	allowed, err := s.AllowedChannels(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	var allowedNames []string
	for _, c := range allowed {
		allowedNames = append(allowedNames, c.Name)
	}
	slices.Sort(allowedNames)
	if !reflect.DeepEqual(allowedNames, []string{"ESPN", "ESPN Deportes HD"}) {
		t.Fatalf("AllowedChannels = %v, want only [ESPN, ESPN Deportes HD]", allowedNames)
	}
	page, err := s.ChannelPage(ctx, ChannelPageQuery{Limit: 50})
	if err != nil || page.Total != 2 || page.Counts.All != 2 {
		t.Fatalf("ChannelPage total=%d counts=%+v err=%v, want 2 standalone channels", page.Total, page.Counts, err)
	}

	// Later refresh of Bravo TV rotates the existing FHD stream URL and adds a
	// brand-new UHD/4K feed, which attaches automatically ahead of FHD and hides
	// its standalone channel entry.
	bravoRefreshedM3U := strings.Join([]string{
		"#EXTM3U",
		`#EXTINF:-1 tvg-id="espn.us" tvg-chno="201" group-title="US Sports",US | ESPN SD`,
		"https://bravo.invalid/live/espn-sd.m3u8",
		`#EXTINF:-1 tvg-id="espn.us" tvg-chno="202" group-title="US Sports",US | ESPN FHD`,
		"https://bravo.invalid/live/espn-fhd-v2.m3u8",
		`#EXTINF:-1 tvg-id="espn.us" tvg-chno="203" group-title="US Sports",US | ESPN UHD`,
		"https://bravo.invalid/live/espn-uhd.m3u8",
	}, "\n") + "\n"
	if _, err := s.ImportSource(ctx, Source{
		ID:       "src-b",
		Name:     "Bravo TV",
		Playlist: bravoRefreshedM3U,
	}); err != nil {
		t.Fatalf("refresh bravo: %v", err)
	}

	reloaded, err := s.Channel(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantAfterRefresh := []string{
		"https://bravo.invalid/live/espn-uhd.m3u8",    // Bravo (#1), 4K/UHD (new feed!)
		"https://bravo.invalid/live/espn-fhd-v2.m3u8", // Bravo (#1), FHD (URL rotated via mergeImportedStreams!)
		"https://bravo.invalid/live/espn-sd.m3u8",     // Bravo (#1), SD
		"https://alpha.invalid/live/espn-4k.m3u8",     // Alpha (#2), 4K
		"https://alpha.invalid/live/espn-hd.m3u8",     // Alpha (#2), HD
	}
	if !reflect.DeepEqual(gotURLs(reloaded), wantAfterRefresh) {
		t.Fatalf("after refresh StreamSources = %v, want %v", gotURLs(reloaded), wantAfterRefresh)
	}
	allowedAfter, err := s.AllowedChannels(ctx, owner)
	if err != nil || len(allowedAfter) != 2 {
		t.Fatalf("AllowedChannels after refresh = %+v (%v), want 2", allowedAfter, err)
	}

	// Flipping provider priority promotes Alpha TV feeds ahead of Bravo TV feeds.
	if _, err := s.SetSourceOrder(ctx, []string{"src-a", "src-b"}); err != nil {
		t.Fatal(err)
	}
	reordered, err := s.Channel(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantReordered := []string{
		"https://alpha.invalid/live/espn-4k.m3u8",
		"https://alpha.invalid/live/espn-hd.m3u8",
		"https://bravo.invalid/live/espn-uhd.m3u8",
		"https://bravo.invalid/live/espn-fhd-v2.m3u8",
		"https://bravo.invalid/live/espn-sd.m3u8",
	}
	if !reflect.DeepEqual(gotURLs(reordered), wantReordered) {
		t.Fatalf("after provider reorder StreamSources = %v, want %v", gotURLs(reordered), wantReordered)
	}
}
