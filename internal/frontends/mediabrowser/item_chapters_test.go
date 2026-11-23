package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// A multi-part audiobook knows its chapters from its parts before any source
// is probed. The Jellyfin item detail must list them: apps read chapter names
// and start positions from the item, not from a resolved version.
func TestJellyfinItemDetailCarriesItemLevelChapters(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1}
	bookKey := core.PluginKey(501)
	exp := frontendtest.Experience{
		ItemFunc: func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
			return frontend.ItemDetail{
				Title: frontend.Title{Key: bookKey, Type: frontend.TypeMovie, Title: "Adventures of Sherlock Holmes", MediaClass: core.MediaClassAudiobook, MBItemType: "AudioBook"},
				Chapters: []core.Chapter{
					{ID: 0, Title: "A Scandal in Bohemia", Start: 0, End: 50 * time.Minute},
					{ID: 1, Title: "The Red-Headed League", Start: 50 * time.Minute, End: 95 * time.Minute},
				},
			}, nil
		},
	}
	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{
		Identity:     ident,
		Experience:   exp,
		Capabilities: frontendtest.Capabilities{},
		SurfaceURL:   func(context.Context, *http.Request) (string, error) { return "https://ether.example/jellyfin", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/jellyfin/Items/"+ids.RatingKeyGUID(bookKey), nil)
	req.Header.Set("X-Emby-Token", "grant")
	rec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("item detail = (%d) %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Chapters []struct {
			Name               string
			StartPositionTicks int64
			ImageDateModified  *string
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2: %s", len(got.Chapters), rec.Body.String())
	}
	second := got.Chapters[1]
	if second.Name != "The Red-Headed League" || second.StartPositionTicks != (50*time.Minute).Nanoseconds()/100 || second.ImageDateModified == nil {
		t.Fatalf("second chapter = %+v", second)
	}
}
