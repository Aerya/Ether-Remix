package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type listProbePlayback struct {
	core.Playback
	resumed      map[core.RatingKey]listResumedEntry
	warm         map[core.RatingKey]core.MediaInfo
	preloadCalls int
	preloadedLen int
}

type listResumedEntry struct {
	info     core.MediaInfo
	rel      core.Release
	selector string
}

func (p *listProbePlayback) PreloadResumes(ctx context.Context, _ *core.User, keys []core.RatingKey) (context.Context, []core.RatingKey) {
	p.preloadCalls++
	p.preloadedLen += len(keys)
	var hits []core.RatingKey
	for _, k := range keys {
		if _, ok := p.resumed[k]; ok {
			hits = append(hits, k)
		}
	}
	return ctx, hits
}

func (p *listProbePlayback) ResumedVersion(_ context.Context, _ *core.User, key core.RatingKey, _ core.ClientCaps) (core.MediaInfo, core.Release, string, bool) {
	e, ok := p.resumed[key]
	if !ok {
		return core.MediaInfo{}, core.Release{}, "", false
	}
	return e.info, e.rel, e.selector, true
}

func (p *listProbePlayback) WarmProbeInfo(_ *core.User, _ core.ClientCaps, key core.RatingKey) (core.MediaInfo, bool) {
	info, ok := p.warm[key]
	return info, ok
}

type listSlotVariants struct {
	byKey map[core.RatingKey][]core.ReleaseVariant
}

func (v *listSlotVariants) VersionSlots(_ context.Context, req core.ResolveRequest, _ int, _ bool) ([]core.ReleaseVariant, error) {
	if req.Title == nil {
		return nil, nil
	}
	slots := v.byKey[req.Title.Key]
	return append([]core.ReleaseVariant(nil), slots...), nil
}

func (v *listSlotVariants) Variants(ctx context.Context, req core.ResolveRequest, max int) ([]core.ReleaseVariant, error) {
	return v.VersionSlots(ctx, req, max, true)
}

func decodeSectionItemsRaw(t *testing.T, body []byte) map[string]map[string]any {
	t.Helper()
	var root struct {
		MediaContainer struct {
			Metadata []map[string]any `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, body)
	}
	byKey := make(map[string]map[string]any, len(root.MediaContainer.Metadata))
	for _, item := range root.MediaContainer.Metadata {
		rk, _ := item["ratingKey"].(string)
		byKey[rk] = item
	}
	return byKey
}

func firstRawMediaAndPart(t *testing.T, item map[string]any) (map[string]any, map[string]any) {
	t.Helper()
	mediaList, ok := item["Media"].([]any)
	if !ok || len(mediaList) == 0 {
		t.Fatalf("item %v missing Media element", item["ratingKey"])
	}
	media, ok := mediaList[0].(map[string]any)
	if !ok {
		t.Fatalf("Media[0] is %T, want object", mediaList[0])
	}
	partList, ok := media["Part"].([]any)
	if !ok || len(partList) == 0 {
		t.Fatalf("item %v missing Part element", item["ratingKey"])
	}
	part, ok := partList[0].(map[string]any)
	if !ok {
		t.Fatalf("Part[0] is %T, want object", partList[0])
	}
	return media, part
}

var unprobedForbiddenMediaKeys = []string{
	"bitrate",
	"width",
	"height",
	"aspectRatio",
	"audioChannels",
	"audioCodec",
	"audioProfile",
	"videoCodec",
	"videoResolution",
	"videoFrameRate",
	"videoProfile",
	"optimizedForStreaming",
	"has64bitOffsets",
}

var unprobedForbiddenPartKeys = []string{
	"size",
	"videoProfile",
	"audioProfile",
	"optimizedForStreaming",
	"has64bitOffsets",
}

func TestSectionListingOmitsPlaceholderMediaForUnprobedTitles(t *testing.T) {
	unknownRuntimeMovie := core.Title{
		Key:       core.MovieKey(9001),
		Type:      core.TypeMovie,
		Title:     "Unknown Runtime",
		Year:      2024,
		UpdatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}

	makeCold4KEstimate := func() core.MediaInfo {
		return core.MediaInfo{
			Estimated:   true,
			Container:   "mkv",
			Duration:    110 * time.Minute,
			Size:        16_500_000_000,
			BitrateKbps: 20000,
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "h264", Profile: "high", Width: 3840, Height: 2160, FrameRate: 23.976, Default: true},
				{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Default: true},
			},
		}
	}

	for _, tc := range []struct {
		name      string
		withSlots bool
	}{
		{name: "plain-estimate", withSlots: false},
		{name: "cold-configured-slots", withSlots: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			defer f.srv.Close()
			f.catalog.titles[unknownRuntimeMovie.Key] = unknownRuntimeMovie
			f.playback.info = makeCold4KEstimate()

			if tc.withSlots {
				coldSlot := core.ReleaseVariant{
					ID:    frontend.VersionSelectorPrefix + "auto",
					Label: "Auto",
					Auto:  true,
					Release: core.Release{
						ID:   frontend.VersionSelectorPrefix + "auto",
						Name: "Auto",
						Info: core.ReleaseInfo{Height: 2160, VideoCodec: "h264", AudioCodec: "aac", Source: core.SourceWEBDL},
					},
					Info: makeCold4KEstimate(),
				}
				f.srv.setVariants(&listSlotVariants{
					byKey: map[core.RatingKey][]core.ReleaseVariant{
						fightClub.Key:           {coldSlot},
						unknownRuntimeMovie.Key: {coldSlot},
					},
				})
			}

			rec := f.get(t, "/library/sections/1/all", jsonAccept...)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			items := decodeSectionItemsRaw(t, rec.Body.Bytes())

			for _, title := range []core.Title{fightClub, unknownRuntimeMovie} {
				item, ok := items[title.Key.String()]
				if !ok {
					t.Fatalf("missing item %s", title.Key)
				}
				media, part := firstRawMediaAndPart(t, item)
				if media["id"] == nil || media["container"] != "mkv" {
					t.Fatalf("%s: playable Media identity missing: %+v", title.Title, media)
				}
				if part["id"] == nil || part["key"] == "" || part["file"] == "" || part["container"] != "mkv" {
					t.Fatalf("%s: playable Part identity missing: %+v", title.Title, part)
				}
				for _, k := range unprobedForbiddenMediaKeys {
					if v, exists := media[k]; exists {
						t.Errorf("%s: unprobed list Media leaked placeholder %s=%v", title.Title, k, v)
					}
				}
				for _, k := range unprobedForbiddenPartKeys {
					if v, exists := part[k]; exists {
						t.Errorf("%s: unprobed list Part leaked placeholder %s=%v", title.Title, k, v)
					}
				}
				if title.Runtime > 0 {
					wantDur := float64(title.Runtime.Milliseconds())
					if media["duration"] != wantDur || part["duration"] != wantDur {
						t.Errorf("%s: duration=%v part.duration=%v, want %v", title.Title, media["duration"], part["duration"], wantDur)
					}
				} else {
					if _, exists := media["duration"]; exists {
						t.Errorf("%s: unknown-runtime title leaked estimated Media.duration=%v", title.Title, media["duration"])
					}
					if _, exists := part["duration"]; exists {
						t.Errorf("%s: unknown-runtime title leaked estimated Part.duration=%v", title.Title, part["duration"])
					}
				}
			}
		})
	}
}

func TestSectionListingReportsProbedAndClaimedMediaBatched(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()

	claimedMovie := core.Title{
		Key:       core.MovieKey(9002),
		Type:      core.TypeMovie,
		Title:     "Claimed Release",
		Year:      2025,
		Runtime:   100 * time.Minute,
		UpdatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
	unprobedMovie := core.Title{
		Key:       core.MovieKey(9003),
		Type:      core.TypeMovie,
		Title:     "Cold Movie",
		Year:      2025,
		Runtime:   95 * time.Minute,
		UpdatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
	f.catalog.titles[claimedMovie.Key] = claimedMovie
	f.catalog.titles[unprobedMovie.Key] = unprobedMovie

	resumedProbe := core.MediaInfo{
		Container:   "mkv",
		Duration:    139 * time.Minute,
		Size:        12_345_678_900,
		BitrateKbps: 11842,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Profile: "main 10", Width: 3840, Height: 1600, FrameRate: 23.976, BitDepth: 10, Default: true},
			{Index: 1, Type: "audio", Codec: "truehd", Channels: 8, Default: true},
		},
	}
	warmProbe := core.MediaInfo{
		Container:   "mp4",
		Duration:    117 * time.Minute,
		Size:        4_500_000_000,
		BitrateKbps: 5128,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "av1", Width: 1920, Height: 1080, FrameRate: 24, Default: true},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Default: true},
		},
	}
	pb := &listProbePlayback{
		Playback: f.playback,
		resumed: map[core.RatingKey]listResumedEntry{
			fightClub.Key: {
				info: resumedProbe,
				rel:  core.Release{ID: "rel-resumed", Name: "Fjord.Harbor.2160p.HEVC"},
			},
		},
		warm: map[core.RatingKey]core.MediaInfo{
			alien.Key: warmProbe,
		},
	}
	f.srv.setPlayback(pb)

	coldAuto := func() core.ReleaseVariant {
		return core.ReleaseVariant{
			ID:    frontend.VersionSelectorPrefix + "auto",
			Label: "Auto",
			Auto:  true,
			Release: core.Release{
				ID:   frontend.VersionSelectorPrefix + "auto",
				Name: "Auto",
				Info: core.ReleaseInfo{Height: 2160, VideoCodec: "h264", AudioCodec: "aac"},
			},
			Info: core.MediaInfo{
				Estimated:   true,
				Container:   "mkv",
				BitrateKbps: 20000,
				Streams: []core.StreamInfo{
					{Index: 0, Type: "video", Codec: "h264", Width: 3840, Height: 2160, FrameRate: 23.976},
					{Index: 1, Type: "audio", Codec: "eac3", Channels: 6},
				},
			},
		}
	}
	claimedSlot := core.ReleaseVariant{
		ID:    frontend.VersionSelectorPrefix + "auto",
		Label: "Auto · 2160p HEVC",
		Auto:  true,
		Release: core.Release{
			ID:   "rel-claimed-1",
			Name: "Claimed.Release.2025.2160p.WEB-DL.HEVC.DDP5.1",
			Size: 10_000_000_000,
			Info: core.ReleaseInfo{
				Height:     2160,
				VideoCodec: "hevc",
				AudioCodec: "eac3",
				Channels:   "5.1",
			},
		},
		Info: core.MediaInfo{
			Estimated:   true,
			Container:   "mkv",
			BitrateKbps: 20000,
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "h264", Profile: "high", Width: 3840, Height: 2160, FrameRate: 23.976},
				{Index: 1, Type: "audio", Codec: "aac", Channels: 2},
			},
		},
	}
	f.srv.setVariants(&listSlotVariants{
		byKey: map[core.RatingKey][]core.ReleaseVariant{
			fightClub.Key:     {coldAuto()},
			alien.Key:         {coldAuto()},
			claimedMovie.Key:  {claimedSlot},
			unprobedMovie.Key: {coldAuto()},
		},
	})

	rec := f.get(t, "/library/sections/1/all", jsonAccept...)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if pb.preloadCalls != 1 || pb.preloadedLen != 4 {
		t.Fatalf("PreloadResumes calls=%d keys=%d, want 1 batch call for 4 movies", pb.preloadCalls, pb.preloadedLen)
	}

	items := decodeSectionItemsRaw(t, rec.Body.Bytes())

	// 1. Probed via batched ResumedVersion: reports real probed facts.
	resumedMedia, resumedPart := firstRawMediaAndPart(t, items[fightClub.Key.String()])
	if resumedMedia["width"] != float64(3840) || resumedMedia["height"] != float64(1600) ||
		resumedMedia["videoResolution"] != "4k" || resumedMedia["videoCodec"] != "hevc" ||
		resumedMedia["bitrate"] != float64(11842) || resumedMedia["audioCodec"] != "truehd" ||
		resumedMedia["audioChannels"] != float64(8) || resumedPart["size"] != float64(12_345_678_900) {
		t.Errorf("resumed probed row = %+v part=%+v", resumedMedia, resumedPart)
	}

	// 2. Probed via WarmProbeInfo: reports real probed facts.
	warmMedia, warmPart := firstRawMediaAndPart(t, items[alien.Key.String()])
	if warmMedia["width"] != float64(1920) || warmMedia["height"] != float64(1080) ||
		warmMedia["videoCodec"] != "av1" || warmMedia["bitrate"] != float64(5128) ||
		warmMedia["container"] != "mp4" || warmPart["size"] != float64(4_500_000_000) {
		t.Errorf("warm probed row = %+v part=%+v", warmMedia, warmPart)
	}

	// 3. Unprobed picked release: reports only what release name + size claim.
	claimedMedia, claimedPart := firstRawMediaAndPart(t, items[claimedMovie.Key.String()])
	if claimedMedia["height"] != float64(2160) || claimedMedia["videoResolution"] != "4k" ||
		claimedMedia["videoCodec"] != "hevc" || claimedMedia["audioCodec"] != "eac3" ||
		claimedMedia["audioChannels"] != float64(6) || claimedPart["size"] != float64(10_000_000_000) {
		t.Errorf("claimed release row = %+v part=%+v", claimedMedia, claimedPart)
	}
	wantClaimedBitrate := float64(int(float64(10_000_000_000) * 8 / (100 * time.Minute).Seconds() / 1000))
	if claimedMedia["bitrate"] != wantClaimedBitrate {
		t.Errorf("claimed bitrate = %v, want %v", claimedMedia["bitrate"], wantClaimedBitrate)
	}
	for _, k := range []string{"width", "aspectRatio", "videoFrameRate", "videoProfile"} {
		if v, exists := claimedMedia[k]; exists {
			t.Errorf("claimed unprobed row invented %s=%v", k, v)
		}
	}

	// 4. Cold unprobed movie: omits all placeholder fields.
	coldMedia, coldPart := firstRawMediaAndPart(t, items[unprobedMovie.Key.String()])
	for _, k := range unprobedForbiddenMediaKeys {
		if v, exists := coldMedia[k]; exists {
			t.Errorf("cold row leaked %s=%v", k, v)
		}
	}
	for _, k := range unprobedForbiddenPartKeys {
		if v, exists := coldPart[k]; exists {
			t.Errorf("cold row Part leaked %s=%v", k, v)
		}
	}
}

func TestUniversalDecisionReturnsOutputMediaQualityForHLS(t *testing.T) {
	tests := []struct {
		name           string
		query          string
		decision       core.Decision
		wantWidth      int
		wantHeight     int
		wantBitrate    int
		wantVideoCodec string
	}{
		{
			name:  "hls-transcode-scaled",
			query: "path=/library/metadata/" + fightClub.Key.String() + "&protocol=hls&directPlay=0&directStream=1&maxVideoBitrate=4000&videoResolution=1280x720&session=vela-hls-transcode",
			decision: core.Decision{
				Kind:        core.DecisionTranscode,
				VideoCodec:  "h264",
				AudioCodec:  "aac",
				Container:   "mpegts",
				MaxHeight:   720,
				BitrateKbps: 4000,
			},
			wantWidth:      1280,
			wantHeight:     720,
			wantBitrate:    4000,
			wantVideoCodec: "h264",
		},
		{
			name:  "hls-remux-copy",
			query: "path=/library/metadata/" + fightClub.Key.String() + "&protocol=hls&directPlay=0&directStream=1&session=vela-hls-copy",
			decision: core.Decision{
				Kind:       core.DecisionRemux,
				VideoCopy:  true,
				AudioCopy:  false,
				AudioCodec: "aac",
				Container:  "mpegts",
			},
			wantWidth:      1920,
			wantHeight:     1080,
			wantBitrate:    7600,
			wantVideoCodec: "h264",
		},
		{
			name:  "hls-direct-play",
			query: "path=/library/metadata/" + fightClub.Key.String() + "&protocol=hls&directPlay=1&directStream=1&session=vela-hls-direct",
			decision: core.Decision{
				Kind:      core.DecisionDirectPlay,
				VideoCopy: true,
				AudioCopy: true,
				Container: "mkv",
			},
			wantWidth:      1920,
			wantHeight:     1080,
			wantBitrate:    7600,
			wantVideoCodec: "h264",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			defer f.srv.Close()
			f.trans.decision = tc.decision

			// First verify the section listing omits placeholder values before playback.
			listRec := f.get(t, "/library/sections/1/all", jsonAccept...)
			if listRec.Code != http.StatusOK {
				t.Fatalf("section listing status = %d: %s", listRec.Code, listRec.Body.String())
			}
			listItems := decodeSectionItemsRaw(t, listRec.Body.Bytes())
			listMedia, _ := firstRawMediaAndPart(t, listItems[fightClub.Key.String()])
			if _, exists := listMedia["bitrate"]; exists {
				t.Fatalf("section listing before decision leaked bitrate=%v", listMedia["bitrate"])
			}

			// Then verify /video/:/transcode/universal/decision returns Metadata[0].Media[0]
			// output width, height, bitrate, and videoCodec.
			decRec := f.get(t, "/video/:/transcode/universal/decision?"+tc.query, jsonAccept...)
			if decRec.Code != http.StatusOK {
				t.Fatalf("decision status = %d: %s", decRec.Code, decRec.Body.String())
			}
			var root struct {
				MediaContainer struct {
					Metadata []struct {
						Media []struct {
							Width      int    `json:"width"`
							Height     int    `json:"height"`
							Bitrate    int    `json:"bitrate"`
							VideoCodec string `json:"videoCodec"`
						} `json:"Media"`
					} `json:"Metadata"`
				} `json:"MediaContainer"`
			}
			if err := json.Unmarshal(decRec.Body.Bytes(), &root); err != nil {
				t.Fatalf("decode decision JSON: %v\n%s", err, decRec.Body.String())
			}
			if len(root.MediaContainer.Metadata) == 0 || len(root.MediaContainer.Metadata[0].Media) == 0 {
				t.Fatalf("decision response missing Metadata[0].Media[0]: %s", decRec.Body.String())
			}
			got := root.MediaContainer.Metadata[0].Media[0]
			if got.Width != tc.wantWidth || got.Height != tc.wantHeight || got.Bitrate != tc.wantBitrate || got.VideoCodec != tc.wantVideoCodec {
				t.Fatalf("Metadata[0].Media[0] = %+v, want width=%d height=%d bitrate=%d videoCodec=%q",
					got, tc.wantWidth, tc.wantHeight, tc.wantBitrate, tc.wantVideoCodec)
			}
		})
	}
}
