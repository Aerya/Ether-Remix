package vela

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

type livePageFake struct {
	frontend.LiveTV
	channels   []frontend.LiveChannel
	programmes []frontend.LiveProgramme
	snapshot   string
}

func (f *livePageFake) Channels(context.Context, *frontend.Viewer) ([]frontend.LiveChannel, error) {
	return f.channels, nil
}
func (f *livePageFake) GuidePage(_ context.Context, _ *frontend.Viewer, q frontend.LiveGuidePageQuery) (frontend.LiveGuidePage, error) {
	if q.Snapshot != "" && q.Snapshot != f.snapshot {
		return frontend.LiveGuidePage{}, frontend.ErrLiveGuideStale
	}
	start := min(q.Offset, len(f.programmes))
	end := start + min(q.Limit, len(f.programmes)-start)
	return frontend.LiveGuidePage{Items: f.programmes[start:end], Snapshot: f.snapshot, More: end < len(f.programmes)}, nil
}
func checkLivePages(t *testing.T) {
	source := &livePageFake{snapshot: "one"}
	now := time.Date(2026, 11, 14, 4, 0, 0, 0, time.UTC)
	for n := range 251 {
		id := fmt.Sprint(n)
		source.channels = append(source.channels, frontend.LiveChannel{ID: id, Name: id})
		source.programmes = append(source.programmes, frontend.LiveProgramme{ID: id, ChannelID: id, Start: now, End: now.Add(time.Hour)})
	}
	seal, err := encryption(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	i := &instance{seal: seal, host: frontend.Host{Identity: identityFake{}, Experience: frontendtest.Experience{}, LiveTV: source}}
	get := func(path, device string, handler http.HandlerFunc) (int, map[string]json.RawMessage) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer token")
		r.Header.Set("X-Vela-Device-Id", device)
		w := httptest.NewRecorder()
		handler(w, r)
		var page map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return w.Code, page
	}
	for _, which := range []string{"channels", "guide"} {
		base := "/vela/v0/live/" + which + "?limit=100"
		handler := http.HandlerFunc(i.channels)
		if which == "guide" {
			base += "&from=2026-11-14T04:00:00Z&until=2026-11-14T05:00:00Z"
			handler = i.guide
		}
		path := base
		count := 0
		seen := map[string]bool{}
		firstCursor := ""
		for pageNumber := 0; pageNumber < 4; pageNumber++ {
			code, page := get(path, "device", handler)
			if code != 200 {
				t.Fatalf("%s page%d: %d", which, pageNumber, code)
			}
			var items []struct{ ID string }
			var continuation, next string
			_ = json.Unmarshal(page["items"], &items)
			_ = json.Unmarshal(page["continuation"], &continuation)
			_ = json.Unmarshal(page["nextCursor"], &next)
			for _, item := range items {
				if seen[item.ID] {
					t.Fatal("duplicate", item.ID)
				}
				seen[item.ID] = true
				count++
			}
			if continuation == "exhausted" {
				if next != "" {
					t.Fatal("exhausted cursor")
				}
				break
			}
			if continuation != "more" || next == "" {
				t.Fatal("incomplete continuation", continuation)
			}
			if firstCursor == "" {
				firstCursor = next
			}
			path = base + "&cursor=" + url.QueryEscape(next)
		}
		if count != 251 {
			t.Fatalf("%s count%d", which, count)
		}
		if code, _ := get(base+"&cursor="+url.QueryEscape(firstCursor), "other-device", handler); code != 409 {
			t.Fatal("cross-device cursor accepted", code)
		}
		if code, _ := get(strings.Replace(base, "limit=100", "limit=50", 1)+"&cursor="+url.QueryEscape(firstCursor), "device", handler); code != 409 {
			t.Fatal("changed query accepted", code)
		}
		if which == "channels" {
			source.channels[0].Name = "Changed"
		} else {
			source.snapshot = "two"
		}
		if code, _ := get(base+"&cursor="+url.QueryEscape(firstCursor), "device", handler); code != 409 {
			t.Fatal("changed snapshot accepted", code)
		}
	}
}

type rowVisitFake struct {
	frontendtest.Experience
	refreshes int
	lists     int
}

func (f *rowVisitFake) ListRowViews(_ context.Context, q frontend.RowViewRequest) ([]frontend.RowView, error) {
	f.lists++
	id := "stale"
	if q.Refresh {
		f.refreshes++
		id = "fresh"
	}
	return []frontend.RowView{{ID: id}}, nil
}
func (f *rowVisitFake) ResolveRowView(_ context.Context, q frontend.RowViewPageRequest) (frontend.RowViewPage, error) {
	if q.ID == "stale" {
		return frontend.RowViewPage{}, frontend.ErrRowViewStale
	}
	return frontend.RowViewPage{}, nil
}
func checkFreshHomeVisit(t *testing.T) {
	source := &rowVisitFake{}
	i := &instance{host: frontend.Host{Experience: source}, rowMeta: map[string]frontend.RowView{}}
	r := httptest.NewRequest(http.MethodGet, "/vela/v0/bootstrap", nil)
	rows, _, err := i.home(r, &frontend.Viewer{ID: 1}, "token")
	if err != nil || source.refreshes != 1 || source.lists != 2 || len(rows) != 1 {
		t.Fatalf("fresh visit rows%d refreshes%d lists%d err%v", len(rows), source.refreshes, source.lists, err)
	}
	r = httptest.NewRequest(http.MethodGet, "/vela/v0/rows/stale", nil)
	r.SetPathValue("id", "stale")
	_, _, err = i.home(r, &frontend.Viewer{ID: 1}, "token")
	if err != frontend.ErrRowViewStale || source.refreshes != 1 {
		t.Fatal("old row silently refreshed", err)
	}
}

func TestVelaLiveGuideArtwork(t *testing.T) {
	checkLivePages(t)
	now := time.Date(2026, 11, 14, 4, 0, 0, 0, time.UTC)
	source := &livePageFake{snapshot: "art", channels: []frontend.LiveChannel{{ID: "iptv", Kind: "iptv", Group: "News", Images: []frontend.Image{{Kind: frontend.ImageChannel, Path: "opaque-logo", Tag: "logo"}}}}, programmes: []frontend.LiveProgramme{{ID: "iptv-1", ChannelID: "iptv", Title: "News", Start: now, End: now.Add(time.Hour), Genres: []string{"News"}, Images: []frontend.Image{{Kind: frontend.ImageStill, Path: "opaque-epg", Subject: "iptv-1", Tag: "epg"}}}}}
	seal, _ := encryption(make([]byte, 32))
	issued := 0
	i := &instance{seal: seal, host: frontend.Host{Identity: identityFake{}, Experience: frontendtest.Experience{}, LiveTV: source, ImageGrants: frontendtest.ImageGrants{IssueFunc: func(_ context.Context, req frontend.ImageGrantRequest) (string, error) {
		if req.Channel != "iptv" || req.Item != 0 || req.Credential != "token" || req.Device.ID != "device" {
			t.Fatalf("wrong scope: %+v", req)
		}
		issued++
		return "opaque-capability", nil
	}}}}
	for _, path := range []string{"/vela/v0/live/channels?limit=100", "/vela/v0/live/guide?limit=100&from=2026-11-14T04:00:00Z&until=2026-11-14T05:00:00Z"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer token")
		r.Header.Set("X-Vela-Device-Id", "device")
		w := httptest.NewRecorder()
		if strings.Contains(path, "/channels") {
			i.channels(w, r)
		} else {
			i.guide(w, r)
		}
		if w.Code != 200 {
			t.Fatalf("response %d %s", w.Code, w.Body.String())
		}
		var page struct {
			Items []struct {
				Artwork []struct {
					Sizes []struct {
						URL           string
						Width, Height int
					}
				}
				Group  string
				Genres []string
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || len(page.Items[0].Artwork) != 1 {
			t.Fatal("missing artwork", w.Body.String())
		}
		size := page.Items[0].Artwork[0].Sizes[0]
		if size.URL != "/vela/v0/images/opaque-capability?w=0" || size.Width != 0 || size.Height != 0 {
			t.Fatal("fabricated source sizing", size)
		}
		if strings.Contains(path, "/channels") && page.Items[0].Group != "News" {
			t.Fatal("missing group")
		}
		if strings.Contains(path, "/guide") && len(page.Items[0].Genres) != 1 {
			t.Fatal("missing genres")
		}
	}
	if issued != 2 {
		t.Fatal("unbounded image issue", issued)
	}
}

func TestVelaLiveETagsAndZeroGrantsOn304(t *testing.T) {
	now := time.Date(2026, 11, 18, 18, 0, 0, 0, time.UTC)
	progImg := frontend.Image{Kind: frontend.ImageStill, Path: "epg-still", Subject: "p-1", Tag: "t1"}
	chanImg := frontend.Image{Kind: frontend.ImageChannel, Path: "ch-logo", Tag: "t1"}
	guideProg := frontend.LiveProgramme{
		ID:        "p-1",
		ChannelID: "ch-1",
		Title:     "Evening News",
		Start:     now,
		End:       now.Add(time.Hour),
		Images:    []frontend.Image{progImg},
	}
	windowProg := frontend.LiveProgramme{
		ID:        "p-1",
		ChannelID: "ch-1",
		Title:     "Evening News",
		Start:     now,
		End:       now.Add(time.Hour),
		Images:    []frontend.Image{progImg},
	}
	source := &liveWindowFake{
		livePageFake: livePageFake{
			snapshot:   "snap-1",
			channels:   []frontend.LiveChannel{{ID: "ch-1", Name: "News", Kind: "iptv", Images: []frontend.Image{chanImg}}},
			programmes: []frontend.LiveProgramme{guideProg},
		},
		available: true,
		response: frontend.LiveGuideWindow{
			From:       now,
			Until:      now.Add(time.Hour),
			Revision:   "rev-1",
			ValidUntil: now.Add(6 * time.Hour),
			Channels: []frontend.LiveGuideWindowChannel{
				{ChannelID: "ch-1", State: frontend.LiveGuideWindowReady, Programmes: []frontend.LiveProgramme{windowProg}},
			},
		},
	}

	issued := 0
	grants := frontendtest.ImageGrants{
		IssueFunc: func(_ context.Context, req frontend.ImageGrantRequest) (string, error) {
			issued++
			return fmt.Sprintf("det-%s-%s-%s-%s", req.Channel, req.Image.Kind, req.Image.Path, req.Image.Tag), nil
		},
	}
	inst, _ := newVelaWindowInstance(t, source, grants)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		mutate func()
	}{
		{
			name:   "channels",
			method: http.MethodGet,
			path:   "/vela/v0/live/channels?limit=50",
			mutate: func() {
				source.channels[0].Images[0].Tag = "t2"
			},
		},
		{
			name:   "guide",
			method: http.MethodGet,
			path:   "/vela/v0/live/guide?limit=50&from=2026-11-18T18:00:00Z&until=2026-11-18T19:00:00Z",
			mutate: func() {
				source.programmes[0].Images[0].Tag = "t2"
			},
		},
		{
			name:   "guide-window",
			method: http.MethodPost,
			path:   "/vela/v0/live/guide/window",
			body:   `{"channelIds":["ch-1"],"from":"2026-11-18T18:00:00Z","until":"2026-11-18T19:00:00Z"}`,
			mutate: func() {
				source.response.Channels[0].Programmes[0].Images[0].Tag = "t2"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			do := func(ifNoneMatch string) *httptest.ResponseRecorder {
				var body *strings.Reader
				if tc.body != "" {
					body = strings.NewReader(tc.body)
				} else {
					body = strings.NewReader("")
				}
				r := httptest.NewRequest(tc.method, tc.path, body)
				r.Header.Set("Authorization", "Bearer token")
				r.Header.Set("X-Vela-Device-Id", "tv-1")
				if ifNoneMatch != "" {
					r.Header.Set("If-None-Match", ifNoneMatch)
				}
				w := httptest.NewRecorder()
				inst.serve(w, r)
				return w
			}

			issued = 0
			first := do("")
			if first.Code != http.StatusOK || first.Header().Get("ETag") == "" {
				t.Fatalf("first status=%d etag=%q body=%s", first.Code, first.Header().Get("ETag"), first.Body.String())
			}
			if issued != 1 {
				t.Fatalf("expected 1 grant on first 200, got %d", issued)
			}
			etag := first.Header().Get("ETag")

			// Repeated full 200 is byte-identical with identical ETag.
			second := do("")
			if second.Code != http.StatusOK || second.Header().Get("ETag") != etag {
				t.Fatalf("second status=%d etag=%q want %q", second.Code, second.Header().Get("ETag"), etag)
			}
			if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
				t.Fatalf("expected byte-identical 200 body:\ngot:  %s\nwant: %s", second.Body.String(), first.Body.String())
			}

			// Conditional request returns 304, issues zero grants, and keeps ETag unchanged.
			issued = 0
			notMod := do(etag)
			if notMod.Code != http.StatusNotModified {
				t.Fatalf("expected 304, got %d", notMod.Code)
			}
			if issued != 0 {
				t.Fatalf("expected 0 grants on 304, got %d", issued)
			}
			if notMod.Header().Get("ETag") != etag {
				t.Fatalf("expected unchanged ETag %q on 304, got %q", etag, notMod.Header().Get("ETag"))
			}

			// Changing image tag rotates both the URL and the ETag.
			tc.mutate()
			rotated := do(etag)
			if rotated.Code != http.StatusOK || rotated.Header().Get("ETag") == etag {
				t.Fatalf("expected 200 and new ETag after image tag change, got %d etag=%q", rotated.Code, rotated.Header().Get("ETag"))
			}
		})
	}
}

func TestVelaCacheETagsStayStable(t *testing.T) {
	views := []frontend.View{
		{ID: "23", Name: "Sci-Fi", Type: frontend.TypeMovie, Revision: "rev-1"},
	}
	col1 := core.Collection{Key: core.CollectionKey(10), Type: core.TypeMovie, Title: "Dune Saga", ChildCount: 2}
	col2 := core.Collection{Key: core.SmartKey(20), Type: core.TypeMovie, Title: "90s Classics", ChildCount: 5}
	exp := frontendtest.Experience{
		ViewsFunc: func(context.Context, *frontend.Viewer) ([]frontend.View, error) {
			return views, nil
		},
		CollectionsFunc: func(_ context.Context, _ *frontend.Viewer, _ core.Device, q frontend.CollectionQuery) (frontend.CollectionPage, error) {
			all := []core.Collection{col1, col2}
			start := min(q.Offset, len(all))
			end := min(start+q.Limit, len(all))
			return frontend.CollectionPage{Items: all[start:end], Total: len(all)}, nil
		},
	}
	seal, err := encryption(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	inst := &instance{
		seal: seal,
		mux:  http.NewServeMux(),
		host: frontend.Host{Identity: identityFake{}, Experience: exp, ImageGrants: collectionSignerFake{}},
	}
	inst.routes()

	for _, path := range []string{"/vela/v0/libraries", "/vela/v0/libraries/23/collections?limit=1"} {
		do := func(ifNoneMatch string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.Header.Set("Authorization", "Bearer token")
			r.Header.Set("X-Vela-Device-Id", "device")
			if ifNoneMatch != "" {
				r.Header.Set("If-None-Match", ifNoneMatch)
			}
			w := httptest.NewRecorder()
			inst.serve(w, r)
			return w
		}
		first := do("")
		second := do("")
		if first.Code != http.StatusOK || second.Code != http.StatusOK {
			t.Fatalf("%s codes: %d %d", path, first.Code, second.Code)
		}
		etag := first.Header().Get("ETag")
		if etag == "" || second.Header().Get("ETag") != etag || !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
			t.Fatalf("%s unstable ETag or body: %q vs %q", path, etag, second.Header().Get("ETag"))
		}
		if notMod := do(etag); notMod.Code != http.StatusNotModified {
			t.Fatalf("%s expected 304 on If-None-Match, got %d", path, notMod.Code)
		}
	}
}
