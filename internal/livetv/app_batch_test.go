package livetv

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestSetChannelsApp(t *testing.T) {
	ctx := context.Background()
	var bumps int
	s := testService(t, Options{})
	s.o.DB.SetMaxOpenConns(1)
	s.onChange = func() { bumps++ }

	seed := []Channel{
		{ID: "uk-ent-1", Number: "101", Name: "BBC One", Kind: "iptv", SourceID: "uk", Group: "Entertainment", StreamURL: "https://example.test/1", Enabled: true, HiddenApps: []string{"emby"}},
		{ID: "uk-ent-2", Number: "102", Name: "BBC Two", Kind: "iptv", SourceID: "uk", Group: "Entertainment", StreamURL: "https://example.test/2", Enabled: true},
		{ID: "uk-sport", Number: "104", Name: "Sky Sports", Kind: "iptv", SourceID: "uk", Group: "Sports", StreamURL: "https://example.test/3", Enabled: true},
		{ID: "us-news", Number: "201", Name: "CNN", Kind: "iptv", SourceID: "us", Group: "News", StreamURL: "https://example.test/4", Enabled: true},
	}
	for _, c := range seed {
		if _, err := s.SaveChannel(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveOwnerGroups(ctx, []OwnerGroup{
		{ID: "picks", Name: "Picks", Groups: []OwnerGroupSource{{SourceID: "uk", Name: "Sports"}}, Channels: []string{"us-news"}},
	}); err != nil {
		t.Fatal(err)
	}

	post := func(body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/channels/apps", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// Hide a provider group from Plex in one transaction and bump once.
	bumpsBefore := bumps
	code, res := post(`{"app":"plex","show":false,"source":"uk","group":"Entertainment"}`)
	if code != http.StatusOK || res["updated"] != float64(2) {
		t.Fatalf("provider group hide: %d %+v", code, res)
	}
	if bumps != bumpsBefore+1 {
		t.Fatalf("expected 1 bump, got %d -> %d", bumpsBefore, bumps)
	}
	if c, err := s.Channel(ctx, "uk-ent-1"); err != nil || !slices.Equal(c.HiddenApps, []string{"emby", "plex"}) {
		t.Fatalf("uk-ent-1 hiddenApps: %v %v", c.HiddenApps, err)
	}
	if c, err := s.Channel(ctx, "uk-sport"); err != nil || len(c.HiddenApps) != 0 {
		t.Fatalf("uk-sport hiddenApps should stay empty: %v %v", c.HiddenApps, err)
	}

	// Hide whole source from Vela, then re-show explicit channel list.
	code, res = post(`{"app":"vela","show":false,"source":"uk"}`)
	if code != http.StatusOK || res["updated"] != float64(3) {
		t.Fatalf("source hide vela: %d %+v", code, res)
	}
	viewer := &core.User{ID: 1, IsOwner: true}
	velaChannels, err := s.AllowedChannels(core.WithLiveApp(ctx, "vela"), viewer)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range velaChannels {
		if c.SourceID == "uk" {
			t.Fatalf("uk channel %s remained visible in vela", c.ID)
		}
	}

	code, res = post(`{"app":"vela","show":true,"channels":["uk-ent-2","uk-sport"]}`)
	if code != http.StatusOK || res["updated"] != float64(2) {
		t.Fatalf("channels show vela: %d %+v", code, res)
	}
	if c, err := s.Channel(ctx, "uk-ent-2"); err != nil || len(c.HiddenApps) != 1 || c.HiddenApps[0] != "plex" {
		t.Fatalf("uk-ent-2 hiddenApps: %v %v", c.HiddenApps, err)
	}

	// Decision A: Owner group toggle persists rule on OwnerGroup.Apps and hides current + future members.
	code, res = post(`{"app":"m3u","show":false,"ownerGroup":"picks"}`)
	if code != http.StatusOK || res["updated"] != float64(2) {
		t.Fatalf("ownerGroup hide m3u: %d %+v", code, res)
	}
	gs, err := s.OwnerGroups(ctx)
	if err != nil || len(gs) != 1 || gs[0].Apps["m3u"] != false {
		t.Fatalf("ownerGroup rule not persisted: %+v %v", gs, err)
	}
	m3uChans, err := s.AllowedChannels(core.WithLiveApp(ctx, "m3u"), viewer)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range m3uChans {
		if c.ID == "uk-sport" || c.ID == "us-news" {
			t.Fatalf("%s remained visible in m3u after ownerGroup hide", c.ID)
		}
	}
	// Explicit per-channel re-show overrides the group hide via ShownApps.
	code, res = post(`{"app":"m3u","show":true,"channels":["us-news"]}`)
	if code != http.StatusOK || res["updated"] != float64(1) {
		t.Fatalf("explicit re-show over group hide: %d %+v", code, res)
	}
	if c, err := s.Channel(ctx, "us-news"); err != nil || !slices.Equal(c.ShownApps, []string{"m3u"}) {
		t.Fatalf("us-news shownApps: %+v %v", c, err)
	}

	// Virtual source selector. The virtual channel joins only now: with one
	// in the lineup, AllowedChannels waits on the background guide render
	// after each edit, which the listing checks above must not race.
	if _, err := s.SaveChannel(ctx, Channel{ID: "virt-1", Number: "1", Name: "Movie Night", Kind: "virtual", Group: "Custom", OffAir: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	code, res = post(`{"app":"silo","show":false,"source":"virtual"}`)
	if code != http.StatusOK || res["updated"] != float64(1) {
		t.Fatalf("virtual source hide: %d %+v", code, res)
	}

	// Validation & missing targets.
	if code, _ := post(`{"app":"unknown","show":false,"channels":["us-news"]}`); code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown app, got %d", code)
	}
	if code, _ := post(`{"app":"plex","channels":["us-news"]}`); code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing show, got %d", code)
	}
	if code, _ := post(`{"app":"plex","show":true,"source":"uk","channels":["us-news"]}`); code != http.StatusBadRequest {
		t.Fatalf("expected 400 for multiple scopes, got %d", code)
	}
	if code, _ := post(`{"app":"plex","show":true,"channels":["missing-id"]}`); code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing channel, got %d", code)
	}
	if code, _ := post(`{"app":"plex","show":true,"ownerGroup":"missing-group"}`); code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing ownerGroup, got %d", code)
	}
}
