package livetv

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// The invariant is membership rather than duplicated channels: ordered overlaps,
// rename-stable identity, and viewer visibility hold at the same boundary.
func TestOwnerGroupMembership(t *testing.T) {
	ctx := context.Background()
	s := testService(t, Options{})
	for _, c := range []Channel{
		{ID: "news", Name: "News", Number: "1", Kind: "iptv", Enabled: true, SourceID: "s", Group: "News", StreamURL: "https://example.invalid/news"},
		{ID: "kids", Name: "Kids", Number: "2", Kind: "iptv", Enabled: true, SourceID: "s", Group: "Kids", StreamURL: "https://example.invalid/kids"},
		{ID: "off", Name: "Off", Number: "3", Kind: "iptv", Enabled: false, SourceID: "s", Group: "News", StreamURL: "https://example.invalid/off"},
	} {
		if _, err := s.SaveChannel(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	gs := []OwnerGroup{
		{ID: "family", Name: "Family", Groups: []OwnerGroupSource{{SourceID: "s", Name: "News"}, {SourceID: "s", Name: "Kids"}}},
		{ID: "news", Name: "News", Groups: []OwnerGroupSource{{SourceID: "s", Name: "News"}}},
		{ID: "hidden", Name: "Hidden", All: true, Hidden: true},
	}
	if err := s.SaveOwnerGroups(ctx, gs); err != nil {
		t.Fatal(err)
	}
	host := liveTVHost{s: s}
	viewer := &core.User{ID: 1, IsOwner: true}
	cs, err := host.Channels(ctx, viewer)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("visible channels: %+v", cs)
	}
	for _, c := range cs {
		if c.ID == "news" && !reflect.DeepEqual(c.Groups, []frontend.LiveGroup{{ID: "family", Name: "Family"}, {ID: "news", Name: "News"}}) {
			t.Fatalf("memberships: %+v", c.Groups)
		}
	}
	selected, err := host.GroupChannels(ctx, viewer, "news")
	if err != nil || len(selected) != 1 || selected[0].ID != "news" {
		t.Fatalf("group: %+v %v", selected, err)
	}
	gs[0].Name = "Renamed"
	gs[0], gs[1] = gs[1], gs[0]
	if err := s.SaveOwnerGroups(ctx, gs); err != nil {
		t.Fatal(err)
	}
	groups, err := host.Groups(ctx, viewer)
	if err != nil || len(groups) != 2 || groups[1].ID != "family" || groups[1].Name != "Renamed" {
		t.Fatalf("renamed order: %+v %v", groups, err)
	}
	if _, err := host.GroupChannels(ctx, viewer, "hidden"); err == nil {
		t.Fatal("hidden group exposed")
	}
}

func TestOwnerGroupsEndpointsAndMigration(t *testing.T) {
	ctx := context.Background()
	s := testService(t, Options{})
	for _, c := range []Channel{
		{ID: "c1", Name: "BBC One", Number: "101", Kind: "iptv", Enabled: true, SourceID: "uk", Group: "Entertainment", StreamURL: "https://example.invalid/1"},
		{ID: "c2", Name: "Sky Sports", Number: "102", Kind: "iptv", Enabled: true, SourceID: "uk", Group: "Sports", StreamURL: "https://example.invalid/2"},
	} {
		if _, err := s.SaveChannel(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}

	// Empty list returns JSON [] (never null).
	if rec := call(http.MethodGet, "/groups", ""); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty GET /groups: %d %q", rec.Code, rec.Body.String())
	}

	// PUT /groups accepts W1's ProviderGroupRef extra UI fields (sourceName, count) and returns OwnerGroup[].
	putBody := `[
		{"id":"sports","name":"Sports","groups":[{"sourceId":"uk","name":"Sports","sourceName":"UK","count":1}],"hiddenApps":["emby"]},
		{"id":"ent","name":"Entertainment","channels":["c1"]}
	]`
	rec := call(http.MethodPut, "/groups", putBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /groups: %d %s", rec.Code, rec.Body.String())
	}
	var list []OwnerGroup
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("unmarshal PUT /groups: %v %+v", err, list)
	}
	if list[0].ID != "sports" || list[0].ChannelCount != 1 || list[0].Apps["emby"] != false {
		t.Fatalf("unexpected sports group: %+v", list[0])
	}

	// POST /groups appends, PUT /groups/order reorders, PATCH /groups/{id}/apps updates app map, DELETE removes.
	rec = call(http.MethodPost, "/groups", `{"name":"All Live","all":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /groups: %d %s", rec.Code, rec.Body.String())
	}
	var createEnv struct {
		Group  OwnerGroup   `json:"group"`
		Groups []OwnerGroup `json:"groups"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &createEnv)
	created := createEnv.Group
	if created.ID == "" || created.ChannelCount != 2 || len(createEnv.Groups) != 3 {
		t.Fatalf("created group: %+v", createEnv)
	}
	if rec = call(http.MethodPut, "/groups/order", `{"ids":["ent","sports","`+created.ID+`"]}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT /groups/order: %d %s", rec.Code, rec.Body.String())
	}
	if rec = call(http.MethodPatch, "/groups/sports/apps", `{"plex":false}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH /groups/sports/apps: %d %s", rec.Code, rec.Body.String())
	}
	if rec = call(http.MethodDelete, "/groups/"+created.ID, ""); rec.Code != http.StatusOK {
		t.Fatalf("DELETE /groups/{id}: %d %s", rec.Code, rec.Body.String())
	}

	// Migration v24 strips hubGroups from livetv_sources JSON cleanly.
	tx, err := s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO livetv_sources(id, data) VALUES('src1', '{"id":"src1","name":"Src","hubGroups":true}')`); err != nil {
		t.Fatal(err)
	}
	if err := migrateOwnerGroupsV25(ctx, tx); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT data FROM livetv_sources WHERE id='src1'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "hubGroups") {
		t.Fatalf("hubGroups was not stripped by migration: %s", raw)
	}
}

func TestOwnerGroupDecisionALateProviderChannel(t *testing.T) {
	ctx := context.Background()
	s := testService(t, Options{})
	if _, err := s.SaveChannel(ctx, Channel{
		ID: "sky-main", Name: "Sky Sports Main", Number: "401", Kind: "iptv",
		Enabled: true, SourceID: "uk", Group: "Sports", StreamURL: "https://example.invalid/401",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveOwnerGroups(ctx, []OwnerGroup{{
		ID: "sports", Name: "Sports",
		Groups: []OwnerGroupSource{{SourceID: "uk", Name: "Sports"}},
		Apps:   map[string]bool{"plex": false},
	}}); err != nil {
		t.Fatal(err)
	}

	// Provider later imports a brand new channel in ("uk", "Sports").
	if _, err := s.SaveChannel(ctx, Channel{
		ID: "sky-f1", Name: "Sky Sports F1", Number: "406", Kind: "iptv",
		Enabled: true, SourceID: "uk", Group: "Sports", StreamURL: "https://example.invalid/406",
	}); err != nil {
		t.Fatal(err)
	}

	viewer := &core.User{ID: 1, IsOwner: true}
	plexChans, err := s.AllowedChannels(core.WithLiveApp(ctx, "plex"), viewer)
	if err != nil {
		t.Fatal(err)
	}
	if len(plexChans) != 0 {
		t.Fatalf("late provider channel leaked into plex despite group hide: %+v", plexChans)
	}
	jfChans, err := s.AllowedChannels(core.WithLiveApp(ctx, "jellyfin"), viewer)
	if err != nil || len(jfChans) != 2 {
		t.Fatalf("jellyfin should still see both sports channels: %+v %v", jfChans, err)
	}

	// Explicit per-channel show override via PUT /channels/{id} wins over the group hide.
	body := bytes.NewBufferString(`{"id":"sky-f1","name":"Sky Sports F1","number":"406","kind":"iptv","enabled":true,"sourceId":"uk","group":"Sports","streamUrl":"https://example.invalid/406","hiddenApps":[]}`)
	req := httptest.NewRequest(http.MethodPut, "/channels/sky-f1", body)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /channels/sky-f1: %d %s", rec.Code, rec.Body.String())
	}
	plexChans, err = s.AllowedChannels(core.WithLiveApp(ctx, "plex"), viewer)
	if err != nil || len(plexChans) != 1 || plexChans[0].ID != "sky-f1" {
		t.Fatalf("explicit channel show override failed: %+v %v", plexChans, err)
	}
	summaries, err := s.ChannelSummaries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, sum := range summaries {
		if sum.ID == "sky-main" && !slices.Contains(sum.HiddenApps, "plex") {
			t.Fatalf("sky-main summary should report effective plex hide: %+v", sum)
		}
		if sum.ID == "sky-f1" && (slices.Contains(sum.HiddenApps, "plex") || !slices.Contains(sum.ShownApps, "plex")) {
			t.Fatalf("sky-f1 summary should report shownApps=[plex] and not hidden: %+v", sum)
		}
	}
}

func TestOwnerGroupRulesAndPreview(t *testing.T) {
	ctx := context.Background()
	s := testService(t, Options{})
	if _, err := s.SavePendingSource(ctx, Source{ID: "tuli", Name: "Tuliprox US"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Channel{
		{ID: "cbs", Name: "CBS WCBS HD", Number: "2", Kind: "iptv", Enabled: true, SourceID: "tuli", Group: "US Locals", GuideID: "cbs.wcbs", StreamURL: "https://example.invalid/cbs"},
		{ID: "espn", Name: "ESPN HD", Number: "206", Kind: "iptv", Enabled: true, SourceID: "tuli", Group: "US Sports", GuideID: "espn.us", StreamURL: "https://example.invalid/espn"},
		{ID: "espn-bak", Name: "ESPN Backup", Number: "207", Kind: "iptv", Enabled: true, SourceID: "tuli", Group: "US Sports", GuideID: "espn.us", StreamURL: "https://example.invalid/espn-bak"},
		{ID: "golf", Name: "Golf Channel SD", Number: "218", Kind: "iptv", Enabled: true, SourceID: "tuli", Group: "US Sports", GuideID: "golf.us", StreamURL: "https://example.invalid/golf"},
		{ID: "bbc", Name: "BBC News HD", Number: "501", Kind: "iptv", Enabled: true, SourceID: "uk", Group: "UK News", GuideID: "bbc.news", StreamURL: "https://example.invalid/bbc"},
	} {
		if _, err := s.SaveChannel(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}

	// 1. Invalid pattern returns HTTP 400 with the bad pattern and RE2 error text on both preview and save.
	badPattern := `(?i)^(cbs\.wcbs|espn\.us`
	badPayloadBytes, _ := json.Marshal(OwnerGroup{
		Name:  "Broken",
		Rules: []OwnerGroupRule{{Field: "guideId", Op: "matches", Value: badPattern}},
	})
	badBody := string(badPayloadBytes)
	for _, target := range []struct{ method, path, payload string }{
		{http.MethodPost, "/groups/preview", badBody},
		{http.MethodPut, "/groups", `[` + badBody + `]`},
	} {
		rec := call(target.method, target.path, target.payload)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s %s status=%d body=%s", target.method, target.path, rec.Code, rec.Body.String())
		}
		var errEnv struct {
			Error struct {
				Message string            `json:"message"`
				Fields  []ownerFieldError `json:"fields"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &errEnv); err != nil {
			t.Fatalf("%s %s decode error: %v (%s)", target.method, target.path, err, rec.Body.String())
		}
		if (!strings.Contains(errEnv.Error.Message, badPattern) && !strings.Contains(errEnv.Error.Message, `(?i)^(cbs\\.wcbs|espn\\.us`)) ||
			!strings.Contains(errEnv.Error.Message, "missing closing )") {
			t.Fatalf("%s %s expected bad pattern and RE2 error in %+v", target.method, target.path, errEnv.Error)
		}
		if len(errEnv.Error.Fields) != 1 || errEnv.Error.Fields[0].Field != "rules.0.value" || !strings.Contains(errEnv.Error.Fields[0].Message, "missing closing )") {
			t.Fatalf("%s %s unexpected field errors: %+v", target.method, target.path, errEnv.Error.Fields)
		}
	}

	// 2. Preview endpoint evaluates Tuliprox-style (?i) alternation + exclude subtraction before saving.
	previewReq := `{
		"name": "US Sports",
		"groups": [],
		"match": "any",
		"rules": [
			{"field": "guideId", "op": "matches", "value": "(?i)^(cbs\\.wcbs|espn\\.us|fs1\\.us)$"},
			{"field": "name", "op": "notContains", "value": "backup"}
		]
	}`
	rec := call(http.MethodPost, "/groups/preview", previewReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /groups/preview: %d %s", rec.Code, rec.Body.String())
	}
	var preview OwnerGroupPreview
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Count != 2 || len(preview.Channels) != 2 || preview.Channels[0].ID != "cbs" || preview.Channels[1].ID != "espn" {
		t.Fatalf("unexpected preview result: %+v", preview)
	}

	// 3. Save groups testing Match="any" (with (?i) regex + provider group + exclude) and Match="all" (source + name + notMatches).
	putGroups := `[
		{
			"id": "us-sports",
			"name": "US Sports",
			"groups": [{"sourceId": "tuli", "name": "US Sports"}],
			"match": "any",
			"rules": [
				{"field": "guideId", "op": "matches", "value": "(?i)^(cbs\\.wcbs|espn\\.us|fs1\\.us)$"},
				{"field": "name", "op": "notContains", "value": "Backup"},
				{"field": "name", "op": "notMatches", "value": "\\bSD$"}
			],
			"unratedRating": "TV-PG",
			"apps": {"plex": false}
		},
		{
			"id": "hd-tuli",
			"name": "Tuliprox HD",
			"groups": [],
			"match": "all",
			"rules": [
				{"field": "source", "op": "contains", "value": "tuliprox"},
				{"field": "name", "op": "contains", "value": "hd"},
				{"field": "number", "op": "is", "value": "206"}
			]
		}
	]`
	rec = call(http.MethodPut, "/groups", putGroups)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /groups: %d %s", rec.Code, rec.Body.String())
	}
	var saved []OwnerGroup
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil || len(saved) != 2 {
		t.Fatalf("unmarshal PUT /groups: %v %+v", err, saved)
	}
	// us-sports: provider group US Sports={espn, espn-bak, golf} U regex={cbs, espn, espn-bak} - {espn-bak, golf} = {cbs, espn}.
	if saved[0].ChannelCount != 2 || len(saved[0].Rules) != 3 || saved[0].Match != "any" {
		t.Fatalf("unexpected us-sports saved group: %+v", saved[0])
	}
	// hd-tuli (match=all): source contains tuliprox AND name contains hd AND number is 206 => {espn}.
	if saved[1].ChannelCount != 1 || saved[1].Match != "all" {
		t.Fatalf("unexpected hd-tuli saved group: %+v", saved[1])
	}

	host := liveTVHost{s: s}
	viewer := &core.User{ID: 1, IsOwner: true}
	hdMembers, err := host.GroupChannels(ctx, viewer, "hd-tuli")
	if err != nil || len(hdMembers) != 1 || hdMembers[0].ID != "espn" {
		t.Fatalf("hd-tuli members: %+v %v", hdMembers, err)
	}

	// 4. Decision A: provider later imports fs1.us in a DIFFERENT provider group ("Extras").
	// Because guideId matches (?i)^(cbs\.wcbs|espn\.us|fs1\.us)$, it automatically joins us-sports
	// and inherits both Apps["plex"]=false and UnratedRating="TV-PG".
	fs1, err := s.SaveChannel(ctx, Channel{
		ID: "fs1", Name: "FS1 HD", Number: "219", Kind: "iptv",
		Enabled: true, SourceID: "tuli", Group: "Extras", GuideID: "FS1.US", StreamURL: "https://example.invalid/fs1",
	})
	if err != nil {
		t.Fatal(err)
	}
	groupsForFS1, err := s.ChannelGroups(ctx, fs1.SourceID, fs1.Group, fs1.ID)
	if err != nil || !reflect.DeepEqual(groupsForFS1, []frontend.LiveGroup{{ID: "us-sports", Name: "US Sports"}}) {
		t.Fatalf("late rule-matched channel did not join us-sports: %+v %v", groupsForFS1, err)
	}
	plexAllowed, err := s.AllowedChannels(core.WithLiveApp(ctx, "plex"), viewer)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range plexAllowed {
		if ch.ID == "fs1" || ch.ID == "cbs" || ch.ID == "espn" {
			t.Fatalf("rule-matched channel %s leaked into plex despite group hide", ch.ID)
		}
	}
	// Excluded channels (espn-bak, golf) in provider group "US Sports" were subtracted, so they remain visible in Plex.
	plexIDs := make([]string, 0, len(plexAllowed))
	for _, ch := range plexAllowed {
		plexIDs = append(plexIDs, ch.ID)
	}
	if !slices.Contains(plexIDs, "espn-bak") || !slices.Contains(plexIDs, "golf") {
		t.Fatalf("excluded channels should not inherit group hide: %v", plexIDs)
	}
	if gotRating := s.channelContentRating(ctx, fs1, nil); gotRating != "TV-PG" {
		t.Fatalf("fs1 inherited rating = %q, want TV-PG", gotRating)
	}
}
