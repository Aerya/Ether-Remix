package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend/kit/caps"
	"github.com/braedonsaunders/ether/internal/frontends/plex"
	"github.com/braedonsaunders/ether/internal/resolver"
)

func TestRX2MigrationMatrix(t *testing.T) {
	root := os.Getenv("ETHER_RX2_DIR")
	if root == "" {
		t.Skip("run scripts/check-rx2-migration.sh to compare with released Ether")
	}
	var fixtures []rx2Fixture
	rx2Read(t, filepath.Join(root, "fixtures.json"), &fixtures)
	if prod := os.Getenv("ETHER_RX2_PRODUCTION"); prod != "" {
		var fixture rx2Fixture
		rx2Read(t, prod, &fixture)
		fixtures = append(fixtures, fixture)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			a, dir := rx2Boot(t, fixture)
			t.Cleanup(a.Close)
			before := a.cfg.Get()
			got := rx2Matrix(t, a, fixture)
			a.Close()
			restarted, err := New(context.Background(), rx2Options(dir))
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			after := restarted.cfg.Get()
			if !reflect.DeepEqual(before.Quality, after.Quality) || !reflect.DeepEqual(before.Clients.Overrides, after.Clients.Overrides) {
				t.Error("quality or client overrides changed after a second startup")
			}
			if after.Quality.MigratedRules != 1 {
				t.Fatal("startup did not mark migration complete")
			}
			var want map[string]rx2Choice
			rx2Read(t, filepath.Join(root, fixture.Name+".json"), &want)
			if len(want) != len(got) {
				t.Fatalf("legacy/current matrix size: %d/%d", len(want), len(got))
			}
			var legacy struct {
				Transcode struct {
					RemoteMaxBitrateKbps int `json:"remoteMaxBitrateKbps"`
				} `json:"transcode"`
			}
			_ = json.Unmarshal(fixture.Config, &legacy)
			awayCap := legacy.Transcode.RemoteMaxBitrateKbps
			differences := map[string]int{}
			mismatched := map[string]bool{}
			examples := map[string]string{}
			record := func(key, label string) {
				differences[label]++
				mismatched[key] = true
				if examples[label] == "" || key < examples[label] {
					examples[label] = key
				}
			}
			for key, old := range want {
				next, ok := got[key]
				if !ok {
					t.Fatalf("missing cell %s", key)
				}
				if old.Profile["name"].(string)+" (TV)" == next.Profile["name"] && strings.Contains(key, "/episode/") {
					old.Profile["name"] = next.Profile["name"]
				}
				for k, v := range old.Profile {
					if !reflect.DeepEqual(v, next.Profile[k]) {
						if k == "customFormatScores" || k == "qualityOrder" {
							record(key, "profile."+k+" changed")
							continue
						}
						record(key, fmt.Sprintf("profile.%s: %v → %v", k, v, next.Profile[k]))
					}
				}
				for k, v := range next.Profile {
					if _, ok := old.Profile[k]; !ok {
						label := fmt.Sprintf("profile.%s: absent → %v", k, v)
						record(key, label)
					}
				}
				// Owner decision (Option C): a migrated server away bitrate cap
				// now also steers source selection for 4K plays instead of
				// picking a 4K release only to transcode it down at delivery.
				wantBitrate := old.Bitrate
				if awayCap > 0 && strings.HasSuffix(key, "/away=true") && old.Bitrate == 0 && rx2FourK(old.Profile) {
					wantBitrate = awayCap
				}
				for _, limit := range []struct {
					label     string
					old, next any
				}{{"bitrate", wantBitrate, next.Bitrate}, {"stereo", old.Stereo, next.Stereo}, {"blocked", old.Blocked, next.Blocked}} {
					if limit.old != limit.next {
						label := fmt.Sprintf("%s: %v → %v", limit.label, limit.old, limit.next)
						record(key, label)
					}
				}
			}
			var profiles []string
			for _, p := range after.Quality.Profiles {
				profiles = append(profiles, fmt.Sprintf("%s (%dp)", p.Name, p.MaxHeight))
			}
			t.Logf("%d decisions, %d with differences; profiles=%v home=%q away=%q rules=%s overrides=%s", len(got), len(mismatched), profiles, after.Quality.Default, after.Quality.DefaultAway, rx2JSON(t, after.Quality.Rules), rx2JSON(t, after.Clients.Overrides))
			changes := make([]string, 0, len(differences))
			for change := range differences {
				changes = append(changes, change)
			}
			sort.Strings(changes)
			for _, change := range changes {
				t.Errorf("%s in %d cells, e.g. %s", change, differences[change], examples[change])
			}
		})
	}
}

// rx2FourK reports whether a released profile allowed 4K, the plays whose
// away cap 0.3.20 dropped at delivery.
func rx2FourK(profile map[string]any) bool {
	h, _ := profile["maxHeight"].(float64)
	return h == 0 || h >= 2160
}

func rx2Resolve(t *testing.T, a *App, v rx2Viewer, device rx2Device, title *core.Title, remote bool) rx2Choice {
	t.Helper()
	ctx := context.Background()
	cfg := a.cfg.Read()
	u := &core.User{ID: v.ID}
	pr := &plex.Request{Product: device.Product, Platform: device.Platform, Device: device.Device, DeviceName: device.Device, Remote: remote, User: u}
	declared := pr.ClientCapsWith(0, cfg.Clients.Overrides)
	if frontendHostPolicy(device.Protocol).ProfileQualityEvaluation {
		info := caps.DeviceInfo{Protocol: device.Protocol, Product: device.Product, Platform: device.Platform, Device: device.Device, Remote: remote}
		neutral, err := (caps.Builder{}).Build(ctx, info, caps.DeviceProfile{}, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		declared = playbackFloor(neutral, cfg.Clients.Overrides)
	} else if device.Product == "" {
		declared = core.ClientCaps{Remote: remote, HDR10: true, DolbyVision: true}
	}
	member := a.membershipFor(ctx, title)
	if title.Type == core.TypeMovie && len(cfg.Quality.Rules) == 3 && len(cfg.Quality.Rules[0].Libraries) == 1 {
		genre := title.Genres[0]
		custom := core.LibraryID(cfg.Quality.Rules[0].Libraries[0])
		if member(core.LibraryMovies) != (genre == "Drama") || member(custom) != (genre == "Animation") {
			t.Fatalf("library fixture membership unexpectedly differs for %s", genre)
		}
	}
	profile := resolver.SelectProfileForTitle(cfg.Quality, declared, u, title, member)
	applied := resolver.ApplyClientRulesForTitle(cfg.Quality, declared, u, title, member)
	return rx2Choice{Profile: rx2Profile(t, profile, title), Bitrate: applied.MaxBitrateKbps, Stereo: applied.ConvertSurroundAudio, Blocked: remote && a.safety.ForUser(u).RemoteDisabled}
}
