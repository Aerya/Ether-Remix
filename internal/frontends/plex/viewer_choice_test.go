package plex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type rememberedSlotVariants struct {
	*fakeVariantResolver
	plan []core.ReleaseVariant
}

func (v *rememberedSlotVariants) VersionSlots(_ context.Context, _ core.ResolveRequest, _ int, _ bool) ([]core.ReleaseVariant, error) {
	return append([]core.ReleaseVariant(nil), v.plan...), nil
}

func TestPlexSlotMediaHonoursRememberedViewerChoice(t *testing.T) {
	f, _ := newVariantPlaybackFixture(t)
	rememberedRel := core.Release{
		ID:   "rel-remembered-1080p-remux",
		Name: "Fight.Club.1999.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP",
		Info: core.ReleaseInfo{Height: 1080, Source: core.SourceRemux, VideoCodec: "h264", AudioCodec: "dtshd", Channels: "5.1", Group: "GRP"},
	}
	otherRel := core.Release{
		ID:   "rel-4k-webdl",
		Name: "Fight.Club.1999.2160p.WEB-DL.HEVC.DDP5.1-GRP",
		Info: core.ReleaseInfo{Height: 2160, Source: core.SourceWEBDL, VideoCodec: "hevc", AudioCodec: "eac3", Channels: "5.1", Group: "GRP"},
	}
	plan := []core.ReleaseVariant{
		{
			ID: "slot:auto", Auto: true, Remembered: true,
			Label:   "Auto · 1080p Remux · DTS-HD 5.1",
			Release: rememberedRel,
			Info: core.MediaInfo{
				Container: "mkv", Duration: fightClub.Runtime,
				Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080}, {Index: 1, Type: "audio", Codec: "dts", Channels: 6, Language: "eng", Default: true}},
			},
		},
		{
			ID: "slot:alt0", Label: "4K WEB-DL · DD+ 5.1",
			Release: otherRel,
			Info: core.MediaInfo{
				Container: "mkv", Duration: fightClub.Runtime,
				Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160}, {Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "eng", Default: true}},
			},
		},
	}
	f.srv.setVariants(&rememberedSlotVariants{fakeVariantResolver: f.resolver, plan: plan})

	w := f.get(t, "/library/metadata/"+fightClub.Key.String(), "Accept", "application/json", "X-Plex-Product", "Plex Web")

	media := configuredResponseMedia(t, w, fightClub.Key)
	if len(media) != 2 {
		t.Fatalf("len(Media) = %d, want 2", len(media))
	}
	if !bool(media[0].Selected) {
		t.Fatalf("Media[0].Selected = false; want true for remembered Auto slot")
	}
	var doc struct {
		MediaContainer struct {
			Metadata []struct {
				Media []struct {
					VideoResolution string `json:"videoResolution"`
					Title           string `json:"title"`
				} `json:"Media"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil || len(doc.MediaContainer.Metadata) != 1 {
		t.Fatalf("decode metadata: %v", err)
	}
	got := doc.MediaContainer.Metadata[0].Media
	if len(got) != 2 || got[0].VideoResolution != "1080" {
		t.Fatalf("Media[0] = %+v; want videoResolution=1080 from remembered choice", got)
	}
}
