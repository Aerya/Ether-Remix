package plex

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/livetv"
)

// Search is another channel policy boundary; returning even a name or a count
// from an inaccessible channel leaks owner/private/current-programme metadata.
func TestLiveChannelSearchContractAndPolicy(t *testing.T) {
	f, service, ch := liveFixture(t)
	ctx := context.Background()
	ch.Name = `Lantern "News" & More`
	ch.Number = "9981"
	ch.Group = ""
	var err error
	if ch, err = service.SaveChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	// First demand returns partial indexed results rather than an error.
	search := service.Frontend().(frontend.LiveTVSearch)
	for deadline := time.Now().Add(2 * time.Second); ; {
		hits, e := search.SearchLiveTV(ctx, &ownerUser, "Lantern", 10)
		if e != nil {
			t.Fatal(e)
		}
		if !hits.Partial {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("search index did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	provider := f.srv.liveTVProvider(ctx, &ownerUser)
	advertised := false
	for _, feature := range provider.Feature {
		if feature.Type == "universalsearch" && feature.Key == liveProviderPath+"/library/search" {
			advertised = true
		}
	}
	if !advertised {
		t.Fatal("channel search is not discoverable")
	}
	wantSource := "server://" + f.srv.identity().MachineIdentifier + liveProviderPath
	wantChannelKey := liveProviderPath + "/channels/" + url.PathEscape(ch.ID)
	path := liveProviderPath + "/library/search?query="
	requestWithHeaders := func(query string, headers map[string]string) map[string]any {
		t.Helper()
		r := f.request(t, "GET", path+url.QueryEscape(query), headers)
		// Initial search demands the index; preparation runs outside requests.
		for deadline := time.Now().Add(2 * time.Second); r.Code == 503 && time.Now().Before(deadline); {
			time.Sleep(10 * time.Millisecond)
			r = f.request(t, "GET", path+url.QueryEscape(query), headers)
		}
		if r.Code != 200 {
			t.Fatalf("search: %d %s", r.Code, r.Body.String())
		}
		return decodeJSON(t, r)
	}
	request := func(query, token string) map[string]any {
		return requestWithHeaders(query, map[string]string{"Accept": "application/json", "X-Plex-Token": token})
	}
	for _, q := range []string{"lantern", "9981", "NEWS lantern", "  Lantern  "} {
		mc := request(q, ownerToken)
		if mc["size"] != float64(1) {
			t.Fatalf("%q: %#v", q, mc)
		}
		hit := mc["SearchResult"].([]any)[0].(map[string]any)
		channel := hit["Channel"].(map[string]any)
		if hit["Metadata"] != nil ||
			channel["id"] != ch.ID ||
			channel["identifier"] != ch.ID ||
			channel["channelIdentifier"] != ch.ID ||
			channel["slug"] != ch.ID ||
			channel["key"] != wantChannelKey ||
			channel["type"] != "channel" ||
			channel["gridKey"] != ch.ID ||
			channel["vcn"] != ch.Number ||
			channel["title"] != ch.Name ||
			channel["shortTitle"] != ch.Name ||
			channel["callSign"] != ch.Name ||
			channel["language"] != "eng" ||
			channel["providerIdentifier"] != frontend.LiveTVProvider ||
			channel["source"] != wantSource ||
			channel["thumb"] != liveLogo(livetv.ToFrontendChannel(ch)) ||
			channel["coverPoster"] != liveLogo(livetv.ToFrontendChannel(ch)) {
			t.Fatalf("channel result: %#v", hit)
		}
		if cats, ok := channel["categories"].([]any); !ok || len(cats) != 0 {
			t.Fatalf("required empty array missing: %#v", channel)
		}
		if channel["streamUrl"] != nil {
			t.Fatal("upstream exposed")
		}
	}
	// Plex Web and Bedrock clients render coverPoster/thumb directly from SearchResult.Channel.
	webMC := requestWithHeaders("Lantern", map[string]string{"Accept": "application/json", "X-Plex-Token": ownerToken, "X-Plex-Product": "Plex Web"})
	webChan := webMC["SearchResult"].([]any)[0].(map[string]any)["Channel"].(map[string]any)
	webLogo, err := url.Parse(webChan["coverPoster"].(string))
	if err != nil || webLogo.Host != "example.com" || webLogo.Query().Get("X-Plex-Token") != ownerToken || webChan["thumb"] != webChan["coverPoster"] {
		t.Fatalf("Plex Web search channel artwork: %#v", webChan)
	}
	bedrockMC := requestWithHeaders("Lantern", map[string]string{"Accept": "application/json", "X-Plex-Token": ownerToken, "X-Plex-Product": "Plex for Apple TV", "X-Plex-Platform": "tvOS"})
	bedrockChan := bedrockMC["SearchResult"].([]any)[0].(map[string]any)["Channel"].(map[string]any)
	bedrockLogo := bedrockChan["coverPoster"].(string)
	if !strings.Contains(bedrockLogo, signedLogoPrefix) || strings.Contains(bedrockLogo, ownerToken) || bedrockChan["thumb"] != bedrockLogo {
		t.Fatalf("Bedrock search channel artwork: %#v", bedrockChan)
	}

	for _, q := range []string{"", "  ", "not-present"} {
		if mc := request(q, ownerToken); mc["size"] != float64(0) {
			t.Fatalf("not channel-only for %q: %#v", q, mc)
		}
	}
	if mc := request("Owner programme", ownerToken); mc["size"] != float64(1) {
		t.Fatalf("programme search missing: %#v", mc)
	}
	r := f.request(t, "GET", path+"Lantern", map[string]string{"Accept": "application/xml"})
	var wire struct {
		Results []struct {
			Channel struct {
				ID          string `xml:"id,attr"`
				Identifier  string `xml:"identifier,attr"`
				Key         string `xml:"key,attr"`
				Type        string `xml:"type,attr"`
				Title       string `xml:"title,attr"`
				Thumb       string `xml:"thumb,attr"`
				CoverPoster string `xml:"coverPoster,attr"`
				Source      string `xml:"source,attr"`
				Categories  string `xml:"categories,attr"`
			} `xml:"Channel"`
		} `xml:"SearchResult"`
	}
	if err := xml.Unmarshal(r.Body.Bytes(), &wire); err != nil ||
		len(wire.Results) != 1 ||
		wire.Results[0].Channel.ID != ch.ID ||
		wire.Results[0].Channel.Identifier != ch.ID ||
		wire.Results[0].Channel.Key != wantChannelKey ||
		wire.Results[0].Channel.Type != "channel" ||
		wire.Results[0].Channel.Title != ch.Name ||
		wire.Results[0].Channel.Thumb == "" ||
		wire.Results[0].Channel.CoverPoster == "" ||
		wire.Results[0].Channel.Source != wantSource {
		t.Fatalf("XML: %v %s", err, r.Body.String())
	}
	// Both the channel metadata key and tune route resolve for the search hit.
	detail := f.request(t, "GET", wantChannelKey, map[string]string{"Accept": "application/json", "X-Plex-Token": ownerToken})
	if detail.Code != http.StatusOK {
		t.Fatalf("channel detail %s: %d %s", wantChannelKey, detail.Code, detail.Body.String())
	}
	tune := f.request(t, "POST", "/livetv/dvrs/1/channels/"+url.PathEscape(ch.ID)+"/tune", map[string]string{"Accept": "application/json"})
	if tune.Code != 200 {
		t.Fatalf("tune: %d %s", tune.Code, tune.Body.String())
	}
	if err := service.SavePolicy(ctx, livetv.Policy{UserID: friendUser.ID, Restricted: true}); err != nil {
		t.Fatal(err)
	}
	if r := f.request(t, "GET", path+"Lantern", map[string]string{"X-Plex-Token": friendToken}); r.Code != 404 {
		t.Fatalf("restricted search: %d", r.Code)
	}
	if err := service.SavePolicy(ctx, livetv.Policy{UserID: friendUser.ID, MaxRating: "G"}); err != nil {
		t.Fatal(err)
	}
	if mc := request("Lantern", friendToken); mc["size"] != float64(0) {
		t.Fatalf("current programme rating leaked: %#v", mc)
	}
	if err := service.SavePolicy(ctx, livetv.Policy{UserID: friendUser.ID, Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if r := f.request(t, "GET", path+"Lantern", map[string]string{"X-Plex-Token": friendToken}); r.Code != 404 {
		t.Fatalf("disabled search: %d", r.Code)
	}
	if r := f.request(t, "GET", path+strings.Repeat("a", 257), nil); r.Code != 400 {
		t.Fatalf("oversize query: %d", r.Code)
	}
	ch.Enabled = false
	if _, err := service.SaveChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if r := f.request(t, "GET", path+"Lantern", map[string]string{"X-Plex-Token": ownerToken}); r.Code != 404 {
		t.Fatalf("disabled channel search: %d", r.Code)
	}
}

// Plex Web and Bedrock clients that search via /hubs/search only normalize
// Metadata and Directory children on Hub (ignoring Hub.Channel), require the
// EPG provider source URI so isLiveTVMetadataItem dispatches Live TV tuning
// instead of library PlayQueue playback, and require skipDetails + an active
// Media[0] window (beginsAt <= now <= endsAt) + channelIdentifier so clicking
// a channel card tunes directly instead of navigating to a dead /details page
// or failing with "Not on air".
func TestHubSearchLiveTVChannelTunesDirectlyAndHonorsPolicy(t *testing.T) {
	f, service, ch := liveFixture(t)
	ctx := context.Background()
	ch.Name = "Lantern Sports HD"
	ch.Number = "4412"
	ch.Group = "Sports"
	var err error
	if ch, err = service.SaveChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	search := service.Frontend().(frontend.LiveTVSearch)
	for deadline := time.Now().Add(2 * time.Second); ; {
		hits, e := search.SearchLiveTV(ctx, &ownerUser, "Lantern", 10)
		if e != nil {
			t.Fatal(e)
		}
		if !hits.Partial {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("search index did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	wantSource := "server://" + f.srv.identity().MachineIdentifier + liveProviderPath
	wantChannelKey := liveProviderPath + "/channels/" + url.PathEscape(ch.ID)

	findHub := func(mc map[string]any, id string) (map[string]any, int) {
		for idx, raw := range mc["Hub"].([]any) {
			h := raw.(map[string]any)
			if h["hubIdentifier"] == id {
				return h, idx
			}
		}
		return nil, -1
	}

	// 1. Channel hit in /hubs/search (JSON, Plex Web).
	rec := f.request(t, http.MethodGet, "/hubs/search?query=Lantern&limit=10", map[string]string{
		"Accept":         "application/json",
		"X-Plex-Token":   ownerToken,
		"X-Plex-Product": "Plex Web",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("/hubs/search status = %d: %s", rec.Code, rec.Body.String())
	}
	mc := decodeJSON(t, rec)
	liveHub, hubIdx := findHub(mc, "livetv")
	if liveHub == nil {
		t.Fatalf("missing livetv hub in /hubs/search: %#v", mc["Hub"])
	}
	if hubIdx != 0 {
		t.Fatalf("livetv hub placed at index %d behind empty hubs; want 0", hubIdx)
	}
	if _, hasChannelArray := liveHub["Channel"]; hasChannelArray {
		t.Fatalf("livetv hub must not emit ignored Channel array: %#v", liveHub)
	}
	if liveHub["type"] != "clip" ||
		liveHub["title"] != "Live TV" ||
		liveHub["source"] != wantSource ||
		liveHub["identifier"] != frontend.LiveTVProvider ||
		liveHub["size"] != float64(1) {
		t.Fatalf("unexpected livetv hub shape: %#v", liveHub)
	}
	items, ok := liveHub["Metadata"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("livetv hub missing Metadata items: %#v", liveHub)
	}
	item := items[0].(map[string]any)
	score, scoreErr := strconv.ParseFloat(item["score"].(string), 64)
	if scoreErr != nil || score <= 0 {
		t.Fatalf("invalid Metadata score %v: %v", item["score"], scoreErr)
	}
	if item["type"] != "clip" ||
		item["key"] != wantChannelKey ||
		item["title"] != ch.Name ||
		item["live"] != true ||
		item["dvrChannel"] != true ||
		item["skipDetails"] != true ||
		item["channelIdentifier"] != ch.ID ||
		item["vcn"] != ch.Number ||
		item["source"] != wantSource {
		t.Fatalf("unexpected channel Metadata item: %#v", item)
	}
	thumbURL, err := url.Parse(item["thumb"].(string))
	if err != nil || thumbURL.Host != "example.com" || thumbURL.Query().Get("X-Plex-Token") != ownerToken {
		t.Fatalf("Plex Web channel Metadata thumb = %v", item["thumb"])
	}
	mediaList, ok := item["Media"].([]any)
	if !ok || len(mediaList) != 1 {
		t.Fatalf("channel Metadata missing Media: %#v", item)
	}
	media := mediaList[0].(map[string]any)
	now := float64(f.srv.now().Unix())
	beginsAt, _ := media["beginsAt"].(float64)
	endsAt, _ := media["endsAt"].(float64)
	if media["protocol"] != "livetv" ||
		media["origin"] != "livetv" ||
		media["channelIdentifier"] != ch.ID ||
		media["channelID"] != ch.ID ||
		media["gridKey"] != ch.ID ||
		media["channelTitle"] != ch.Name ||
		media["channelCallSign"] != ch.Name ||
		media["channelVcn"] != ch.Number ||
		media["channelThumb"] != item["thumb"] ||
		media["onAir"] != true ||
		!(beginsAt <= now && now < endsAt) {
		t.Fatalf("unexpected channel Media[0] (now=%v): %#v", now, media)
	}

	// Tuning with Media[0].channelIdentifier (what Plex Web 57055 h() dispatches) succeeds.
	tune := f.request(t, http.MethodPost, "/livetv/dvrs/1/channels/"+url.PathEscape(media["channelIdentifier"].(string))+"/tune", map[string]string{
		"Accept":       "application/json",
		"X-Plex-Token": ownerToken,
	})
	if tune.Code != http.StatusOK {
		t.Fatalf("tune channelIdentifier %q: %d %s", media["channelIdentifier"], tune.Code, tune.Body.String())
	}

	// 2. XML /hubs/search renders <Video> (not <Channel>) inside the Live TV <Hub>,
	// and Bedrock clients receive cloud-safe signed channelThumb/thumb URLs.
	xmlRec := f.request(t, http.MethodGet, "/hubs/search?query=Lantern&limit=10", map[string]string{
		"Accept":          "application/xml",
		"X-Plex-Token":    ownerToken,
		"X-Plex-Product":  "Plex for Apple TV",
		"X-Plex-Platform": "tvOS",
	})
	if xmlRec.Code != http.StatusOK {
		t.Fatalf("XML /hubs/search: %d %s", xmlRec.Code, xmlRec.Body.String())
	}
	var xmlDoc struct {
		Hubs []struct {
			HubIdentifier string `xml:"hubIdentifier,attr"`
			Type          string `xml:"type,attr"`
			Source        string `xml:"source,attr"`
			Videos        []struct {
				Key         string `xml:"key,attr"`
				Type        string `xml:"type,attr"`
				Title       string `xml:"title,attr"`
				Thumb       string `xml:"thumb,attr"`
				Live        string `xml:"live,attr"`
				DVRChannel  string `xml:"dvrChannel,attr"`
				SkipDetails string `xml:"skipDetails,attr"`
				Source      string `xml:"source,attr"`
				Media       []struct {
					Protocol          string `xml:"protocol,attr"`
					Origin            string `xml:"origin,attr"`
					ChannelIdentifier string `xml:"channelIdentifier,attr"`
					ChannelThumb      string `xml:"channelThumb,attr"`
					BeginsAt          int64  `xml:"beginsAt,attr"`
					EndsAt            int64  `xml:"endsAt,attr"`
					OnAir             string `xml:"onAir,attr"`
				} `xml:"Media"`
			} `xml:"Video"`
			Channels []struct{} `xml:"Channel"`
		} `xml:"Hub"`
	}
	if err := xml.Unmarshal(xmlRec.Body.Bytes(), &xmlDoc); err != nil {
		t.Fatalf("XML unmarshal: %v\n%s", err, xmlRec.Body.String())
	}
	foundXMLHub := false
	for _, h := range xmlDoc.Hubs {
		if h.HubIdentifier != "livetv" {
			continue
		}
		foundXMLHub = true
		if len(h.Channels) != 0 || len(h.Videos) != 1 {
			t.Fatalf("XML livetv hub Videos=%d Channels=%d", len(h.Videos), len(h.Channels))
		}
		v := h.Videos[0]
		if h.Type != "clip" || h.Source != wantSource ||
			v.Key != wantChannelKey || v.Type != "clip" || v.Live != "1" || v.DVRChannel != "1" || v.SkipDetails != "1" || v.Source != wantSource ||
			!strings.Contains(v.Thumb, signedLogoPrefix) || strings.Contains(v.Thumb, ownerToken) ||
			len(v.Media) != 1 || v.Media[0].ChannelIdentifier != ch.ID || v.Media[0].ChannelThumb != v.Thumb || v.Media[0].OnAir != "1" {
			t.Fatalf("unexpected XML livetv Video: %+v", v)
		}
	}
	if !foundXMLHub {
		t.Fatalf("XML /hubs/search missing livetv hub: %s", xmlRec.Body.String())
	}

	// 3. Programme hit ("On TV" hub) in /hubs/search also carries EPG provider source and finite score.
	progRec := f.request(t, http.MethodGet, "/hubs/search?query="+url.QueryEscape("Owner programme")+"&limit=10", map[string]string{
		"Accept":       "application/json",
		"X-Plex-Token": ownerToken,
	})
	if progRec.Code != http.StatusOK {
		t.Fatalf("programme /hubs/search: %d %s", progRec.Code, progRec.Body.String())
	}
	progMC := decodeJSON(t, progRec)
	onTVHub, onTVIdx := findHub(progMC, "tv.plex.provider.epg")
	if onTVHub == nil || onTVIdx != 0 {
		t.Fatalf("expected On TV hub at index 0, got idx=%d hub=%#v", onTVIdx, onTVHub)
	}
	if onTVHub["source"] != wantSource || onTVHub["identifier"] != frontend.LiveTVProvider {
		t.Fatalf("On TV hub missing EPG source: %#v", onTVHub)
	}
	progItem := onTVHub["Metadata"].([]any)[0].(map[string]any)
	if progScore, err := strconv.ParseFloat(progItem["score"].(string), 64); err != nil || progScore <= 0 || progItem["source"] != wantSource {
		t.Fatalf("On TV Metadata missing score/source: %#v", progItem)
	}

	// 4. Section-scoped search and restricted/rating-blocked/disabled viewers must not see Live TV hubs.
	secRec := f.request(t, http.MethodGet, "/hubs/search?query=Lantern&sectionId=1", map[string]string{
		"Accept":       "application/json",
		"X-Plex-Token": ownerToken,
	})
	if h, _ := findHub(decodeJSON(t, secRec), "livetv"); h != nil {
		t.Fatalf("section-scoped /hubs/search leaked livetv hub: %#v", h)
	}
	for _, pol := range []livetv.Policy{
		{UserID: friendUser.ID, Restricted: true},
		{UserID: friendUser.ID, MaxRating: "G"},
		{UserID: friendUser.ID, Disabled: true},
	} {
		if err := service.SavePolicy(ctx, pol); err != nil {
			t.Fatal(err)
		}
		friendRec := f.request(t, http.MethodGet, "/hubs/search?query=Lantern&limit=10", map[string]string{
			"Accept":       "application/json",
			"X-Plex-Token": friendToken,
		})
		if friendRec.Code != http.StatusOK {
			t.Fatalf("friend /hubs/search (%+v): %d", pol, friendRec.Code)
		}
		friendMC := decodeJSON(t, friendRec)
		if h, _ := findHub(friendMC, "livetv"); h != nil {
			t.Fatalf("policy %+v leaked livetv hub in /hubs/search: %#v", pol, h)
		}
		if h, _ := findHub(friendMC, "tv.plex.provider.epg"); h != nil {
			t.Fatalf("policy %+v leaked On TV hub in /hubs/search: %#v", pol, h)
		}
	}
}

// Plex for iOS and Plex for Android TV only query /library/search (never
// /hubs/search or the EPG provider's /library/search). /library/search must
// return matching Live TV channels (in the EPG SearchResult.Channel shape) and
// on-now airings when searchTypes is empty or includes livetv, support
// space-insensitive channel queries, and enforce viewer Live TV policy.
func TestLibrarySearchIncludesLiveTVChannelsOnNowAndHonorsPolicy(t *testing.T) {
	f, service, ch := liveFixture(t)
	ctx := context.Background()
	ch.Name = "NFL RedZone"
	ch.Number = "701"
	ch.Group = "Sports"
	var err error
	if ch, err = service.SaveChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	search := service.Frontend().(frontend.LiveTVSearch)
	for deadline := time.Now().Add(2 * time.Second); ; {
		hits, e := search.SearchLiveTV(ctx, &ownerUser, "redzone", 10)
		if e != nil {
			t.Fatal(e)
		}
		if !hits.Partial {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("search index did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	wantSource := "server://" + f.srv.identity().MachineIdentifier + liveProviderPath
	wantChannelKey := liveProviderPath + "/channels/" + url.PathEscape(ch.ID)

	findHits := func(mc map[string]any) (chanHit, onNowHit map[string]any) {
		raw, _ := mc["SearchResult"].([]any)
		for _, r := range raw {
			hit := r.(map[string]any)
			if c, ok := hit["Channel"].(map[string]any); ok && c["id"] == ch.ID {
				chanHit = c
			}
			if m, ok := hit["Metadata"].(map[string]any); ok && m["source"] == wantSource {
				onNowHit = m
			}
		}
		return chanHit, onNowHit
	}

	// 1. Space-insensitive variants work on /library/search with empty searchTypes,
	// mixed searchTypes=movies,tv,livetv, and livetv-only (+ searchProviders),
	// as well as on the EPG provider /library/search.
	for _, target := range []string{
		"/library/search?query=" + url.QueryEscape("red zone"),
		"/library/search?query=" + url.QueryEscape("redzone") + "&searchTypes=movies,tv,livetv",
		"/library/search?query=" + url.QueryEscape("redz") + "&searchTypes=livetv",
		"/library/search?query=" + url.QueryEscape("nfl red") + "&searchTypes=livetv&searchProviders=" + url.QueryEscape(frontend.LiveTVProvider),
	} {
		rec := f.request(t, http.MethodGet, target, map[string]string{
			"Accept":          "application/json",
			"X-Plex-Token":    ownerToken,
			"X-Plex-Product":  "Plex for iOS",
			"X-Plex-Platform": "iOS",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", target, rec.Code, rec.Body.String())
		}
		mc := decodeJSON(t, rec)
		c, onNow := findHits(mc)
		if c == nil {
			t.Fatalf("%s missing Channel hit: %#v", target, mc)
		}
		if c["id"] != ch.ID ||
			c["identifier"] != ch.ID ||
			c["channelIdentifier"] != ch.ID ||
			c["slug"] != ch.ID ||
			c["key"] != wantChannelKey ||
			c["type"] != "channel" ||
			c["gridKey"] != ch.ID ||
			c["vcn"] != ch.Number ||
			c["title"] != ch.Name ||
			c["shortTitle"] != ch.Name ||
			c["callSign"] != ch.Name ||
			c["providerIdentifier"] != frontend.LiveTVProvider ||
			c["source"] != wantSource {
			t.Fatalf("%s unexpected Channel shape: %#v", target, c)
		}
		logo, _ := c["coverPoster"].(string)
		if !strings.Contains(logo, signedLogoPrefix) || strings.Contains(logo, ownerToken) || c["thumb"] != logo {
			t.Fatalf("%s expected cloud-safe logo URL, got %#v", target, c)
		}
		if onNow == nil {
			t.Fatalf("%s missing on-now airing Metadata hit: %#v", target, mc)
		}
		mediaList, _ := onNow["Media"].([]any)
		if len(mediaList) != 1 {
			t.Fatalf("%s on-now hit missing Media: %#v", target, onNow)
		}
		media := mediaList[0].(map[string]any)
		if media["protocol"] != "livetv" || media["channelIdentifier"] != ch.ID || media["onAir"] != true {
			t.Fatalf("%s unexpected on-now Media: %#v", target, media)
		}
	}

	// EPG provider /library/search also matches space-insensitive channel queries.
	for _, q := range []string{"red zone", "redzone", "redz", "nfl red"} {
		rec := f.request(t, http.MethodGet, liveProviderPath+"/library/search?query="+url.QueryEscape(q), map[string]string{
			"Accept":       "application/json",
			"X-Plex-Token": ownerToken,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("EPG search %q: %d %s", q, rec.Code, rec.Body.String())
		}
		c, _ := findHits(decodeJSON(t, rec))
		if c == nil {
			t.Fatalf("EPG search %q missing NFL RedZone", q)
		}
	}

	// 2. Explicit non-LiveTV searchTypes excludes Live TV results.
	noLiveRec := f.request(t, http.MethodGet, "/library/search?query=redzone&searchTypes=movies,tv", map[string]string{
		"Accept":       "application/json",
		"X-Plex-Token": ownerToken,
	})
	if noLiveRec.Code != http.StatusOK {
		t.Fatalf("movies,tv search: %d", noLiveRec.Code)
	}
	if c, onNow := findHits(decodeJSON(t, noLiveRec)); c != nil || onNow != nil {
		t.Fatalf("searchTypes=movies,tv leaked Live TV: channel=%#v onNow=%#v", c, onNow)
	}

	// 3. XML /library/search emits <SearchResult><Channel .../></SearchResult> cleanly.
	xmlRec := f.request(t, http.MethodGet, "/library/search?query="+url.QueryEscape("red zone")+"&searchTypes=livetv", map[string]string{
		"Accept":          "application/xml",
		"X-Plex-Token":    ownerToken,
		"X-Plex-Product":  "Plex for Android (TV)",
		"X-Plex-Platform": "Android",
	})
	if xmlRec.Code != http.StatusOK {
		t.Fatalf("XML /library/search: %d %s", xmlRec.Code, xmlRec.Body.String())
	}
	var xmlDoc struct {
		Results []struct {
			Channel *struct {
				ID     string `xml:"id,attr"`
				Key    string `xml:"key,attr"`
				Title  string `xml:"title,attr"`
				Source string `xml:"source,attr"`
			} `xml:"Channel"`
		} `xml:"SearchResult"`
	}
	if err := xml.Unmarshal(xmlRec.Body.Bytes(), &xmlDoc); err != nil {
		t.Fatalf("XML unmarshal: %v\n%s", err, xmlRec.Body.String())
	}
	foundXMLChan := false
	for _, r := range xmlDoc.Results {
		if r.Channel != nil && r.Channel.ID == ch.ID && r.Channel.Key == wantChannelKey && r.Channel.Title == ch.Name && r.Channel.Source == wantSource {
			foundXMLChan = true
		}
	}
	if !foundXMLChan {
		t.Fatalf("XML /library/search missing Channel hit: %s", xmlRec.Body.String())
	}

	// 4. Viewer Live TV policy (Restricted, MaxRating, Disabled) blocks Live TV hits.
	for _, pol := range []livetv.Policy{
		{UserID: friendUser.ID, Restricted: true},
		{UserID: friendUser.ID, MaxRating: "G"},
		{UserID: friendUser.ID, Disabled: true},
	} {
		if err := service.SavePolicy(ctx, pol); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"/library/search?query=redzone", "/library/search?query=redzone&searchTypes=livetv"} {
			rec := f.request(t, http.MethodGet, target, map[string]string{
				"Accept":       "application/json",
				"X-Plex-Token": friendToken,
			})
			if rec.Code != http.StatusOK {
				t.Fatalf("friend %s (%+v): %d", target, pol, rec.Code)
			}
			if c, onNow := findHits(decodeJSON(t, rec)); c != nil || onNow != nil {
				t.Fatalf("policy %+v leaked Live TV in %s: channel=%#v onNow=%#v", pol, target, c, onNow)
			}
		}
	}
}

// Channel-name matches on /library/search must surface the matched channel's
// on-now airing first (above library title matches), label it with the channel
// identity for Plex for iOS/Apple TV (which skip SearchResult.Channel), keep
// the Channel element, and omit future airings of that channel — while
// programme-title matches keep normal ranking and future airings (#764).
func TestLibrarySearchChannelMatchRankingAndLaterAirings(t *testing.T) {
	f, service, _ := liveFixture(t)
	ctx := context.Background()

	// Seed library titles matching both the channel query ("Red zone") and the
	// programme query ("Every Sunday Afternoon").
	for _, item := range []core.Title{
		{Key: core.ShowKey(76401), Type: core.TypeShow, Title: "Red Zone", Year: 2024, SearchScore: 1.0},
		{Key: core.ShowKey(76402), Type: core.TypeShow, Title: "The Red Zone", Year: 2021, SearchScore: 0.9669},
		{Key: core.MovieKey(76403), Type: core.TypeMovie, Title: "Every Sunday Afternoon", Year: 2023, SearchScore: 1.0},
	} {
		f.catalog.titles[item.Key] = item
	}

	// Import NFL RedZone (on-now + 3 future airings whose summary also mentions
	// RedZone) and ESPN Sports (episodic on-now + future airing).
	src := livetv.Source{
		Name: "Sports IPTV",
		Playlist: "#EXTM3U\n" +
			"#EXTINF:-1 tvg-id=\"redzone\" tvg-chno=\"195\" group-title=\"Sports\",NFL RedZone\nhttp://127.0.0.1:9/redzone.m3u8\n" +
			"#EXTINF:-1 tvg-id=\"espn\" tvg-chno=\"206\" group-title=\"Sports\",ESPN Sports\nhttp://127.0.0.1:9/espn.m3u8\n",
		Guide: `<tv>` +
			`<channel id="redzone"><display-name>NFL RedZone</display-name></channel>` +
			`<channel id="espn"><display-name>ESPN Sports</display-name></channel>` +
			`<programme channel="redzone" start="20261018110000 +0000" stop="20261018130000 +0000"><title>Every Sunday Afternoon</title><desc>Every touchdown from every game on NFL RedZone.</desc></programme>` +
			`<programme channel="redzone" start="20261018150000 +0000" stop="20261018170000 +0000"><title>Every Sunday Afternoon</title><desc>Encore coverage on NFL RedZone.</desc></programme>` +
			`<programme channel="redzone" start="20261019110000 +0000" stop="20261019130000 +0000"><title>Every Sunday Afternoon</title><desc>Next day replay on NFL RedZone.</desc></programme>` +
			`<programme channel="redzone" start="20261020110000 +0000" stop="20261020130000 +0000"><title>Every Sunday Afternoon</title><desc>Week recap on NFL RedZone.</desc></programme>` +
			`<programme channel="espn" start="20261018110000 +0000" stop="20261018130000 +0000"><title>SportsCenter</title><desc>Live sports news on ESPN.</desc><category>Series</category></programme>` +
			`<programme channel="espn" start="20261018160000 +0000" stop="20261018180000 +0000"><title>SportsCenter</title><desc>Evening edition on ESPN.</desc><category>Series</category></programme>` +
			`</tv>`,
	}
	if _, err := service.ImportSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	var redzoneID, espnID string
	cs, err := service.Channels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		switch c.Name {
		case "NFL RedZone":
			redzoneID = c.ID
		case "ESPN Sports":
			espnID = c.ID
		}
	}
	if redzoneID == "" || espnID == "" {
		t.Fatalf("missing imported channels: %+v", cs)
	}
	search := service.Frontend().(frontend.LiveTVSearch)
	for deadline := time.Now().Add(2 * time.Second); ; {
		hits, e := search.SearchLiveTV(ctx, &ownerUser, "red zone", 10)
		if e != nil {
			t.Fatal(e)
		}
		if !hits.Partial {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("search index did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	wantSource := "server://" + f.srv.identity().MachineIdentifier + liveProviderPath
	iosHeaders := map[string]string{
		"Accept":          "application/json",
		"X-Plex-Token":    ownerToken,
		"X-Plex-Product":  "Plex for iOS",
		"X-Plex-Platform": "iOS",
	}

	// 1. Channel-name fuzzy match ("Red zone" -> "NFL RedZone"):
	//    - on-now airing ranks FIRST (ahead of "Red Zone" and "The Red Zone")
	//    - carries channel identity on title + Media[0] guide fields
	//    - keeps the Channel element
	//    - returns only the on-now airing, omitting the 3 future airings
	rec := f.request(t, http.MethodGet, "/library/search?query="+url.QueryEscape("Red zone")+"&searchTypes=movies,tv,livetv&limit=30", iosHeaders)
	if rec.Code != http.StatusOK {
		t.Fatalf("red zone search: %d %s", rec.Code, rec.Body.String())
	}
	mc := decodeJSON(t, rec)
	results, _ := mc["SearchResult"].([]any)
	if len(results) < 3 {
		t.Fatalf("expected channel airing, channel element, and library matches, got %#v", results)
	}
	first := results[0].(map[string]any)
	firstMD, _ := first["Metadata"].(map[string]any)
	if firstMD == nil || firstMD["source"] != wantSource {
		t.Fatalf("expected on-now Live TV airing first, got %#v", first)
	}
	if firstMD["title"] != "NFL RedZone" {
		t.Fatalf("expected channel name in on-now title, got %#v", firstMD["title"])
	}
	if firstMD["tagline"] != "Every Sunday Afternoon" || !strings.Contains(firstMD["summary"].(string), "Every Sunday Afternoon") {
		t.Fatalf("expected programme title preserved in tagline/summary, got tagline=%#v summary=%#v", firstMD["tagline"], firstMD["summary"])
	}
	mediaList, _ := firstMD["Media"].([]any)
	if len(mediaList) != 1 {
		t.Fatalf("expected single Media on on-now airing, got %#v", firstMD["Media"])
	}
	media := mediaList[0].(map[string]any)
	if media["channelTitle"] != "NFL RedZone" ||
		media["channelCallSign"] != "NFL RedZone" ||
		media["channelIdentifier"] != redzoneID ||
		media["channelVcn"] != "195" ||
		media["onAir"] != true ||
		!strings.Contains(media["channelThumb"].(string), signedLogoPrefix) {
		t.Fatalf("expected PMS guide channel identity on Media[0], got %#v", media)
	}
	firstScore := first["score"].(float64)

	var redzoneAirings, redzoneChannels, libMatches int
	for _, rawHit := range results {
		hit := rawHit.(map[string]any)
		if chHit, ok := hit["Channel"].(map[string]any); ok && chHit["id"] == redzoneID {
			redzoneChannels++
			continue
		}
		if md, ok := hit["Metadata"].(map[string]any); ok {
			if md["source"] == wantSource {
				redzoneAirings++
			} else {
				libMatches++
				if sc := hit["score"].(float64); sc >= firstScore {
					t.Fatalf("library hit %q score %v must rank below matched channel score %v", md["title"], sc, firstScore)
				}
			}
		}
	}
	if redzoneAirings != 1 {
		t.Fatalf("channel-name match must return only the on-now airing (got %d airings): %#v", redzoneAirings, results)
	}
	if redzoneChannels != 1 {
		t.Fatalf("channel-name match must keep the Channel element (got %d)", redzoneChannels)
	}
	if libMatches < 2 {
		t.Fatalf("expected library matches to remain in results (got %d)", libMatches)
	}

	// 2. Episodic on-now airing on a channel-name match carries grandparentTitle
	//    and omits future airings of that channel.
	epRec := f.request(t, http.MethodGet, "/library/search?query=ESPN&searchTypes=movies,tv,livetv&limit=30", map[string]string{
		"Accept":          "application/json",
		"X-Plex-Token":    ownerToken,
		"X-Plex-Product":  "Plex for Apple TV",
		"X-Plex-Platform": "tvOS",
	})
	if epRec.Code != http.StatusOK {
		t.Fatalf("espn search: %d %s", epRec.Code, epRec.Body.String())
	}
	epResults, _ := decodeJSON(t, epRec)["SearchResult"].([]any)
	if len(epResults) < 2 {
		t.Fatalf("expected episodic airing and channel element, got %#v", epResults)
	}
	epFirstMD, _ := epResults[0].(map[string]any)["Metadata"].(map[string]any)
	if epFirstMD == nil || epFirstMD["type"] != "episode" || epFirstMD["title"] != "ESPN Sports" || epFirstMD["grandparentTitle"] != "SportsCenter" {
		t.Fatalf("unexpected episodic on-now airing: %#v", epFirstMD)
	}
	epAirings := 0
	for _, rawHit := range epResults {
		if md, ok := rawHit.(map[string]any)["Metadata"].(map[string]any); ok && md["source"] == wantSource {
			epAirings++
		}
	}
	if epAirings != 1 {
		t.Fatalf("episodic channel-name match must omit future airings (got %d)", epAirings)
	}

	// 3. Programme-title match ("Every Sunday Afternoon") does NOT match any
	//    channel name: library title ranks normally first, airing keeps the
	//    programme title, and future airings stay.
	progRec := f.request(t, http.MethodGet, "/library/search?query="+url.QueryEscape("Every Sunday Afternoon")+"&searchTypes=movies,tv,livetv&limit=30", iosHeaders)
	if progRec.Code != http.StatusOK {
		t.Fatalf("programme search: %d %s", progRec.Code, progRec.Body.String())
	}
	progResults, _ := decodeJSON(t, progRec)["SearchResult"].([]any)
	if len(progResults) < 5 {
		t.Fatalf("expected library match plus on-now and future airings, got %#v", progResults)
	}
	progFirstMD, _ := progResults[0].(map[string]any)["Metadata"].(map[string]any)
	if progFirstMD == nil || progFirstMD["source"] == wantSource || progFirstMD["title"] != "Every Sunday Afternoon" {
		t.Fatalf("programme-title search must keep normal ranking with library match first, got %#v", progResults[0])
	}
	var progLiveAirings, progOnAir int
	for _, rawHit := range progResults {
		md, ok := rawHit.(map[string]any)["Metadata"].(map[string]any)
		if !ok || md["source"] != wantSource {
			continue
		}
		progLiveAirings++
		if md["title"] != "Every Sunday Afternoon" {
			t.Fatalf("programme-title match should keep programme title, got %#v", md["title"])
		}
		if ml, _ := md["Media"].([]any); len(ml) == 1 && ml[0].(map[string]any)["onAir"] == true {
			progOnAir++
		}
	}
	if progLiveAirings != 4 || progOnAir != 1 {
		t.Fatalf("programme-title match must keep on-now + future airings (got total=%d onAir=%d)", progLiveAirings, progOnAir)
	}
}
