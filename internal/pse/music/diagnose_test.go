package music

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFormatOf(t *testing.T) {
	for mime, want := range map[string]string{
		"audio/flac": "FLAC", "audio/x-flac": "FLAC", "audio/mpeg": "MP3",
		"audio/mp4": "AAC/M4A", "audio/ogg; codecs=opus": "Opus/Vorbis", "text/html": "",
	} {
		if got, _ := formatOf(mime); got != want {
			t.Errorf("formatOf(%q) = %q, want %q", mime, got, want)
		}
	}
	if _, lossless := formatOf("audio/mpeg"); lossless {
		t.Error("MP3 is not lossless")
	}
}

func TestAddonNameHidesToken(t *testing.T) {
	if got := addonName("https://eclipse.example/SECRET/manifest.json?key=x"); got != "eclipse.example" {
		t.Fatalf("addonName = %q", got)
	}
}

func TestProbeReadsRealFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "" {
			t.Error("probe must only ask for the first bytes")
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte{0xff, 0xfb, 0x90, 0x00})
	}))
	defer srv.Close()
	var rep SourceReport
	// Annoncé FLAC, servi MP3 : le rapport doit dire MP3.
	if err := probe(context.Background(), srv.Client(), Stream{URL: srv.URL, Mime: "audio/flac", Quality: "LOSSLESS"}, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Format != "MP3" || rep.Lossless {
		t.Fatalf("report = %+v", rep)
	}
}

func TestDiagnoseWithoutSources(t *testing.T) {
	d, err := Diagnose(context.Background(), Settings{}, nil, "", nil)
	if err != nil || len(d.Sources) != 0 {
		t.Fatalf("got %+v, %v", d, err)
	}
}

func TestProbeRejectsFalseLossless(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/flac")
		_, _ = w.Write([]byte(`<html>Access denied</html>`))
	}))
	defer srv.Close()
	var rep SourceReport
	if err := probe(context.Background(), srv.Client(), Stream{URL: srv.URL, Mime: "audio/flac", Quality: "HI_RES_LOSSLESS"}, &rep); err == nil || rep.Lossless || rep.HiRes {
		t.Fatalf("accepted non-audio: %+v %v", rep, err)
	}
}
