package livetv

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestChannelArchiveURL(t *testing.T) {
	const server = "https://example.invalid"
	start := time.Date(2026, 11, 23, 14, 5, 6, 0, time.UTC).In(time.FixedZone("viewer", -5*60*60))
	const dur = 61 * time.Second
	const utc, end = "1795442706", "1795442767"
	tests := []struct {
		name, mode, live, template, timezone, want string
	}{
		{name: "xtream", mode: "xtream", live: server + "/panel/live/view%2Fer/test%3F/123.m3u8?token=demo", timezone: "America/Toronto", want: server + "/panel/timeshift/view%2Fer/test%3F/2/2026-11-23:09-05/123.ts"},
		{name: "xtream UTC fallback", mode: "xtream", live: server + "/live/viewer/test/123.ts", want: server + "/timeshift/viewer/test/2/2026-11-23:14-05/123.ts"},
		{name: "xtream panel UTC", mode: "xtream", live: server + "/live/viewer/test/123.ts", timezone: "UTC", want: server + "/timeshift/viewer/test/2/2026-11-23:14-05/123.ts"},
		{name: "xc", mode: "xc", live: server + "/live/viewer/test/123.ts", want: server + "/timeshift/viewer/test/2/2026-11-23:14-05/123.ts"},
		{name: "xc explicit template", mode: "xc", live: server + "/channel.ts", template: server + "/archive/{utc}", want: server + "/archive/" + utc},
		{name: "default", mode: "default", template: server + "/{(b)YmdHMS}/${(b)Y-m-d}/{Y}{m}{d}{H}{M}{S}?t={utc},{start},${start}&e={utcend},{end},${end}&n={lutc},${now},{timestamp}&d={duration},${duration},{duration:60}&o={offset}",
			want: server + "/20261123140506/2026-11-23/20261123140506?t=" + strings.Join([]string{utc, utc, utc}, ",") + "&e=" + strings.Join([]string{end, end, end}, ",") + "&n=" + strings.Join([]string{end, end, end}, ",") + "&d=61,61,1&o=61"},
		{name: "append", mode: "append", live: server + "/live.m3u8?sig=one%20two#part", template: "?start={utc}", want: server + "/live.m3u8?sig=one%20two&start=" + utc + "#part"},
		{name: "shift", mode: "shift", live: server + "/live.m3u8?sig=one%20two", want: server + "/live.m3u8?sig=one%20two&utc=" + utc + "&lutc=" + end},
		{name: "flussonic HLS", mode: "flussonic", live: server + "/channel/index.m3u8?sig=demo", want: server + "/channel/index-" + utc + "-61.m3u8?sig=demo"},
		{name: "flussonic TS", mode: "flussonic", live: server + "/channel/mpegts", want: server + "/channel/timeshift_abs-" + utc + ".ts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := Channel{StreamURL: tt.live, archiveTimezone: tt.timezone}
			a := ChannelArchive{Days: 2, Mode: tt.mode, Source: tt.template}
			got, err := a.URL(ch, start, dur)
			if err != nil || got != tt.want {
				t.Fatal("archive URL did not match the golden")
			}
		})
	}
}

func TestM3UArchiveDefaultsAndOverrides(t *testing.T) {
	playlist := `#EXTM3U catchup-type='append' tvg-rec='2' catchup-source='?start={utc}'
#EXTINF:-1, Default
https://example.invalid/ch1
#EXTINF:-1 catchup="xc" timeshift="40", Xtream
https://example.invalid/live/viewer/test/2.ts
#EXTINF:-1 catchup="unknown", Unknown
https://example.invalid/ch3
#EXTINF:-1 catchup-days="0", Disabled
https://example.invalid/ch4
`
	channels, err := ParseM3U(strings.NewReader(playlist), "fixture")
	if err != nil || len(channels) != 4 {
		t.Fatal("expected four imported channels")
	}
	wantDefault := ChannelArchive{Days: 2, Mode: "append", Source: "?start={utc}"}
	wantXtream := ChannelArchive{Days: 30, Mode: "xtream"}
	if channels[0].Archive == nil || *channels[0].Archive != wantDefault || channels[1].Archive == nil || *channels[1].Archive != wantXtream ||
		channels[2].Archive != nil || channels[3].Archive != nil {
		t.Fatal("unexpected archive capabilities after applying playlist defaults")
	}
}

func TestXtreamArchiveFields(t *testing.T) {
	var streams []xtreamStream
	if err := json.Unmarshal([]byte(`[
		{"stream_id":1,"tv_archive":1,"tv_archive_duration":2},
		{"stream_id":2,"tv_archive":true,"tv_archive_duration":"2"},
		{"stream_id":3,"tv_archive":"false","tv_archive_duration":2},
		{"stream_id":4,"tv_archive":1,"tv_archive_duration":0},
		{"stream_id":5}
	]`), &streams); err != nil {
		t.Fatal("could not decode the live streams")
	}
	channels := xtreamChannels("fixture", "https://example.invalid", &SourceAuth{Username: "viewer", Password: "test"}, "ts", nil, streams, nil)
	if len(channels) != 5 {
		t.Fatal("expected five channels")
	}
	for i, c := range channels {
		if i < 2 {
			if c.Archive == nil || *c.Archive != (ChannelArchive{Days: 2, Mode: "xtream"}) {
				t.Fatal("archive capability was not read from the Xtream stream")
			}
		} else if c.Archive != nil {
			t.Fatal("stream without a positive archive window advertised an archive")
		}
	}
}

func TestArchiveTemplateSealing(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(3600, 0).UTC()
	s := testService(t, Options{Sealer: upgradedSealer(t), Now: func() time.Time { return now }})
	template := &ChannelArchive{Days: 2, Mode: "default", Source: "https://example.invalid/archive?from={utc}&now={lutc}&offset={offset}"}
	ch := Channel{ID: "ch", SourceID: "fixture", Archive: template}
	if err := s.sealArchive(&ch, Channel{}); err != nil || ch.Archive.Source == template.Source {
		t.Fatal("archive template was not sealed")
	}
	refreshed := Channel{ID: ch.ID, SourceID: ch.SourceID, Archive: template}
	if err := s.sealArchive(&refreshed, ch); err != nil || refreshed.Archive.Source != ch.Archive.Source {
		t.Fatal("unchanged archive template was resealed")
	}
	if got, err := s.ArchiveURL(ctx, ch, now.Add(-time.Hour), time.Minute); err != nil ||
		got != "https://example.invalid/archive?from=0&now=3600&offset=3600" {
		t.Fatal("archive playback did not use the sealed template and injected clock")
	}
	refreshed.redactArchiveSource()
	if refreshed.Archive.Source != "" || ch.Archive.Source == "" {
		t.Fatal("redaction exposed the template or mutated the original channel")
	}
}
