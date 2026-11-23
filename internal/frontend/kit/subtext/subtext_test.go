package subtext

import (
	"strings"
	"testing"
	"time"
)

func TestAcceptsUploadFormat(t *testing.T) {
	for _, f := range []string{"srt", "vtt", "ass", "ssa", "smi", "txt", "psb"} {
		if !AcceptsUploadFormat(f) {
			t.Errorf("AcceptsUploadFormat(%q) = false, want true", f)
		}
	}
	for _, f := range []string{"", "SRT", ".srt", "sub", "webvtt"} {
		if AcceptsUploadFormat(f) {
			t.Errorf("AcceptsUploadFormat(%q) = true, want false", f)
		}
	}
}

func TestParseAndRenderCues(t *testing.T) {
	srt := "1\r\n00:00:01,000 --> 00:00:02,500\r\n<i>Hello</i> &amp; welcome\nLine two\r\n \r\n2\r\n00:00:03,000 --> 00:00:04,000\r\nWorld\r\n"
	cues, err := ParseCues(srt)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 2 || cues[0].Start != time.Second || cues[0].End != 2500*time.Millisecond {
		t.Fatalf("unexpected cues: %+v", cues)
	}

	vtt := RenderVTT(cues)
	if !strings.HasPrefix(vtt, "WEBVTT\n\n") || !strings.Contains(vtt, "<i>Hello</i> &amp; welcome") {
		t.Fatalf("unexpected VTT:\n%s", vtt)
	}
	roundTrip, err := ParseCues(vtt)
	if err != nil || len(roundTrip) != 2 {
		t.Fatalf("ParseCues(vtt) = %v, %v", roundTrip, err)
	}

	ass := RenderASS(cues)
	if !strings.HasPrefix(ass, ASSHeader) || !strings.Contains(ass, `Dialogue: 0,0:00:01.00,0:00:02.50,Default,,0,0,0,,{\i1}Hello{\i0} & welcome\NLine two`) {
		t.Fatalf("unexpected ASS:\n%s", ass)
	}

	renderedSRT := RenderSRT(cues)
	if !strings.Contains(renderedSRT, "1\n00:00:01,000 --> 00:00:02,500\n") {
		t.Fatalf("unexpected SRT:\n%s", renderedSRT)
	}
}

// TestWebVTTSpansStrippedFromSRT is #616: an SRT converted from Netflix
// WebVTT captions carries <c.…> class spans that SRT players show
// literally. Parsing must fold cue text to the model's markup, and
// every renderer must stay within its own format's set.
func TestWebVTTSpansStrippedFromSRT(t *testing.T) {
	srt := "1\n00:00:01,000 --> 00:00:04,000\n" +
		"<c.white><c.mono_sans><i>Hello</i> <font color=\"#ff0000\">red</font></c.mono_sans></c.white>\n\n" +
		"2\n00:00:05,000 --> 00:00:07,000\n" +
		"<v Roger>Spoken</v> <lang en>word</lang> <00:00:06.000>later\n\n" +
		"3\n00:00:08,000 --> 00:00:09,000\n" +
		"<ruby>漢<rp>(</rp><rt>kan</rt><rp>)</rp></ruby>字 <font face=\"x\">plain</font>\n\n" +
		"4\n00:00:10,000 --> 00:00:11,000\n<c.white></c.white>\n"
	cues, err := ParseCues(srt)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`<i>Hello</i> <font color="#ff0000">red</font>`,
		"Spoken word later",
		"漢字 plain",
	}
	if len(cues) != len(want) {
		t.Fatalf("cues = %+v, want %d (markup-only cue dropped)", cues, len(want))
	}
	for i, w := range want {
		if cues[i].Text != w {
			t.Errorf("cue %d = %q, want %q", i, cues[i].Text, w)
		}
	}

	rendered := RenderSRT(cues)
	for _, bad := range []string{"<c.", "</c", "<v ", "<lang", "<ruby", "<rt>", "kan", "00:00:06.000>"} {
		if strings.Contains(rendered, bad) {
			t.Errorf("SRT still contains %q:\n%s", bad, rendered)
		}
	}
	if !strings.Contains(rendered, `<i>Hello</i> <font color="#ff0000">red</font>`) {
		t.Fatalf("SRT lost its own markup:\n%s", rendered)
	}

	vtt := RenderVTT(cues)
	if strings.Contains(vtt, "font") || strings.Contains(vtt, "<c.") || !strings.Contains(vtt, "<i>Hello</i>") {
		t.Fatalf("unexpected VTT:\n%s", vtt)
	}
	ass := RenderASS(cues)
	if strings.Contains(ass, "<") || !strings.Contains(ass, `{\i1}Hello{\i0}`) {
		t.Fatalf("unexpected ASS:\n%s", ass)
	}

	// Cues built without the parser (embedded extraction, cached cue
	// windows) are cleaned by the renderers themselves.
	dirty := []Cue{{Start: time.Second, End: 2 * time.Second, Text: "<c.white><b>Hey</b></c.white>"}}
	if got := RenderSRT(dirty); strings.Contains(got, "<c") || !strings.Contains(got, "<b>Hey</b>") {
		t.Fatalf("RenderSRT(dirty cue) = %q", got)
	}
	if got := RenderVTT(dirty); strings.Contains(got, "<c") {
		t.Fatalf("RenderVTT(dirty cue) = %q", got)
	}
	if got := RenderASS(dirty); strings.Contains(got, "<") {
		t.Fatalf("RenderASS(dirty cue) = %q", got)
	}
}

func TestHasForeignTags(t *testing.T) {
	cases := []struct {
		text, format string
		want         bool
	}{
		{"<c.white>Hi</c.white>", "srt", true},
		{"<c.white>Hi</c.white>", "vtt", true},
		{"<v Ann>Hi</v>", "srt", true},
		{"a <00:00:01.000>b", "vtt", true},
		{`<i>Hi</i> <font color="red">r</font>`, "srt", false},
		{`<i>Hi</i> <font color="red">r</font>`, "vtt", true},
		{`<font face="x">r</font>`, "srt", true},
		{"one<br>two", "srt", true},
		{"<i>Hi</i> plain &amp; text", "vtt", false},
		{"no tags at all", "srt", false},
	}
	for _, tc := range cases {
		if got := HasForeignTags(tc.text, tc.format); got != tc.want {
			t.Errorf("HasForeignTags(%q, %q) = %v, want %v", tc.text, tc.format, got, tc.want)
		}
	}
}
