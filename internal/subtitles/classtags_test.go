package subtitles

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenFormatReRendersDirtyCachedFile is the cached half of #616: a
// sidecar normalised before cue text was cleaned still holds WebVTT
// <c.…> spans on disk. Serving it must re-render from its cues, with
// no migration of the stored file; a clean file serves byte for byte.
func TestOpenFormatReRendersDirtyCachedFile(t *testing.T) {
	s := New(Options{CacheDir: t.TempDir()})
	id := "netflix-en"
	dirty := "1\n00:00:01,000 --> 00:00:04,000\n<c.white><c.mono_sans><i>Line</i> one</c.mono_sans></c.white>\n\n" +
		"2\n00:00:05,000 --> 00:00:07,000\n<v Ann>Two</v>\n"
	if err := os.WriteFile(filepath.Join(s.dir, id+".srt"), []byte(dirty), 0o644); err != nil {
		t.Fatal(err)
	}
	dirtyVTT := "WEBVTT\n\n00:00:01.000 --> 00:00:04.000\n<c.white><i>Line</i> one</c.white>\n"
	if err := os.WriteFile(filepath.Join(s.dir, id+".vtt"), []byte(dirtyVTT), 0o644); err != nil {
		t.Fatal(err)
	}
	read := func(format string) string {
		r, _, err := s.OpenFormat(id, format)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		b, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := read("srt"); strings.Contains(got, "<c.") || strings.Contains(got, "<v ") || !strings.Contains(got, "<i>Line</i> one") || !strings.Contains(got, "Two") {
		t.Fatalf("dirty SRT served uncleaned:\n%s", got)
	}
	if got := read("vtt"); strings.Contains(got, "<c.") || !strings.Contains(got, "<i>Line</i> one") {
		t.Fatalf("dirty VTT served uncleaned:\n%s", got)
	}
	// The stored files are left as written; only the served bytes change.
	stored, err := os.ReadFile(filepath.Join(s.dir, id+".srt"))
	if err != nil || string(stored) != dirty {
		t.Fatalf("stored SRT rewritten: %q %v", stored, err)
	}

	cleanID := "clean-en"
	clean := "1\r\n00:00:01,000 --> 00:00:04,000\r\n<i>Line</i> one\r\n\r\n"
	if err := os.WriteFile(filepath.Join(s.dir, cleanID+".srt"), []byte(clean), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _, err := s.OpenFormat(cleanID, "srt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != clean {
		t.Fatalf("clean SRT not served as stored:\n%q\nwant:\n%q", got, clean)
	}
}

// TestNormalizeLangStripsWebVTTClasses covers the downloaded/uploaded
// path end to end: both rendered forms of a converted Netflix track
// are free of class spans (#616).
func TestNormalizeLangStripsWebVTTClasses(t *testing.T) {
	raw := []byte("WEBVTT\n\n00:00:01.000 --> 00:00:04.000\n<c.white>Hi <b>there</b></c.white>\n")
	srt, vtt, err := NormalizeLang(raw, "en")
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{"srt": srt, "vtt": vtt} {
		if strings.Contains(out, "<c.") || !strings.Contains(out, "Hi <b>there</b>") {
			t.Errorf("%s = %q", name, out)
		}
	}
}

// TestParseASSCleansCueText: an ASS body carrying HTML/WebVTT spans
// (from a converted track) enters the cue model cleaned, like SRT/VTT.
func TestParseASSCleansCueText(t *testing.T) {
	ass := "[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:01.00,0:00:04.00,Default,,0,0,0,,{\\an8}<c.white><i>Hi</i></c.white>\\Ntwo\n"
	cues, err := ParseASS(ass)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 1 || cues[0].Text != "<i>Hi</i>\ntwo" {
		t.Fatalf("cues = %+v", cues)
	}
}
