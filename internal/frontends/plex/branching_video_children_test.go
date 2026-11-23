package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type plexBranchingConcertExperience struct {
	*kindExperience
	concert        core.Title
	artistOverride *core.Title
}

func newPlexBranchingConcertExperience() *plexBranchingConcertExperience {
	return &plexBranchingConcertExperience{
		kindExperience: newKindExperience(),
		concert: core.Title{
			Key:         core.PluginKey(109),
			Type:        core.TypeMovie,
			Title:       "Live at Newport 1958",
			Year:        1958,
			Runtime:     3600 * time.Second,
			KindLevel:   "concert",
			MediaClass:  core.MediaClassVideo,
			MediaRole:   "concert",
			ParentKey:   core.PluginKey(101),
			ParentTitle: "Artist One",
			LibraryID:   7,
		},
	}
}

func (e *plexBranchingConcertExperience) Item(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
	if req.Key == e.concert.Key {
		return frontend.ItemDetail{Title: e.concert}, nil
	}
	if e.artistOverride != nil && req.Key == e.artistOverride.Key {
		return frontend.ItemDetail{Title: *e.artistOverride}, nil
	}
	return e.kindExperience.Item(context.Background(), req)
}

func (e *plexBranchingConcertExperience) Children(_ context.Context, _ *core.Viewer, _ core.Device, parent core.RatingKey, q frontend.ItemQuery) (core.Page, error) {
	if parent == core.PluginKey(101) {
		return core.Page{
			Items:      []core.Title{kindMusicAlbum(), e.concert},
			Total:      2,
			TotalExact: true,
		}, nil
	}
	return e.kindExperience.Children(context.Background(), nil, core.Device{}, parent, q)
}

// TestPlexBranchingVideoChildUnderMusicArtist proves:
//   - artist /children omits video concerts (keeps albums only, never mislabelled as albums)
//   - artist ?includeExtras=1 attaches video concerts as Plex Extras clips
//   - /library/metadata/{artist}/extras lists video concerts as type="clip"
//   - direct /library/metadata/{concert} renders as type="clip" (not track/album)
func TestPlexBranchingVideoChildUnderMusicArtist(t *testing.T) {
	exp := newPlexBranchingConcertExperience()
	srv := newTestServer(testServerDeps{Experience: exp})
	artistKeyStr := core.PluginKey(101).String()
	concertKeyStr := exp.concert.Key.String()

	// 1. /library/metadata/{artist}/children returns albums only (omits video concert).
	rec := serveKindRoute(t, srv, srv.handleChildren, "/library/metadata/"+artistKeyStr+"/children", map[string]string{"key": artistKeyStr}, &ownerUser)
	if rec.Code != http.StatusOK {
		t.Fatalf("artist /children status = %d %s", rec.Code, rec.Body.String())
	}
	var chResp struct {
		MediaContainer struct {
			Size     int              `json:"size"`
			Metadata []map[string]any `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &chResp); err != nil {
		t.Fatal(err)
	}
	if len(chResp.MediaContainer.Metadata) != 1 || chResp.MediaContainer.Metadata[0]["type"] != "album" {
		t.Fatalf("artist /children = %+v, want single album (video concert omitted)", chResp.MediaContainer.Metadata)
	}

	// 2. /library/metadata/{artist}?includeExtras=1 includes the concert as an Extra clip.
	rec = serveKindRoute(t, srv, srv.handleMetadata, "/library/metadata/"+artistKeyStr+"?includeExtras=1", map[string]string{"key": artistKeyStr}, &ownerUser)
	if rec.Code != http.StatusOK {
		t.Fatalf("artist ?includeExtras=1 status = %d %s", rec.Code, rec.Body.String())
	}
	var artistResp struct {
		MediaContainer struct {
			Metadata []struct {
				Type   string `json:"type"`
				Extras *struct {
					Size     int              `json:"size"`
					Metadata []map[string]any `json:"Metadata"`
				} `json:"Extras"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &artistResp); err != nil {
		t.Fatal(err)
	}
	if len(artistResp.MediaContainer.Metadata) != 1 || artistResp.MediaContainer.Metadata[0].Extras == nil || len(artistResp.MediaContainer.Metadata[0].Extras.Metadata) != 1 {
		t.Fatalf("artist ?includeExtras=1 = %+v, want 1 extra clip", artistResp.MediaContainer.Metadata)
	}
	extra0 := artistResp.MediaContainer.Metadata[0].Extras.Metadata[0]
	if extra0["type"] != "clip" || extra0["title"] != "Live at Newport 1958" || extra0["subtype"] != "concert" {
		t.Fatalf("artist extra[0] = %+v, want clip/concert", extra0)
	}

	// 3. /library/metadata/{artist}/extras lists the concert clip.
	rec = serveKindRoute(t, srv, srv.handleExtras, "/library/metadata/"+artistKeyStr+"/extras", map[string]string{"key": artistKeyStr}, &ownerUser)
	if rec.Code != http.StatusOK {
		t.Fatalf("artist /extras status = %d %s", rec.Code, rec.Body.String())
	}
	var extrasResp struct {
		MediaContainer struct {
			Metadata []map[string]any `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &extrasResp); err != nil {
		t.Fatal(err)
	}
	if len(extrasResp.MediaContainer.Metadata) != 1 || extrasResp.MediaContainer.Metadata[0]["type"] != "clip" || extrasResp.MediaContainer.Metadata[0]["subtype"] != "concert" {
		t.Fatalf("artist /extras = %+v, want 1 concert clip", extrasResp.MediaContainer.Metadata)
	}

	// 4. Direct /library/metadata/{concert} renders as type="clip".
	rec = serveKindRoute(t, srv, srv.handleMetadata, "/library/metadata/"+concertKeyStr, map[string]string{"key": concertKeyStr}, &ownerUser)
	if rec.Code != http.StatusOK {
		t.Fatalf("concert metadata status = %d %s", rec.Code, rec.Body.String())
	}
	var conResp struct {
		MediaContainer struct {
			Metadata []map[string]any `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &conResp); err != nil {
		t.Fatal(err)
	}
	if len(conResp.MediaContainer.Metadata) != 1 || conResp.MediaContainer.Metadata[0]["type"] != "clip" {
		t.Fatalf("concert metadata = %+v, want type=clip", conResp.MediaContainer.Metadata)
	}

	// 5. /library/metadata/{artist}?includeChildren=1 embeds primary-level albums ONLY (type="album",
	//    video concert omitted) and preserves direct childCount vs recursive leafCount.
	artistWithCounts := kindMusicArtist()
	artistWithCounts.PrimaryChildLevel = "album"
	artistWithCounts.ChildCount = 7
	artistWithCounts.LeafCount = 27
	exp.artistOverride = &artistWithCounts

	rec = serveKindRoute(t, srv, srv.handleMetadata, "/library/metadata/"+artistKeyStr+"?includeChildren=1", map[string]string{"key": artistKeyStr}, &ownerUser)
	if rec.Code != http.StatusOK {
		t.Fatalf("artist ?includeChildren=1 status = %d %s", rec.Code, rec.Body.String())
	}
	var incResp struct {
		MediaContainer struct {
			Metadata []struct {
				Type       string `json:"type"`
				ChildCount *int   `json:"childCount"`
				LeafCount  *int   `json:"leafCount"`
				Children   *struct {
					Size     int              `json:"size"`
					Metadata []map[string]any `json:"Metadata"`
				} `json:"Children"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &incResp); err != nil {
		t.Fatal(err)
	}
	if len(incResp.MediaContainer.Metadata) != 1 {
		t.Fatalf("artist ?includeChildren=1 len = %d", len(incResp.MediaContainer.Metadata))
	}
	artMD := incResp.MediaContainer.Metadata[0]
	if artMD.ChildCount == nil || *artMD.ChildCount != 7 || artMD.LeafCount == nil || *artMD.LeafCount != 27 {
		t.Fatalf("artist counts = childCount:%v leafCount:%v, want 7 / 27", artMD.ChildCount, artMD.LeafCount)
	}
	if artMD.Children == nil || len(artMD.Children.Metadata) != 1 || artMD.Children.Metadata[0]["type"] != "album" {
		t.Fatalf("artist ?includeChildren=1 Children = %+v, want single album (video concert omitted)", artMD.Children)
	}
}
