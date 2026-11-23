package plex

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPlexAudioQueryParamsBookRefusalAndReadingOffset(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	ctx := context.Background()
	owner := &core.User{ID: 1, Username: "owner"}

	// 1. playableTitle refuses Book items (both MediaClassBook and MBItemType="Book").
	book1 := &core.Title{Key: core.PluginKey(501), Type: core.TypeMovie, MediaClass: core.MediaClassBook}
	book2 := &core.Title{Key: core.PluginKey(502), Type: core.TypeMovie, MBItemType: "Book"}
	track := &core.Title{Key: core.PluginKey(503), Type: core.TypeMovie, MediaClass: core.MediaClassMusic}
	if f.srv.playableTitle(ctx, owner, book1) {
		t.Fatal("playableTitle accepted MediaClassBook")
	}
	if f.srv.playableTitle(ctx, owner, book2) {
		t.Fatal("playableTitle accepted MBItemType=Book")
	}
	if !f.srv.playableTitle(ctx, owner, track) {
		t.Fatal("playableTitle rejected music track")
	}

	// 2. applyDecisionQuery parses musicBitrate, maxAudioChannels, and audioCodec.
	q := url.Values{
		"musicBitrate":     {"192"},
		"maxAudioChannels": {"2"},
		"audioCodec":       {"mp3"},
	}
	var caps core.ClientCaps
	applyDecisionQuery(&caps, q)
	if caps.MaxBitrateKbps != 192 {
		t.Fatalf("MaxBitrateKbps = %d, want 192", caps.MaxBitrateKbps)
	}
	foundChannels := false
	for _, lim := range caps.AudioLimits {
		if lim.Name == "audio.channels" && lim.Value == 2 {
			foundChannels = true
		}
	}
	if !foundChannels {
		t.Fatalf("AudioLimits = %+v, want audio.channels upperBound 2", caps.AudioLimits)
	}
	foundMP3 := false
	for _, c := range caps.TranscodeAudioCodecs {
		if c == "mp3" {
			foundMP3 = true
		}
	}
	if !foundMP3 {
		t.Fatalf("TranscodeAudioCodecs = %v, want mp3", caps.TranscodeAudioCodecs)
	}

	// 3. Reading progress fraction maps onto Plex ViewOffset when ViewOffset is unset.
	book := core.Title{
		Key:        core.PluginKey(504),
		Type:       core.TypeMovie,
		Title:      "Dune",
		MediaClass: core.MediaClassBook,
		Runtime:    100 * time.Minute,
	}
	f.catalog.titles[book.Key] = book
	watch := state.New(store.OpenTest(t), state.Options{})
	f.srv.setState(watch)
	if err := watch.SetReadingProgress(ctx, owner.ID, book.Key, core.ReadingProgress{Locator: "epubcfi(/6/4)", Fraction: 0.25}); err != nil {
		t.Fatalf("SetReadingProgress: %v", err)
	}
	st, err := watch.Get(ctx, owner.ID, book.Key)
	if err != nil {
		t.Fatalf("watch.Get: %v", err)
	}
	pr := &Request{User: owner}
	m := f.srv.metadata(ctx, pr, &book, renderOpts{state: map[core.RatingKey]core.WatchStatus{book.Key: st}})
	wantOffset := int64(25 * time.Minute / time.Millisecond)
	if m.ViewOffset != wantOffset {
		t.Fatalf("Plex ViewOffset from reading fraction = %d, want %d", m.ViewOffset, wantOffset)
	}

	// 4. /music/:/transcode/universal/decision rejects Book without probing.
	r := f.get(t, "/music/:/transcode/universal/decision?path="+url.QueryEscape("/library/metadata/"+book.Key.String())+"&protocol=http&musicBitrate=192")
	if r.Code == http.StatusOK {
		t.Fatalf("music transcode decision on Book returned 200: %s", r.Body.String())
	}
}
