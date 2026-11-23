package transcode

import (
	"context"
	"encoding/xml"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/mediaproc"
)

type mpdTimelineS struct {
	T *int64 `xml:"t,attr"`
	D int64  `xml:"d,attr"`
	R int    `xml:"r,attr"`
}

type mpdSpan struct {
	startMS int64
	durMS   int64
}

func expandSegmentTimeline(t *testing.T, entries []mpdTimelineS) []mpdSpan {
	t.Helper()
	var out []mpdSpan
	var cur int64
	for _, s := range entries {
		if s.T != nil {
			cur = *s.T
		}
		if s.D <= 0 || s.R < 0 {
			t.Fatalf("bad SegmentTimeline entry: %+v", s)
		}
		for k := 0; k <= s.R; k++ {
			out = append(out, mpdSpan{startMS: cur, durMS: s.D})
			cur += s.D
		}
	}
	return out
}

// mpdDoc is the part of an MPD the tests check.
type mpdDoc struct {
	Type     string `xml:"type,attr"`
	Duration string `xml:"mediaPresentationDuration,attr"`
	Period   struct {
		Sets []struct {
			Template struct {
				Timescale   int    `xml:"timescale,attr"`
				Duration    int    `xml:"duration,attr"`
				Init        string `xml:"initialization,attr"`
				Media       string `xml:"media,attr"`
				StartNumber string `xml:"startNumber,attr"`
				Timeline    struct {
					S []mpdTimelineS `xml:"S"`
				} `xml:"SegmentTimeline"`
			} `xml:"SegmentTemplate"`
			Reps []struct {
				ID       string `xml:"id,attr"`
				MimeType string `xml:"mimeType,attr"`
				Codecs   string `xml:"codecs,attr"`
				Width    int    `xml:"width,attr"`
				Height   int    `xml:"height,attr"`
			} `xml:"Representation"`
		} `xml:"AdaptationSet"`
	} `xml:"Period"`
}

func dashManifest(t *testing.T, s core.DASHSession) mpdDoc {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeManifest(rec, httptest.NewRequest(http.MethodGet, "/start.mpd", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/dash+xml" {
		t.Fatalf("MPD: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var doc mpdDoc
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("MPD XML: %v\n%s", err, rec.Body.String())
	}
	return doc
}

func dashGet(t *testing.T, s core.DASHSession, rep, n int) []byte {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if n == InitSegment {
		s.ServeInit(rec, req, rep)
	} else {
		s.ServeMedia(rec, req, rep, n)
	}
	if rec.Code != 200 {
		t.Fatalf("rep %d segment %d: HTTP %d %s", rep, n, rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

// probeDASH probes init+segments and returns the stream and its first
// packet time.
func (e *realEnv) probeDASH(t *testing.T, name string, parts ...[]byte) (probedStream, float64) {
	t.Helper()
	var all []byte
	for _, p := range parts {
		all = append(all, p...)
	}
	path := e.writeTemp(t, name, all)
	ps := e.probeFile(t, path)
	if len(ps) != 1 {
		t.Fatalf("%s: %d streams", name, len(ps))
	}
	out, err := mediaproc.CommandContext(t.Context(), 2*time.Minute, e.ffprobe, "-v", "error", "-show_entries", "packet=pts_time", "-of", "csv=p=0",
		"-read_intervals", "%+#1", path).Output()
	if err != nil {
		t.Fatalf("ffprobe packets %s: %v", name, err)
	}
	first, _ := strconv.ParseFloat(strings.TrimSpace(strings.Split(string(out), "\n")[0]), 64)
	return ps[0], first
}

func startDASH(t *testing.T, m *Manager, req core.HLSRequest) core.DASHSession {
	t.Helper()
	req.Protocol = core.ProtocolDASH
	hs, err := m.StartHLS(context.Background(), req)
	if err != nil {
		t.Fatalf("StartHLS(dash): %v", err)
	}
	ds, ok := hs.(core.DASHSession)
	if !ok {
		t.Fatalf("%T is not a DASHSession", hs)
	}
	return ds
}

func TestIntegrationDASHRemuxSeekAndSwap(t *testing.T) {
	e := newRealEnv(t)
	e.gen(t, "a.mkv", 24, "-c:v", "libx264", "-preset", "ultrafast", "-g", "24", "-c:a", "ac3", "-ac", "2")
	e.gen(t, "b.mkv", 24, "-c:v", "libx264", "-preset", "ultrafast", "-g", "24", "-c:a", "ac3", "-ac", "2")
	ctx := context.Background()
	info, err := e.m.Probe(ctx, e.url("a.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	d := e.m.Decide(*info, plexWeb, allow())
	if d.Kind != core.DecisionRemux || !d.VideoCopy || d.AudioCopy {
		t.Fatalf("decision %+v", d)
	}
	ds := startDASH(t, e.m, core.HLSRequest{SessionID: "dash-remux", InputURL: e.url("a.mkv"), Info: *info, Decision: d})

	doc := dashManifest(t, ds)
	if doc.Type != "static" || len(doc.Period.Sets) != 2 {
		t.Fatalf("MPD %+v", doc)
	}
	v, a := doc.Period.Sets[0], doc.Period.Sets[1]
	if v.Template.Timescale != 1 || v.Template.Duration != 5 || v.Template.StartNumber != "0" ||
		v.Template.Media != "session/dash-remux/$RepresentationID$/$Number$.m4s" ||
		v.Template.Init != "session/dash-remux/$RepresentationID$/header" {
		t.Errorf("template %+v", v.Template)
	}
	if r := v.Reps[0]; r.ID != "0" || !strings.HasPrefix(r.Codecs, "avc1.") || r.Width != 640 || r.Height != 360 {
		t.Errorf("video rep %+v", r)
	}
	if r := a.Reps[0]; r.ID != "1" || r.MimeType != "audio/mp4" || r.Codecs != "mp4a.40.2" {
		t.Errorf("audio rep %+v", r)
	}

	vinit, ainit := dashGet(t, ds, 0, InitSegment), dashGet(t, ds, 1, InitSegment)
	for _, n := range []int{0, 1} {
		st, first := e.probeDASH(t, "v.mp4", vinit, dashGet(t, ds, 0, n))
		if st.CodecName != "h264" || st.Height != 360 {
			t.Errorf("video segment %d: %+v", n, st)
		}
		if math.Abs(first-float64(5*n)) > 0.6 {
			t.Errorf("video segment %d starts at %.3f, want %d", n, first, 5*n)
		}
		st, first = e.probeDASH(t, "a.mp4", ainit, dashGet(t, ds, 1, n))
		if st.CodecName != "aac" {
			t.Errorf("audio segment %d: %+v", n, st)
		}
		if math.Abs(first-float64(5*n)) > 0.6 {
			t.Errorf("audio segment %d starts at %.3f, want %d", n, first, 5*n)
		}
	}
	// Seek far ahead: a new ffmpeg run starts at segment 4 (20s) and the
	// segment carries absolute timestamps.
	st, first := e.probeDASH(t, "v4.mp4", dashGet(t, ds, 0, 4))
	if st.CodecName != "h264" || math.Abs(first-20) > 0.6 {
		t.Errorf("segment 4 after seek: %+v at %.3f", st, first)
	}
	if _, first := e.probeDASH(t, "a4.mp4", dashGet(t, ds, 1, 4)); math.Abs(first-20) > 0.6 {
		t.Errorf("audio segment 4 at %.3f", first)
	}

	// Failover to b.mkv at 10s: segment 2 is produced again from b.
	binfo, err := e.m.Probe(ctx, e.url("b.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ds.SwapInput(ctx, e.url("b.mkv"), *binfo, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	// (A remux starts at the keyframe at or before the seek point.)
	if _, first := e.probeDASH(t, "v2.mp4", dashGet(t, ds, 0, 2)); math.Abs(first-10) > 1.1 {
		t.Errorf("segment 2 after swap at %.3f", first)
	}

	// The whole presentation plays through ffmpeg's own DASH demuxer.
	srv := httptest.NewServer(dashTestHandler(ds, "dash-remux"))
	defer srv.Close()
	e.playMPD(t, srv.URL+"/start.mpd", 24)
	e.m.Stop("dash-remux")
}

func TestIntegrationDASHTranscodeFromOffset(t *testing.T) {
	e := newRealEnv(t, WithoutHWDetection())
	e.gen(t, "t.mkv", 20, "-c:v", "libx264", "-preset", "ultrafast", "-g", "250", "-c:a", "aac", "-ac", "2")
	ctx := context.Background()
	info, err := e.m.Probe(ctx, e.url("t.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	o := allow()
	o.MaxHeight = 240
	d := e.m.Decide(*info, plexWeb, o)
	if d.Kind != core.DecisionTranscode || d.MaxHeight != 240 {
		t.Fatalf("decision %+v", d)
	}
	ds := startDASH(t, e.m, core.HLSRequest{SessionID: "dash-tx", InputURL: e.url("t.mkv"), Info: *info, Decision: d,
		StartOffset: 10 * time.Second})
	doc := dashManifest(t, ds)
	if r := doc.Period.Sets[0].Reps[0]; r.Height != 240 || !strings.HasPrefix(r.Codecs, "avc1.64") {
		t.Errorf("video rep %+v", r)
	}
	vinit := dashGet(t, ds, 0, InitSegment)
	for _, n := range []int{5, 6} {
		st, first := e.probeDASH(t, "tx.mp4", vinit, dashGet(t, ds, 0, n))
		if st.Height != 240 || st.CodecName != "h264" {
			t.Errorf("segment %d: %+v", n, st)
		}
		if math.Abs(first-float64(2*n)) > 0.1 {
			t.Errorf("segment %d starts at %.3f, want %d (forced keyframes)", n, first, 2*n)
		}
	}
	// Segment 0 is before the start offset: a restart produces it.
	if _, first := e.probeDASH(t, "tx0.mp4", dashGet(t, ds, 0, 0)); math.Abs(first) > 0.1 {
		t.Errorf("segment 0 starts at %.3f", first)
	}
}

// A remux of a source whose keyframes are further apart than a segment:
// segments no chunk starts in repeat the GOP that spans them, so every
// segment number plays and the timeline has no holes.
func TestIntegrationDASHLongGOP(t *testing.T) {
	e := newRealEnv(t, WithoutHWDetection())
	e.gen(t, "lg.mkv", 30, "-c:v", "libx264", "-preset", "ultrafast", "-g", "250", "-sc_threshold", "0",
		"-c:a", "aac", "-ac", "2")
	ctx := context.Background()
	info, err := e.m.Probe(ctx, e.url("lg.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	d := e.m.Decide(*info, plexWeb, allow())
	if !d.VideoCopy {
		t.Fatalf("decision %+v", d)
	}
	ds := startDASH(t, e.m, core.HLSRequest{SessionID: "dash-gop", InputURL: e.url("lg.mkv"), Info: *info, Decision: d})
	// Segment 1 (5-10s) lies inside the first 10.4s GOP.
	st, first := e.probeDASH(t, "g1.mp4", dashGet(t, ds, 0, 1))
	if st.CodecName != "h264" || first > 5 {
		t.Errorf("segment 1: %+v starting %.3f", st, first)
	}
	srv := httptest.NewServer(dashTestHandler(ds, "dash-gop"))
	defer srv.Close()
	e.playMPD(t, srv.URL+"/start.mpd", 30)
}

// dashTestHandler serves a DASH session with PMS's URL layout.
func dashTestHandler(ds core.DASHSession, id string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/start.mpd", ds.ServeManifest)
	mux.HandleFunc("/session/"+id+"/{rep}/{name}", func(w http.ResponseWriter, r *http.Request) {
		rep, err := strconv.Atoi(r.PathValue("rep"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		name := r.PathValue("name")
		if name == "header" {
			ds.ServeInit(w, r, rep)
			return
		}
		n, err := strconv.Atoi(strings.TrimSuffix(name, ".m4s"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		ds.ServeMedia(w, r, rep, n)
	})
	return mux
}

// parseMPDDuration parses the "PT1H2M3.5S" durations renderMPD writes.
func parseMPDDuration(t *testing.T, s string) time.Duration {
	t.Helper()
	rest, ok := strings.CutPrefix(s, "PT")
	if !ok {
		t.Fatalf("duration %q", s)
	}
	var d time.Duration
	for _, unit := range []struct {
		sep string
		mul time.Duration
	}{{"H", time.Hour}, {"M", time.Minute}, {"S", time.Second}} {
		v, after, found := strings.Cut(rest, unit.sep)
		if !found {
			continue
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			t.Fatalf("duration %q: %v", s, err)
		}
		d += time.Duration(f * float64(unit.mul))
		rest = after
	}
	return d
}

// playMPD plays a presentation the way a DASH player does: it reads the
// MPD, fetches every representation's init segment and all $Number$
// segments over HTTP, then decodes the result with ffmpeg and checks that
// the timeline covers the title without holes. (Homebrew-style ffmpeg
// builds lack the libxml2 DASH demuxer, so the test is its own client;
// ffmpeg's demuxer is used as well when present.)
func (e *realEnv) playMPD(t *testing.T, mpdURL string, wantSeconds float64) {
	t.Helper()
	resp, err := http.Get(mpdURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var doc mpdDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("MPD: %v", err)
	}
	dur := parseMPDDuration(t, doc.Duration)
	base, _ := url.Parse(mpdURL)
	get := func(ref string) []byte {
		u, err := base.Parse(ref)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.Get(u.String())
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: %d %s", u, resp.StatusCode, b)
		}
		return b
	}
	var inputs []string
	for _, set := range doc.Period.Sets {
		tpl := set.Template
		var count int
		if len(tpl.Timeline.S) > 0 {
			count = len(expandSegmentTimeline(t, tpl.Timeline.S))
		} else {
			segDur := time.Duration(tpl.Duration) * time.Second / time.Duration(tpl.Timescale)
			count = int((dur + segDur - 1) / segDur)
		}
		for _, rep := range set.Reps {
			expand := func(s string, n int) string {
				s = strings.ReplaceAll(s, "$RepresentationID$", rep.ID)
				return strings.ReplaceAll(s, "$Number$", strconv.Itoa(n))
			}
			data := get(expand(tpl.Init, 0))
			for n := 0; n < count; n++ {
				data = append(data, get(expand(tpl.Media, n))...)
			}
			inputs = append(inputs, e.writeTemp(t, "play-"+rep.ID+".mp4", data))
		}
	}
	args := []string{"-hide_banner", "-v", "error"}
	for _, in := range inputs {
		args = append(args, "-i", in)
	}
	for i := range inputs {
		args = append(args, "-map", strconv.Itoa(i))
	}
	if b, err := mediaproc.CommandContext(t.Context(), 2*time.Minute, e.ffmpeg, append(args, "-f", "null", "-")...).CombinedOutput(); err != nil {
		t.Fatalf("decode fetched presentation: %v\n%s", err, b)
	}
	for _, in := range inputs {
		out, err := mediaproc.CommandContext(t.Context(), 2*time.Minute, e.ffprobe, "-v", "error", "-show_entries", "packet=pts_time,duration_time",
			"-of", "csv=p=0", in).Output()
		if err != nil {
			t.Fatalf("ffprobe %s: %v", in, err)
		}
		// The union of the packets' time ranges must cover the title
		// without holes (overlaps from restarted runs are fine).
		type span struct{ a, b float64 }
		var spans []span
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			// Windows ffprobe ends lines with CRLF; a trailing \r would
			// make the duration unparseable and drop every packet but
			// the last.
			f := strings.Split(strings.TrimSpace(line), ",")
			if len(f) < 2 {
				continue
			}
			a, err1 := strconv.ParseFloat(f[0], 64)
			d, err2 := strconv.ParseFloat(f[1], 64)
			if err1 != nil || err2 != nil {
				continue
			}
			spans = append(spans, span{a, a + d})
			lo, hi = math.Min(lo, a), math.Max(hi, a+d)
		}
		sort.Slice(spans, func(i, j int) bool { return spans[i].a < spans[j].a })
		reach := lo
		for _, sp := range spans {
			if sp.a > reach+0.25 {
				t.Errorf("%s: hole in the timeline from %.3f to %.3f", in, reach, sp.a)
			}
			reach = math.Max(reach, sp.b)
		}
		if lo > 0.1 || math.Abs(hi-wantSeconds) > 1 {
			t.Errorf("%s covers %.3f-%.3f, want 0-%.0f", in, lo, hi, wantSeconds)
		}
	}
	if b, _ := mediaproc.CommandContext(t.Context(), 2*time.Minute, e.ffmpeg, "-hide_banner", "-demuxers").Output(); strings.Contains(string(b), " dash ") {
		if b, err := mediaproc.CommandContext(t.Context(), 2*time.Minute, e.ffmpeg, "-hide_banner", "-v", "error", "-i", mpdURL, "-map", "0", "-f", "null", "-").CombinedOutput(); err != nil {
			t.Errorf("ffmpeg DASH demuxer: %v\n%s", err, b)
		}
	}
}

// Reproduces the 23.976 fps / 2.002 s GOP (48-frame IDR) copy DASH stream
// with B-frame reorder and transcoded audio: verifies the MPD publishes a
// stable SegmentTimeline matching every served video/audio segment's real
// timing (both sequentially and after a seek), while encoded DASH stays on
// a fixed SegmentTemplate duration.
func TestIntegrationDASHCopySegmentTimeline2002GOP(t *testing.T) {
	e := newRealEnv(t, WithoutHWDetection())
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	srcPath := e.dir + "/gop2002.mkv"
	cmd := mediaproc.CommandContext(ctx, 2*time.Minute, e.ffmpeg,
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24000/1001",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-c:v", "libx264", "-preset", "ultrafast",
		"-x264-params", "bframes=3:b-pyramid=normal:keyint=48:min-keyint=48:scenecut=0",
		"-c:a", "eac3", "-ac", "6", "-b:a", "384k",
		"-max_interleave_delta", "0", "-t", "26", srcPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot generate 2.002s GOP fixture: %v: %s", err, out)
	}
	info, err := e.m.Probe(ctx, e.url("gop2002.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	info.Keyframes = e.keyframeIndex(t, "gop2002.mkv")
	if len(info.Keyframes) < 12 {
		t.Fatalf("expected >=12 keyframes for 26s @ 2.002s GOP, got %d", len(info.Keyframes))
	}

	d := core.Decision{Kind: core.DecisionRemux, Container: ContainerFMP4, VideoCopy: true, AudioCopy: false, AudioCodec: "aac", AudioChannels: 2, AudioStream: 1}
	ds := startDASH(t, e.m, core.HLSRequest{
		SessionID: "dash-kf2002", InputURL: e.url("gop2002.mkv"),
		Info: *info, Decision: d, PMSDASH: true,
	})
	defer e.m.Stop("dash-kf2002")

	rec1 := httptest.NewRecorder()
	ds.ServeManifest(rec1, httptest.NewRequest(http.MethodGet, "/start.mpd", nil))
	if rec1.Code != 200 {
		t.Fatalf("MPD: %d %s", rec1.Code, rec1.Body.String())
	}
	var doc mpdDoc
	if err := xml.Unmarshal(rec1.Body.Bytes(), &doc); err != nil {
		t.Fatalf("MPD XML: %v\n%s", err, rec1.Body.String())
	}
	if len(doc.Period.Sets) != 2 {
		t.Fatalf("sets = %d, want 2", len(doc.Period.Sets))
	}
	vSpans := expandSegmentTimeline(t, doc.Period.Sets[0].Template.Timeline.S)
	aSpans := expandSegmentTimeline(t, doc.Period.Sets[1].Template.Timeline.S)
	if len(vSpans) == 0 || len(vSpans) != len(aSpans) {
		t.Fatalf("timeline spans video=%d audio=%d", len(vSpans), len(aSpans))
	}
	if doc.Period.Sets[0].Template.Duration != 0 || doc.Period.Sets[0].Template.Timescale != 1000 {
		t.Fatalf("unexpected template attrs: %+v", doc.Period.Sets[0].Template)
	}
	// Every full segment is 3 × 2.002 s = 6.006 s (6006 ms).
	for n := 0; n < len(vSpans)-1; n++ {
		if math.Abs(float64(vSpans[n].durMS)-6006) > 5 {
			t.Errorf("seg %d MPD dur = %d ms, want ~6006", n, vSpans[n].durMS)
		}
	}

	for n, sp := range vSpans {
		segBytes := dashGet(t, ds, 0, n)
		first, end := e.videoSpan(t, ContainerFMP4, nil, segBytes, "dash-kf2002-v-"+strconv.Itoa(n))
		wantStart := float64(sp.startMS) / 1000
		wantEnd := float64(sp.startMS+sp.durMS) / 1000
		if math.Abs(first-wantStart) > 0.1 || math.Abs(end-wantEnd) > 0.15 {
			t.Errorf("video seg %d spans %.3f-%.3f, MPD SegmentTimeline says %.3f-%.3f", n, first, end, wantStart, wantEnd)
		}
	}

	// Seeking directly to segment 3 (18.018s) in a fresh session must start on
	// that exact boundary without pulling in a preceding GOP.
	seekDS := startDASH(t, e.m, core.HLSRequest{
		SessionID: "dash-kf2002-seek", InputURL: e.url("gop2002.mkv"),
		Info: *info, Decision: d, PMSDASH: true, StartOffset: 18500 * time.Millisecond,
	})
	defer e.m.Stop("dash-kf2002-seek")
	seekSeg := dashGet(t, seekDS, 0, 3)
	sFirst, sEnd := e.videoSpan(t, ContainerFMP4, nil, seekSeg, "dash-kf2002-seek-3")
	wantStart := float64(vSpans[3].startMS) / 1000
	wantEnd := float64(vSpans[3].startMS+vSpans[3].durMS) / 1000
	if math.Abs(sFirst-wantStart) > 0.1 || math.Abs(sEnd-wantEnd) > 0.15 {
		t.Errorf("sought video seg 3 spans %.3f-%.3f, MPD says %.3f-%.3f", sFirst, sEnd, wantStart, wantEnd)
	}

	// MPD stays byte-identical across repeated requests.
	rec2 := httptest.NewRecorder()
	ds.ServeManifest(rec2, httptest.NewRequest(http.MethodGet, "/start.mpd", nil))
	if rec1.Body.String() != rec2.Body.String() {
		t.Fatal("MPD changed across requests within the same session")
	}

	// Full presentation decodes cleanly end to end over HTTP.
	srv := httptest.NewServer(dashTestHandler(ds, "dash-kf2002"))
	defer srv.Close()
	e.playMPD(t, srv.URL+"/start.mpd", 26)

	// Encoded DASH on the same keyframe-indexed source keeps fixed duration.
	encDec := core.Decision{Kind: core.DecisionTranscode, Container: ContainerFMP4, VideoCopy: false, VideoCodec: "h264", MaxHeight: 180, AudioCopy: false, AudioCodec: "aac", AudioChannels: 2, AudioStream: 1}
	encDS := startDASH(t, e.m, core.HLSRequest{
		SessionID: "dash-kf2002-enc", InputURL: e.url("gop2002.mkv"),
		Info: *info, Decision: encDec, PMSDASH: true,
	})
	defer e.m.Stop("dash-kf2002-enc")
	encDoc := dashManifest(t, encDS)
	if len(encDoc.Period.Sets[0].Template.Timeline.S) != 0 || encDoc.Period.Sets[0].Template.Duration != 5 {
		t.Fatalf("encoded DASH should keep fixed duration=5, got %+v", encDoc.Period.Sets[0].Template)
	}
}
