package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func TestJellyfinAndroidTVDetailAndThemeDTOSafety(t *testing.T) {
	// jellyfin-sdk-kotlin's UUIDSerializer rejects uppercase hex with an
	// uncaught IllegalArgumentException.
	if guidLike("0123456789ABCDEF0123456789abcdef") {
		t.Fatal("guidLike accepted uppercase hex")
	}
	if !guidLike("0123456789abcdef0123456789abcdef") || !guidLike("01234567-89ab-cdef-0123-456789abcdef") {
		t.Fatal("guidLike rejected lowercase UUID")
	}

	movieKey := core.MovieKey(759)
	showKey := core.ShowKey(760)
	extraKey := core.ExtraKey(759)
	inst, err := (Jellyfin{MachineID: "test-srv"}).Mount(context.Background(), frontend.Host{
		Identity: frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) {
			return &frontend.Viewer{ID: 1}, nil
		}},
		Themes: &themeCache{},
		ThemeTitle: func(context.Context, *frontend.Viewer, frontend.RatingKey) (*frontend.Title, error) {
			return &frontend.Title{Key: showKey, Title: "Show Theme"}, nil
		},
		Experience: issue759Exp{
			Experience: frontendtest.Experience{
				ItemFunc: func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
					return frontend.ItemDetail{
						Title:  frontend.Title{Key: movieKey, Type: "movie", Title: "The Great Escaper"},
						Extras: []frontend.Extra{{Key: extraKey, ParentKey: movieKey, Kind: "trailer", Title: "Official Trailer", Height: 1080}},
					}, nil
				},
			},
			people: []frontend.PersonDetail{
				{ID: "non-guid-person", Name: "Bad Person"},
				{ID: "0123456789abcdef0123456789abcdef", Name: "Good Person"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	get := func(path string) map[string]any {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("X-Emby-Token", "tok")
		r.Header.Set("X-Emby-Device-Id", "shield")
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("GET %s invalid json: %v", path, err)
		}
		return out
	}

	// 1. /LocalTrailers must carry non-null UserData, ImageTags, BackdropImageTags.
	r := httptest.NewRequest(http.MethodGet, "/Items/"+ids.RatingKeyGUID(movieKey)+"/LocalTrailers", nil)
	r.Header.Set("X-Emby-Token", "tok")
	r.Header.Set("X-Emby-Device-Id", "shield")
	w := httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("LocalTrailers = %d: %s", w.Code, w.Body.String())
	}
	var trailers []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &trailers); err != nil || len(trailers) != 1 {
		t.Fatalf("trailers = %v (%v)", trailers, err)
	}
	for _, field := range []string{"UserData", "ImageTags", "BackdropImageTags"} {
		if trailers[0][field] == nil {
			t.Fatalf("extraItem missing %s: %+v", field, trailers[0])
		}
	}

	// 2. /ThemeMedia with uppercase path ID emits canonical lowercase OwnerId
	// and complete MediaSource + BaseItemDto fields.
	upperShowID := strings.ToUpper(ids.RatingKeyGUID(showKey))
	tm := get("/Items/" + upperShowID + "/ThemeMedia")
	songsResult := tm["ThemeSongsResult"].(map[string]any)
	if got := songsResult["OwnerId"].(string); got != ids.RatingKeyGUID(showKey) {
		t.Fatalf("ThemeSongsResult.OwnerId = %q, want canonical lowercase %q", got, ids.RatingKeyGUID(showKey))
	}
	song := songsResult["Items"].([]any)[0].(map[string]any)
	for _, field := range []string{"ServerId", "UserData", "ImageTags", "BackdropImageTags"} {
		if song[field] == nil {
			t.Fatalf("theme song missing %s: %+v", field, song)
		}
	}
	src := song["MediaSources"].([]any)[0].(map[string]any)
	for _, field := range []string{"Type", "RequiresOpening", "RequiresClosing", "ReadAtNativeFramerate", "IgnoreDts", "IgnoreIndex", "GenPtsInput"} {
		if _, ok := src[field]; !ok {
			t.Fatalf("theme MediaSource missing required Kotlin SDK field %s: %+v", field, src)
		}
	}

	// 3. Search hints for people omit non-GUID person IDs on Jellyfin.
	hints := get("/Search/Hints?searchTerm=person&includePeople=true&includeItemTypes=Person")
	items := hints["SearchHints"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["Name"] != "Good Person" {
		t.Fatalf("person search hints leaked non-GUID person: %+v", items)
	}
}

type issue759Exp struct {
	frontendtest.Experience
	people []frontend.PersonDetail
}

func (p issue759Exp) People(context.Context, *frontend.Viewer, frontend.Device, frontend.PeopleQuery) ([]frontend.PersonDetail, error) {
	return p.people, nil
}

func (issue759Exp) Person(context.Context, *frontend.Viewer, frontend.Device, string, frontend.PersonReadOptions) (frontend.PersonDetail, error) {
	return frontend.PersonDetail{}, frontend.ErrNotFound
}

func (issue759Exp) PersonCredit(context.Context, *frontend.Viewer, frontend.Device, frontend.RatingKey, int64) (frontend.PersonDetail, error) {
	return frontend.PersonDetail{}, frontend.ErrNotFound
}
