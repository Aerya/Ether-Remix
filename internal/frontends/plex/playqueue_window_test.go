package plex

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestPlayQueueWindowsAroundSelectedEpisode(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()

	const totalEpisodes = 1180
	show := core.ShowKey(37854)
	f.catalog.titles[show] = core.Title{Key: show, Type: core.TypeShow, Title: "One Piece"}

	// Include a Specials season so regular-episode counts and offsets prove
	// specials are excluded before windowing.
	specialSeason := core.SeasonKey(37854, 0)
	specialEp := core.EpisodeKey(37854, 0, 1)
	f.catalog.titles[specialSeason] = core.Title{Key: specialSeason, Type: core.TypeSeason, ShowKey: show, Season: 0}
	f.catalog.titles[specialEp] = core.Title{
		Key: specialEp, Type: core.TypeEpisode, ShowKey: show, Season: 0, Episode: 1,
		Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	f.catalog.children[show] = append(f.catalog.children[show], specialSeason)
	f.catalog.children[specialSeason] = []core.RatingKey{specialEp}

	eps := make([]core.RatingKey, totalEpisodes)
	seenSeason := map[int]bool{}
	for i := 0; i < totalEpisodes; i++ {
		epNum := i + 1
		season := i/40 + 1
		sk := core.SeasonKey(37854, season)
		if !seenSeason[season] {
			seenSeason[season] = true
			f.catalog.titles[sk] = core.Title{Key: sk, Type: core.TypeSeason, ShowKey: show, Season: season}
			f.catalog.children[show] = append(f.catalog.children[show], sk)
		}
		ek := core.EpisodeKey(37854, season, epNum)
		eps[i] = ek
		f.catalog.titles[ek] = core.Title{
			Key: ek, Type: core.TypeEpisode, ShowKey: show, Season: season, Episode: epNum,
			Title:    fmt.Sprintf("Episode %d", epNum),
			Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		f.catalog.children[sk] = append(f.catalog.children[sk], ek)
	}

	headers := map[string]string{
		"Accept":                   "application/json",
		"X-Plex-Client-Identifier": "roku-ultra",
		"X-Plex-Product":           "Plex for Roku",
	}
	showURI := "server://0123456789abcdef0123456789abcdef01234567/com.plexapp.plugins.library/library/metadata/" + show.String()
	ep583 := eps[582] // 1-indexed episode 583 -> 0-based offset 582

	rec := f.request(t, http.MethodPost, "/playQueues?type=video&uri="+url.QueryEscape(showURI)+"&key="+url.QueryEscape("/library/metadata/"+ep583.String()), headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /playQueues: %d %s", rec.Code, rec.Body.String())
	}
	mc := decodeJSON(t, rec)
	if got := mc["size"]; got != float64(frontend.PlaylistExpandLimit) {
		t.Fatalf("size = %v, want %d", got, frontend.PlaylistExpandLimit)
	}
	if got := mc["playQueueTotalCount"]; got != float64(totalEpisodes) {
		t.Fatalf("playQueueTotalCount = %v, want %d", got, totalEpisodes)
	}
	if got := mc["playQueueSelectedMetadataItemID"]; got != ep583.String() {
		t.Fatalf("playQueueSelectedMetadataItemID = %v, want %s", got, ep583)
	}
	if got := mc["playQueueSelectedItemOffset"]; got != float64(582) {
		t.Fatalf("playQueueSelectedItemOffset = %v, want 582", got)
	}
	if got := mc["playQueueVersion"]; got != float64(1) {
		t.Fatalf("playQueueVersion = %v, want 1", got)
	}

	rows := mc["Metadata"].([]any)
	if len(rows) != frontend.PlaylistExpandLimit {
		t.Fatalf("len(Metadata) = %d, want %d", len(rows), frontend.PlaylistExpandLimit)
	}
	if got := rows[0].(map[string]any)["ratingKey"]; got != eps[332].String() {
		t.Fatalf("window first ratingKey = %v, want episode 333 (%s)", got, eps[332])
	}
	selRow := rows[250].(map[string]any)
	if selRow["ratingKey"] != ep583.String() || selRow["playQueueItemID"] != mc["playQueueSelectedItemID"] {
		t.Fatalf("selected row at window index 250 = %v, want key %s itemID %v", selRow, ep583, mc["playQueueSelectedItemID"])
	}
	if got := rows[499].(map[string]any)["ratingKey"]; got != eps[831].String() {
		t.Fatalf("window last ratingKey = %v, want episode 832 (%s)", got, eps[831])
	}

	pqPath := fmt.Sprintf("/playQueues/%.0f", mc["playQueueID"])

	// Windowed read centres on the selected episode while keeping true offsets.
	winRec := f.request(t, http.MethodGet, pqPath+"?window=50", headers)
	if winRec.Code != http.StatusOK {
		t.Fatalf("GET %s?window=50: %d %s", pqPath, winRec.Code, winRec.Body.String())
	}
	win := decodeJSON(t, winRec)
	winRows := win["Metadata"].([]any)
	if win["size"] != float64(101) || len(winRows) != 101 ||
		win["playQueueTotalCount"] != float64(totalEpisodes) ||
		win["playQueueSelectedItemOffset"] != float64(582) ||
		winRows[0].(map[string]any)["ratingKey"] != eps[532].String() ||
		winRows[50].(map[string]any)["ratingKey"] != ep583.String() ||
		winRows[100].(map[string]any)["ratingKey"] != eps[632].String() {
		t.Fatalf("window=50 mismatch: size=%v total=%v offset=%v", win["size"], win["playQueueTotalCount"], win["playQueueSelectedItemOffset"])
	}

	// Next (episode 584) and Previous (episode 582) advance and retreat across the window.
	for _, step := range []struct {
		key    core.RatingKey
		offset int
	}{
		{eps[583], 583},
		{eps[581], 581},
	} {
		tl := f.request(t, http.MethodGet, "/:/timeline?ratingKey="+step.key.String()+"&key=/library/metadata/"+step.key.String()+"&state=playing&time=1000&duration=1440000", headers)
		if tl.Code != http.StatusOK {
			t.Fatalf("timeline %s: %d %s", step.key, tl.Code, tl.Body.String())
		}
		after := decodeJSON(t, f.request(t, http.MethodGet, pqPath+"?window=50", headers))
		afterRows := after["Metadata"].([]any)
		if after["playQueueSelectedMetadataItemID"] != step.key.String() ||
			after["playQueueSelectedItemOffset"] != float64(step.offset) ||
			after["playQueueTotalCount"] != float64(totalEpisodes) ||
			afterRows[50].(map[string]any)["ratingKey"] != step.key.String() {
			t.Fatalf("after stepping to %s: selected=%v offset=%v total=%v center=%v",
				step.key, after["playQueueSelectedMetadataItemID"], after["playQueueSelectedItemOffset"], after["playQueueTotalCount"], afterRows[50].(map[string]any)["ratingKey"])
		}
	}

	// Shuffle places the selected item at offset 0; unshuffle restores its true offset.
	shuf := decodeJSON(t, f.request(t, http.MethodPut, pqPath+"/shuffle", headers))
	if shuf["playQueueSelectedItemOffset"] != float64(0) || shuf["playQueueTotalCount"] != float64(totalEpisodes) || shuf["playQueueVersion"] != float64(2) {
		t.Fatalf("shuffle: offset=%v total=%v version=%v", shuf["playQueueSelectedItemOffset"], shuf["playQueueTotalCount"], shuf["playQueueVersion"])
	}
	unshuf := decodeJSON(t, f.request(t, http.MethodPut, pqPath+"/unshuffle", headers))
	if unshuf["playQueueSelectedItemOffset"] != float64(581) || unshuf["playQueueTotalCount"] != float64(totalEpisodes) || unshuf["playQueueVersion"] != float64(3) {
		t.Fatalf("unshuffle: offset=%v total=%v version=%v", unshuf["playQueueSelectedItemOffset"], unshuf["playQueueTotalCount"], unshuf["playQueueVersion"])
	}

	// Starting at the final episode keeps a full 500-episode window ending at 1,180.
	endRec := f.request(t, http.MethodPost, "/playQueues?type=video&uri="+url.QueryEscape(showURI)+"&key="+url.QueryEscape("/library/metadata/"+eps[totalEpisodes-1].String()), headers)
	if endRec.Code != http.StatusOK {
		t.Fatalf("end POST /playQueues: %d %s", endRec.Code, endRec.Body.String())
	}
	endMC := decodeJSON(t, endRec)
	endRows := endMC["Metadata"].([]any)
	if endMC["size"] != float64(frontend.PlaylistExpandLimit) ||
		endMC["playQueueTotalCount"] != float64(totalEpisodes) ||
		endMC["playQueueSelectedItemOffset"] != float64(totalEpisodes-1) ||
		endRows[len(endRows)-1].(map[string]any)["ratingKey"] != eps[totalEpisodes-1].String() {
		t.Fatalf("end window: size=%v total=%v offset=%v", endMC["size"], endMC["playQueueTotalCount"], endMC["playQueueSelectedItemOffset"])
	}
}

func TestPlayQueueRejectsUnreachableStartKey(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()

	headers := map[string]string{"Accept": "application/json"}
	showURI := "server://0123456789abcdef0123456789abcdef01234567/com.plexapp.plugins.library/library/metadata/" + breakingBad.Key.String()

	for _, tc := range []struct {
		name string
		key  string
		want int
	}{
		{"wrong title", "/library/metadata/" + fightClub.Key.String(), http.StatusNotFound},
		{"missing episode", "/library/metadata/" + core.EpisodeKey(breakingBad.Key.TMDB(), 9, 99).String(), http.StatusNotFound},
		{"empty key", "", http.StatusBadRequest},
		{"malformed key", "not-a-key", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.request(t, http.MethodPost, "/playQueues?type=video&uri="+url.QueryEscape(showURI)+"&key="+url.QueryEscape(tc.key), headers)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	// A restricted viewer cannot start a playlist queue at a hidden item or
	// silently fall back to item 0.
	f.srv.testAuth().users["kid"] = core.User{ID: 30, Username: "kid", MaxContentRating: "PG"}
	pg := alien
	pg.ContentRating = "PG"
	f.catalog.titles[pg.Key] = pg
	pl, err := f.pls.Create(t.Context(), 30, "Mixed", []core.RatingKey{pg.Key, fightClub.Key})
	if err != nil {
		t.Fatal(err)
	}
	rec := f.request(t, http.MethodPost, "/playQueues?type=video&playlistID="+pl.Key.String()+"&key="+url.QueryEscape("/library/metadata/"+fightClub.Key.String()), map[string]string{
		"Accept":       "application/json",
		"X-Plex-Token": "kid",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("restricted start key: status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}
