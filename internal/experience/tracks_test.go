package experience

import (
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestPickAudioPreferencesAndFallback(t *testing.T) {
	info := core.MediaInfo{Streams: []core.StreamInfo{{Type: "subtitle"}, {Type: "audio", Language: "fr", Default: true}, {Type: "audio", Language: "pt-PT"}, {Type: "audio", Language: "pt-BR"}}}
	if got := PickAudio(info, []string{"pt-BR", "fr"}); got != 3 {
		t.Fatalf("exact preference: %d", got)
	}
	if got := PickAudio(info, []string{"de"}); got != 1 {
		t.Fatalf("default: %d", got)
	}
	info.Streams[1].Default = false
	if got := PickAudio(info, nil); got != 1 {
		t.Fatalf("first: %d", got)
	}
	if got := PickAudio(core.MediaInfo{}, nil); got != -1 {
		t.Fatalf("none: %d", got)
	}
}

func TestResolveSubtitlePreservesIdentityAndFallback(t *testing.T) {
	info := core.MediaInfo{Streams: []core.StreamInfo{{Type: "subtitle", Language: "en", Index: 5}, {Type: "subtitle", Language: "en", External: true, ExternalURL: "sidecar", Forced: true}, {Type: "subtitle", Language: "en", Forced: true}}}
	embedded := &core.StreamInfo{Type: "subtitle", Language: "en", Index: 5}
	if got := ResolveSubtitle(embedded, 0, info); got != 0 {
		t.Fatalf("embedded: %d", got)
	}
	external := &core.StreamInfo{Type: "subtitle", Language: "fr", External: true, ExternalURL: "sidecar"}
	if got := ResolveSubtitle(external, -1, info); got != 1 {
		t.Fatalf("sidecar identity: %d", got)
	}
	forced := &core.StreamInfo{Type: "subtitle", Language: "en", Forced: true}
	if got := ResolveSubtitle(forced, -1, info); got != 2 {
		t.Fatalf("forced same kind: %d", got)
	}
	if got := ResolveSubtitle(nil, 1, info); got != 1 {
		t.Fatalf("position: %d", got)
	}
	if got := ResolveSubtitle(nil, 5, info); got != -1 {
		t.Fatalf("invalid: %d", got)
	}
}

func TestResolveSubtitlePrefersCodecOverTheOldSlot(t *testing.T) {
	want := &core.StreamInfo{Type: "subtitle", Language: "es", Codec: "srt", Index: 3}
	info := core.MediaInfo{Streams: []core.StreamInfo{
		{Type: "video"},
		{Type: "subtitle", Language: "es", Codec: "hdmv_pgs_subtitle", Index: 3},
		{Type: "subtitle", Language: "es", Codec: "srt", Index: 9},
	}}
	if got := ResolveSubtitle(want, 1, info); got != 2 {
		t.Fatalf("Spanish SRT at the new index = %d, want 2", got)
	}
}

func TestPickAudioPenalizesCommentaryAndVisualImpaired(t *testing.T) {
	tr := true
	info := core.MediaInfo{Streams: []core.StreamInfo{
		{Type: "audio", Language: "en", Commentary: &tr},
		{Type: "audio", Language: "en", VisualImpaired: &tr},
		{Type: "audio", Language: "en"},
	}}
	if got := PickAudio(info, []string{"en"}); got != 2 {
		t.Fatalf("PickAudio = %d, want 2 (main track over commentary/AD)", got)
	}
}

// A wanted language heard only as commentary loses to the file's main
// tracks (default-flagged first); it plays only when nothing else exists.
func TestPickAudioWantedCommentaryIsLastResort(t *testing.T) {
	info := core.MediaInfo{Streams: []core.StreamInfo{
		{Type: "audio", Language: "spa"},
		{Type: "audio", Language: "eng", Title: "Commentary by the director"},
		{Type: "audio", Language: "ita", Default: true},
	}}
	if got := PickAudio(info, []string{"en"}); got != 2 {
		t.Fatalf("PickAudio = %d, want 2 (default main track over English commentary)", got)
	}
	only := core.MediaInfo{Streams: []core.StreamInfo{{Type: "audio", Language: "eng", Title: "Commentary"}}}
	if got := PickAudio(only, []string{"en"}); got != 0 {
		t.Fatalf("commentary only = %d, want 0", got)
	}
}
