package plex

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/livetv"
)

func TestSingleLiveProviderWithGroupGuideTabs(t *testing.T) {
	f, service, chUS := liveFixture(t)
	src := livetv.Source{
		Name: "More",
		Playlist: "#EXTM3U\n" +
			"#EXTINF:-1 tvg-id=\"ca\" tvg-chno=\"8\",Canada channel\nhttp://127.0.0.1:9/ca.m3u8\n" +
			"#EXTINF:-1 tvg-id=\"fr\" tvg-chno=\"9\",Francais channel\nhttp://127.0.0.1:9/fr.m3u8\n",
		Guide: `<tv>` +
			`<channel id="ca"><display-name>Canada channel</display-name></channel>` +
			`<channel id="fr"><display-name>Francais channel</display-name></channel>` +
			`</tv>`,
	}
	if _, err := service.ImportSource(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	cs, err := service.Channels(context.Background())
	if err != nil || len(cs) != 3 {
		t.Fatalf("channels %v %+v", err, cs)
	}
	byNumber := map[string]livetv.Channel{}
	for _, c := range cs {
		byNumber[c.Number] = c
	}
	chCA, chFR := byNumber["8"], byNumber["9"]

	// Non-alphabetical owner order (both by ID and by display name) proves the
	// guide tabs preserve the owner's ordering rather than sorting.
	groups := []livetv.OwnerGroup{
		{ID: "us", Name: "US", Channels: []string{chUS.ID}},
		{ID: "canada", Name: "Canada", Channels: []string{chCA.ID}},
		{ID: "francais", Name: "Français", Channels: []string{chFR.ID}},
	}
	if err := service.SaveOwnerGroups(context.Background(), groups); err != nil {
		t.Fatal(err)
	}

	r := f.request(t, "GET", "/media/providers", map[string]string{"Accept": "application/json"})
	if r.Code != http.StatusOK {
		t.Fatalf("providers %d %s", r.Code, r.Body.String())
	}
	var liveProviders []map[string]any
	for _, raw := range decodeJSON(t, r)["MediaProvider"].([]any) {
		p := raw.(map[string]any)
		if p["protocols"] == "livetv" || strings.HasPrefix(p["identifier"].(string), "tv.plex.providers.epg.xmltv:") {
			liveProviders = append(liveProviders, p)
		}
	}
	if len(liveProviders) != 1 || liveProviders[0]["identifier"] != livetv.Provider {
		t.Fatalf("expected exactly one Live TV provider (%s), got %d: %s", livetv.Provider, len(liveProviders), r.Body.String())
	}

	var gridFeature map[string]any
	for _, raw := range liveProviders[0]["Feature"].([]any) {
		feat := raw.(map[string]any)
		if feat["type"] == "grid" {
			gridFeature = feat
		}
	}
	if gridFeature == nil {
		t.Fatalf("grid feature missing: %s", r.Body.String())
	}
	promoted, _ := gridFeature["PromotedGridChannelFilter"].([]any)
	if len(promoted) != 1 || promoted[0].(map[string]any)["identifier"] != "featured" {
		t.Fatalf("promoted grid filters: %v", promoted)
	}

	var groupTitles, groupIDs, groupKeys []string
	for _, raw := range gridFeature["GridChannelFilter"].([]any) {
		flt := raw.(map[string]any)
		id, _ := flt["identifier"].(string)
		if strings.HasPrefix(id, "guide-") {
			groupIDs = append(groupIDs, id)
			groupTitles = append(groupTitles, flt["title"].(string))
			groupKeys = append(groupKeys, flt["key"].(string))
		}
	}
	if !slices.Equal(groupIDs, []string{"guide-us", "guide-canada", "guide-francais"}) ||
		!slices.Equal(groupTitles, []string{"US", "Canada", "Français"}) {
		t.Fatalf("group guide tabs out of owner order: ids=%v titles=%v", groupIDs, groupTitles)
	}

	dvrsResp := f.request(t, "GET", "/livetv/dvrs", map[string]string{"Accept": "application/json"})
	dvrs, _ := decodeJSON(t, dvrsResp)["Dvr"].([]any)
	if dvrsResp.Code != http.StatusOK || len(dvrs) != 1 || dvrs[0].(map[string]any)["key"] != "1" || dvrs[0].(map[string]any)["epgIdentifier"] != livetv.Provider {
		t.Fatalf("expected one DVR, got %d: %s", len(dvrs), dvrsResp.Body.String())
	}

	wantChannels := []string{chUS.ID, chCA.ID, chFR.ID}
	for i, key := range groupKeys {
		resp := f.request(t, "GET", key, map[string]string{"Accept": "application/json"})
		chs, _ := decodeJSON(t, resp)["Channel"].([]any)
		if resp.Code != http.StatusOK || len(chs) != 1 || chs[0].(map[string]any)["id"] != wantChannels[i] {
			t.Fatalf("group filter %q (%d): %s", key, resp.Code, resp.Body.String())
		}
	}

	for _, unknown := range []string{
		"/tv.plex.providers.epg.xmltv:2/grid",
		"/tv.plex.providers.epg.xmltv:424242/lineups/dvr/channels",
		"/livetv/dvrs/2",
		"/media/grabbers/devices/2",
		"/media/grabbers/devices/2/channels",
	} {
		if got := f.request(t, "GET", unknown, map[string]string{"Accept": "application/json"}); got.Code != http.StatusNotFound {
			t.Fatalf("%s returned %d, want 404", unknown, got.Code)
		}
	}
}
