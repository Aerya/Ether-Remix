package plex

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/trackprefs"
)

func TestAppleTVPersistedVersionEmbeddedSubtitles(t *testing.T) {
	f, pb := newVariantPlaybackFixture(t)
	info := sampleInfo
	info.Streams = append([]core.StreamInfo(nil), sampleInfo.Streams...)
	info.Streams = append(info.Streams,
		core.StreamInfo{Index: 2, Type: "subtitle", Codec: "pgssub", Language: "eng"},
		core.StreamInfo{Index: 7, Type: "subtitle", Codec: "srt", Language: "fra"})
	info.Estimated = false
	pb.info = info
	// The editions surface now supplies persisted facts for each release.
	f.resolver.byKey[fightClub.Key][1].Info = info
	headers := []string{"X-Plex-Product", "Plex for Apple TV", "X-Plex-Platform", "tvOS"}
	detail := f.get(t, "/library/metadata/"+fightClub.Key.String(), headers...)
	if detail.Code != 200 {
		t.Fatal(detail.Body.String())
	}
	pgs := ""
	for _, tag := range subtitleTags(detail.Body.String()) {
		if strings.Contains(tag, `codec="pgs"`) && strings.Contains(tag, `index="2"`) {
			pgs = streamAttr(tag, "id")
		}
	}
	if pgs == "" || !strings.Contains(detail.Body.String(), `codec="srt"`) {
		t.Fatalf("persisted variant tracks missing: %s", detail.Body.String())
	}
	decision := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+fightClub.Key.String()+"&mediaIndex=1&session=appletv-pgs&subtitleStreamID="+pgs, headers...)
	if decision.Code != 200 {
		t.Fatalf("decision: %d %s", decision.Code, decision.Body.String())
	}
	found := false
	for _, tag := range subtitleTags(decision.Body.String()) {
		if strings.Contains(tag, `codec="pgs"`) && strings.Contains(tag, `selected="1"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("selected PGS missing from alternate decision: %s", decision.Body.String())
	}
}

// A version can have no embedded tracks until Play probes it. The decision
// must publish that release's whole subtitle menu, preserve its issued IDs on
// refresh, and allow an unselected track to be picked without choosing Auto.
func TestDecisionCarriesProbedVersionSubtitles(t *testing.T) {
	for _, client := range []struct{ product, platform string }{
		{"Plex for Apple TV", "tvOS"},
		{"Plex for iOS", "iOS"},
		{"Plex Web", "Chrome"},
		{"Plex for Android (TV)", "Android"},
	} {
		for _, kind := range []core.DecisionKind{core.DecisionDirectPlay, core.DecisionRemux} {
			for _, slot := range []int{0, 1} {
				t.Run(fmt.Sprintf("%s/%s/slot%d", client.product, kind, slot), func(t *testing.T) {
					f, pb, _ := configuredVariantFixture(t)
					info := sampleInfo
					info.Estimated = false
					info.Streams = append([]core.StreamInfo(nil), sampleInfo.Streams[:2]...)
					// The two releases deliberately place English at different
					// indexes, as in the issue's primary/alternate A/B.
					eng := 2 + slot*5
					info.Streams = append(info.Streams,
						core.StreamInfo{Index: eng, Type: "subtitle", Codec: "srt", Language: "eng"},
						core.StreamInfo{Index: eng + 1, Type: "subtitle", Codec: "srt", Language: "fra"})
					f.srv.setTranscoder(newFakeTranscoder(core.Decision{Kind: kind, VideoCopy: true, AudioCopy: true, Container: "mkv"}))
					prefs := trackprefs.New(store.OpenTest(t))
					rules := trackprefs.Default()
					rules.SubtitleLanguages, rules.SubtitleMode = []string{"en"}, "always"
					if err := prefs.PutRules(context.Background(), ownerUser.ID, rules); err != nil {
						t.Fatal(err)
					}
					f.srv.setTrackPreferences(prefs)
					headers := []string{"X-Plex-Product", client.product, "X-Plex-Platform", client.platform}
					key := fightClub.Key.String()
					f.get(t, "/library/metadata/"+key, headers...)
					pb.info = info
					pb.resolution = &core.Resolution{Release: core.Release{ID: "rel-auto"}}
					directPlay := 0
					if kind == core.DecisionDirectPlay {
						directPlay = 1
					}
					query := fmt.Sprintf("/video/:/transcode/universal/decision?path=/library/metadata/%s&mediaIndex=%d&session=version-subs&protocol=hls&directPlay=%d&directStream=1", key, slot, directPlay)
					response := f.get(t, query, headers...)
					if response.Code != 200 {
						t.Fatal(response.Code, response.Body.String())
					}
					type track struct {
						ID       string `xml:"id,attr"`
						Index    int    `xml:"index,attr"`
						Type     int    `xml:"streamType,attr"`
						Language string `xml:"languageCode,attr"`
						Selected int    `xml:"selected,attr"`
					}
					read := func(body string) (string, []track) {
						t.Helper()
						var mc struct {
							Media []struct {
								ID       string  `xml:"id,attr"`
								Selected int     `xml:"selected,attr"`
								Streams  []track `xml:"Part>Stream"`
							} `xml:"Video>Media"`
						}
						if err := xml.Unmarshal([]byte(body), &mc); err != nil {
							t.Fatal(err)
						}
						for _, m := range mc.Media {
							if m.Selected != 1 {
								continue
							}
							var subs []track
							for _, st := range m.Streams {
								if st.Type == 3 {
									subs = append(subs, st)
								}
							}
							return m.ID, subs
						}
						t.Fatal("no selected Media", body)
						return "", nil
					}
					id, subs := read(response.Body.String())
					if len(subs) != 2 || subs[0].Language != "eng" || subs[0].Selected != 1 || subs[1].Selected == 1 || subs[1].Index != eng+1 {
						t.Fatalf("probed subtitle menu/preference missing: %+v\n%s", subs, response.Body.String())
					}
					refresh := f.get(t, "/library/metadata/"+key, headers...)
					refreshID, refreshed := read(refresh.Body.String())
					if refreshID != id || len(refreshed) != 2 || refreshed[1].ID != subs[1].ID {
						t.Fatalf("refresh changed version/tracks: %s %+v", refreshID, refreshed)
					}
					picked := f.get(t, query+"&subtitleStreamID="+subs[1].ID, headers...)
					pickedID, pickedSubs := read(picked.Body.String())
					if picked.Code != 200 || pickedID != id || len(pickedSubs) != 2 || pickedSubs[1].Selected != 1 {
						t.Fatalf("alternate subtitle not selectable: %s", picked.Body.String())
					}
				})
			}
		}
	}
}
