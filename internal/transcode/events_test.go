package transcode

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/events"
)

func TestTranscodeEmitsFallbackEvent(t *testing.T) {
	bus, rec := events.NewTestBus(t)
	reg := events.NewBuiltinRegistry()

	fr := newFakeRunner()
	fr.failStart = func(args []string) (string, bool) {
		if argValue(args, "-c:v") == "h264_videotoolbox" {
			return "[h264_videotoolbox @ 0x1] Error: cannot create compression session: -12902", true
		}
		return "", false
	}
	m := newTestManager(t, fr, config.Transcode{HWAccel: "auto"}, WithEvents(bus))
	s := startSession(t, m, transcodeRequest(t, "hw-fb"))
	if r := get(t, s, 0); r.Code != 200 {
		t.Fatalf("segment 0: %d %s", r.Code, r.Body.String())
	}

	ev := rec.WaitFor(t, events.EventPlaybackTranscodeFallback, time.Second)
	if err := reg.ValidatePayload(ev.Type, ev.Payload); err != nil {
		t.Fatalf("transcode fallback payload invalid: %v", err)
	}
	p, err := events.DecodePayload[events.PlaybackTranscodeFallbackPayload](ev)
	if err != nil {
		t.Fatal(err)
	}
	if p.SessionID != "hw-fb" || p.FromHWAccel != AccelVideoToolbox || p.ToEncoder != "libx264" || p.ActiveTranscodes < 1 {
		t.Fatalf("unexpected transcode fallback payload: %+v", p)
	}
}
