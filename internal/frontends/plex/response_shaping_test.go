package plex

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Rows that skip building the Media a client excluded must encode exactly
// as rows that build it and leave it out, in both formats.
func TestExcludedMediaSkipAnswersSameBytes(t *testing.T) {
	f := newFixture()
	paths := []string{
		"/library/sections/1/all?excludeElements=Media,Genre&excludeFields=summary",
		"/library/sections/2/allLeaves?excludeElements=Media&includeGuids=1",
		"/library/metadata/" + bbS1.Key.String() + "/children?excludeElements=Media",
		"/library/metadata/" + fightClub.Key.String() + "," + alien.Key.String() + "?excludeElements=Media,Role,Director,Writer,Producer,Country,Guid,Rating,Image,UltraBlurColors,Location,Similar,Field&excludeFields=summary,tagline",
	}
	for _, accept := range [][]string{nil, jsonAccept} {
		for _, p := range paths {
			listMediaSkip = false
			want := f.getWarm(t, p, accept...).Body.String()
			listMediaSkip = true
			got := f.getWarm(t, p, accept...).Body.String()
			if !strings.Contains(want, "mediaCount") {
				t.Errorf("%s: no row stood in for its excluded Media: %s", p, want)
			}
			if got != want {
				t.Errorf("%s (accept %v): skipping excluded Media changed the body\nskip: %s\nfull: %s", p, accept, got, want)
			}
		}
	}
}

// Batched metadata with excludeElements=Media still builds Media/stream
// features when the caller explicitly asks for markers or chapters, and
// unexcluded batch renders must not populate playIDs for unpinned items.
func TestBatchMetadataExcludeMediaAndPlayIDInvariants(t *testing.T) {
	f := newFixture()
	f.playback.mu.Lock()
	f.playback.sessions["probed"] = &core.PlaySession{
		ID:       "probed",
		UserID:   ownerUser.ID,
		ClientID: "test-client",
		Key:      fightClub.Key,
		Info: core.MediaInfo{
			Duration:  139 * time.Minute,
			Container: "mkv",
			Streams:   []core.StreamInfo{{Type: "video", Codec: "h264", Width: 1920, Height: 1080}},
		},
	}
	f.playback.mu.Unlock()
	f.srv.setMarkers(fakeMarkers{{Type: "credits", StartMS: 7_800_000, EndMS: 8_340_000, Final: true}})
	batchKeys := fightClub.Key.String() + "," + alien.Key.String()

	rec := f.getWarm(t, "/library/metadata/"+batchKeys+"?excludeElements=Media&includeMarkers=1", append([]string{"X-Plex-Client-Identifier", "test-client"}, jsonAccept...)...)
	var body struct {
		MediaContainer struct {
			Metadata []struct {
				RatingKey string `json:"ratingKey"`
				Marker    []struct {
					Type string `json:"type"`
				} `json:"Marker"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.MediaContainer.Metadata) != 2 || len(body.MediaContainer.Metadata[0].Marker) == 0 {
		t.Fatalf("expected markers when includeMarkers=1 even with excludeElements=Media: %s", rec.Body.String())
	}

	// Unexcluded batch from a fresh sync client must not allocate playIDs
	// entries for unpinned items (prevents evicting real clients during sync).
	f.srv.playIDs.mu.Lock()
	before := len(f.srv.playIDs.m)
	f.srv.playIDs.mu.Unlock()
	syncHeaders := append([]string{"X-Plex-Client-Identifier", "sync-client"}, jsonAccept...)
	if resp := f.getWarm(t, "/library/metadata/"+batchKeys, syncHeaders...); resp.Code != 200 {
		t.Fatalf("batch status = %d", resp.Code)
	}
	f.srv.playIDs.mu.Lock()
	after := len(f.srv.playIDs.m)
	f.srv.playIDs.mu.Unlock()
	if after != before {
		t.Fatalf("batched metadata allocated %d new playIDs entries for unpinned sync client", after-before)
	}
}

// A library listing honours excludeFields and PMS's allow-listed
// excludeElements: an excluded Part leaves Media@partCount, and names a PMS
// ignores (Image) are ignored.
func TestListingHonoursPMSExclusions(t *testing.T) {
	f := newFixture()
	full := f.getWarm(t, "/library/sections/1/all", jsonAccept...).Body.String()
	got := f.getWarm(t, "/library/sections/1/all?excludeFields=summary&excludeElements=Part,Genre,Image", jsonAccept...).Body.String()
	for _, want := range []string{`"summary"`, `"Part"`, `"Genre"`, `"Image"`} {
		if !strings.Contains(full, want) {
			t.Fatalf("the unexcluded listing lacks %s, so the check proves nothing: %s", want, full)
		}
	}
	for _, gone := range []string{`"summary"`, `"Part"`, `"Genre"`} {
		if strings.Contains(got, gone) {
			t.Errorf("excluded %s is still sent: %s", gone, got)
		}
	}
	if !strings.Contains(got, `"Image"`) || !strings.Contains(got, `"partCount":1`) {
		t.Errorf("want Image kept (PMS ignores it) and Media@partCount: %s", got)
	}
}
