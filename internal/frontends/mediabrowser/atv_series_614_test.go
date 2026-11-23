package mediabrowser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// #614: Jellyfin Android TV 0.19.x asks /Shows/Upcoming with the series
// item id as parentId (FullDetailsFragment's Upcoming row). The handler
// only accepted library-view ids and answered 404.
func TestShowsUpcomingAcceptsSeriesParent(t *testing.T) {
	series := core.ShowKey(10)
	future := frontend.Title{Key: core.EpisodeKey(10, 2, 1), Type: "episode", Title: "Premiere", ShowKey: series, Season: 2, Episode: 1, ShowTitle: "Show", Released: time.Now().AddDate(0, 0, 3)}
	var gotQuery frontend.ItemQuery
	var gotKey frontend.RatingKey
	exp := frontendtest.Experience{
		ChildrenFunc: func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, key frontend.RatingKey, q frontend.ItemQuery) (core.Page, error) {
			gotKey, gotQuery = key, q
			return core.Page{Items: []frontend.Title{future}, Total: 1}, nil
		},
		UserDataFunc: func(_ context.Context, _ *frontend.Viewer, keys []frontend.RatingKey) (map[frontend.RatingKey]frontend.WatchStatus, error) {
			out := map[frontend.RatingKey]frontend.WatchStatus{}
			for _, k := range keys {
				out[k] = frontend.WatchStatus{}
			}
			return out, nil
		},
		ItemFunc: func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
			if req.Key == series {
				return frontend.ItemDetail{Title: frontend.Title{Key: series, Type: "show", Title: "Show"}}, nil
			}
			return frontend.ItemDetail{}, core.ErrNotFound
		},
	}
	inst := mountDialect(t, "jellyfin", exp)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/Shows/Upcoming?parentId="+ids.RatingKeyGUID(series)+"&Fields=ChildCount,Overview", nil)
	r.Header.Set("X-Emby-Token", "grant")
	inst.Handler().ServeHTTP(w, r)
	var body struct {
		Items            []map[string]any
		TotalRecordCount int
		StartIndex       int
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Items) != 1 {
		t.Fatalf("upcoming: %d %s", w.Code, w.Body.String())
	}
	item := body.Items[0]
	if item["Type"] != "Episode" || item["SeriesId"] != ids.RatingKeyGUID(series) || item["ParentIndexNumber"] != float64(2) || item["IndexNumber"] != float64(1) {
		t.Fatalf("upcoming episode identity: %v", item)
	}
	data, ok := item["UserData"].(map[string]any)
	if !ok || data["Key"] == nil || data["ItemId"] == nil || data["Played"] == nil || data["PlaybackPositionTicks"] == nil {
		t.Fatalf("upcoming UserData incomplete for Kotlin SDK: %v", item["UserData"])
	}
	if gotKey != series || !gotQuery.Recursive || len(gotQuery.Types) != 1 || gotQuery.Types[0] != core.TypeEpisode || len(gotQuery.Filter.ReleasedFilters) != 1 || gotQuery.Limit <= 0 || gotQuery.Limit > upcomingEpisodesMax {
		t.Fatalf("upcoming query not bounded/indexed: key=%v q=%+v", gotKey, gotQuery)
	}
}

// #614: Android TV uploads its log to /ClientLog/Document after a crash;
// the unmatched route answered 404 on every retry.
func TestClientLogDocumentAcceptsAndDiscards(t *testing.T) {
	inst := mountDialect(t, "jellyfin", frontendtest.Experience{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/ClientLog/Document", strings.NewReader("client log line\n"))
	r.Header.Set("X-Emby-Token", "grant")
	r.Header.Set("Authorization", `MediaBrowser Client="Jellyfin Android TV", Device="Shield", DeviceId="tv", Version="0.19.10", Token="grant"`)
	inst.Handler().ServeHTTP(w, r)
	var body map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatalf("client log: %d %s", w.Code, w.Body.String())
	}
	if name, _ := body["FileName"].(string); !strings.HasSuffix(name, ".log") || !strings.Contains(name, "jellyfin-android-tv") {
		t.Fatalf("client log file name: %v", body)
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/ClientLog/Document", strings.NewReader("x"))
	r.Header.Set("X-Emby-Token", "grant")
	r.ContentLength = 1_000_001
	inst.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize client log: %d %s", w.Code, w.Body.String())
	}
}

// #614: a transient watch-state store failure turned whole /Items pages
// into 503s. Browsing must degrade to unwatched defaults instead.
func TestItemsPageDegradesWhenUserDataFails(t *testing.T) {
	ep := frontend.Title{Key: core.EpisodeKey(10, 1, 1), Type: "episode", Title: "One", ShowKey: core.ShowKey(10), Season: 1, Episode: 1, ShowTitle: "Show"}
	exp := frontendtest.Experience{
		LibraryItemsFunc: func(context.Context, *frontend.Viewer, frontend.Device, string, frontend.ItemQuery) (core.Page, error) {
			return core.Page{Items: []frontend.Title{ep}, Total: 1}, nil
		},
		UserDataFunc: func(context.Context, *frontend.Viewer, []frontend.RatingKey) (map[frontend.RatingKey]frontend.WatchStatus, error) {
			return nil, errors.New("database is locked")
		},
	}
	inst := mountDialect(t, "jellyfin", exp)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/Items?Recursive=true&IncludeItemTypes=Episode", nil)
	r.Header.Set("X-Emby-Token", "grant")
	inst.Handler().ServeHTTP(w, r)
	var body struct {
		Items []map[string]any
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Items) != 1 {
		t.Fatalf("items with failed user data: %d %s", w.Code, w.Body.String())
	}
	if _, ok := body.Items[0]["UserData"].(map[string]any); !ok {
		t.Fatalf("degraded item lost UserData: %v", body.Items[0])
	}
}

// #614: jellyfin-sdk-kotlin requires BaseItemPerson.Id (UUID),
// NameGuidPair.Id (UUID), ChapterInfo.ImageDateModified and a valid
// MediaStream.Type. Any one missing fails decoding of the whole series
// detail response, which is the Android TV crash.
func TestKotlinRequiredNestedFields614(t *testing.T) {
	exp := frontendtest.Experience{
		PersonFacetIDsFunc: func(context.Context, *core.Viewer, core.Device, core.RatingKey, []int64) (map[int64]string, error) {
			return map[int64]string{42: ids.RatingKeyGUID(core.ShowKey(7))}, nil
		},
	}
	i := &instance{protocol: "jellyfin", host: frontend.Host{Experience: exp}}
	r := httptest.NewRequest(http.MethodGet, "/Items/x", nil)
	detail := frontend.ItemDetail{Title: frontend.Title{Key: core.ShowKey(10), Type: "show", Title: "Show",
		Cast: []core.Person{{TMDB: 42, Name: "Known"}, {Name: "No ID anywhere"}, {ID: "not-a-guid", Name: "Bad wire id"}}}}
	people := i.peopleDTO(r, &frontend.Viewer{ID: 1}, "grant", detail)
	if len(people) != 1 {
		t.Fatalf("people kept %d credits, want only the GUID one: %v", len(people), people)
	}
	if id, _ := people[0].(map[string]any)["Id"].(string); !guidLike(id) {
		t.Fatalf("person id not a GUID: %v", people[0])
	}

	item := map[string]any{}
	jellyfinStudioFacts(item, frontend.Title{Type: "show", Studios: []string{"Studio One"}})
	studios, _ := item["Studios"].([]any)
	if len(studios) != 0 {
		t.Fatalf("name-only Studios pairs emitted: %v", studios)
	}
	if item["SeriesStudio"] != "Studio One" {
		t.Fatalf("SeriesStudio lost: %v", item)
	}

	source := map[string]any{}
	addChapters(source, core.MediaInfo{Chapters: []core.Chapter{{Title: "Intro", Start: time.Minute}}})
	chapters, _ := source["Chapters"].([]any)
	if len(chapters) != 1 || chapters[0].(map[string]any)["ImageDateModified"] == nil {
		t.Fatalf("chapter missing ImageDateModified: %v", chapters)
	}

	streams := mediaStreams([]frontend.StreamInfo{{Type: "video", Index: 0}, {Type: "data", Index: 1}, {Type: "audio", Index: 2}})
	if len(streams) != 2 {
		t.Fatalf("data stream not dropped: %v", streams)
	}
	for _, s := range streams {
		if s.(map[string]any)["Type"] == "" {
			t.Fatalf("empty MediaStream.Type: %v", s)
		}
	}
}
