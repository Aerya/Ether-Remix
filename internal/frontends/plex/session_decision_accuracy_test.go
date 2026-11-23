package plex

import (
	"encoding/xml"
	"net/http"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type sessionsWireDoc struct {
	Size   int                `xml:"size,attr"`
	Videos []sessionsWireItem `xml:"Video"`
}

type sessionsWireItem struct {
	SessionKey       string                 `xml:"sessionKey,attr"`
	Media            []sessionsWireMedia    `xml:"Media"`
	Session          *sessionsWireSession   `xml:"Session"`
	TranscodeSession *sessionsWireTranscode `xml:"TranscodeSession"`
}

type sessionsWireMedia struct {
	VideoCodec      string             `xml:"videoCodec,attr"`
	AudioCodec      string             `xml:"audioCodec,attr"`
	AudioChannels   int                `xml:"audioChannels,attr"`
	VideoResolution string             `xml:"videoResolution,attr"`
	Width           int                `xml:"width,attr"`
	Height          int                `xml:"height,attr"`
	Bitrate         int                `xml:"bitrate,attr"`
	Container       string             `xml:"container,attr"`
	Protocol        string             `xml:"protocol,attr"`
	Selected        string             `xml:"selected,attr"`
	Parts           []sessionsWirePart `xml:"Part"`
}

type sessionsWirePart struct {
	Decision  string               `xml:"decision,attr"`
	Selected  string               `xml:"selected,attr"`
	Width     int                  `xml:"width,attr"`
	Height    int                  `xml:"height,attr"`
	Bitrate   int                  `xml:"bitrate,attr"`
	Container string               `xml:"container,attr"`
	Protocol  string               `xml:"protocol,attr"`
	Streams   []sessionsWireStream `xml:"Stream"`
}

type sessionsWireStream struct {
	StreamType int    `xml:"streamType,attr"`
	Codec      string `xml:"codec,attr"`
	Decision   string `xml:"decision,attr"`
	Width      int    `xml:"width,attr"`
	Height     int    `xml:"height,attr"`
	Bitrate    int    `xml:"bitrate,attr"`
	Channels   int    `xml:"channels,attr"`
	Location   string `xml:"location,attr"`
}

type sessionsWireSession struct {
	ID        string `xml:"id,attr"`
	Bandwidth int    `xml:"bandwidth,attr"`
	Location  string `xml:"location,attr"`
}

type sessionsWireTranscode struct {
	Key                     string  `xml:"key,attr"`
	VideoDecision           string  `xml:"videoDecision,attr"`
	AudioDecision           string  `xml:"audioDecision,attr"`
	SourceVideoCodec        string  `xml:"sourceVideoCodec,attr"`
	SourceAudioCodec        string  `xml:"sourceAudioCodec,attr"`
	VideoCodec              string  `xml:"videoCodec,attr"`
	AudioCodec              string  `xml:"audioCodec,attr"`
	AudioChannels           int     `xml:"audioChannels,attr"`
	Width                   int     `xml:"width,attr"`
	Height                  int     `xml:"height,attr"`
	Protocol                string  `xml:"protocol,attr"`
	Container               string  `xml:"container,attr"`
	Speed                   float64 `xml:"speed,attr"`
	TranscodeHwRequested    string  `xml:"transcodeHwRequested,attr"`
	TranscodeHwFullPipeline string  `xml:"transcodeHwFullPipeline,attr"`
}

func parseSessionsXML(t *testing.T, body string) sessionsWireDoc {
	t.Helper()
	var doc sessionsWireDoc
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("unmarshal /status/sessions XML: %v\n%s", err, body)
	}
	return doc
}

func findWireStream(streams []sessionsWireStream, streamType int) *sessionsWireStream {
	for i := range streams {
		if streams[i].StreamType == streamType {
			return &streams[i]
		}
	}
	return nil
}

// Reproduces #622: Plex Web starts a 1080p HEVC movie at Original (DASH fMP4
// video copy + audio transcode), then switches quality mid-stream to 2 Mbps
// with a fresh transcode session ID. /status/sessions must immediately re-bind
// to the active transcode session and report output dimensions, codecs,
// decisions, HW pipeline flags, and output bandwidth — never the closed copy
// session.
func TestStatusSessionsQualityChangeRebindsActiveTranscode(t *testing.T) {
	f, dt := dashFixture()
	defer f.srv.Close()
	f.srv.setTranscoder(&qualityTranscoder{dt})

	hevcInfo := core.MediaInfo{
		Container:   "mkv",
		Duration:    sampleInfo.Duration,
		Size:        sampleInfo.Size,
		BitrateKbps: 9045,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Profile: "main 10", Width: 1920, Height: 1080, FrameRate: 23.976, BitDepth: 10, BitrateKbps: 8405},
			{Index: 1, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, ChannelLayout: "5.1(side)", SampleRate: 48000, BitrateKbps: 640, Default: true},
		},
	}
	f.playback.info = hevcInfo

	headers := []string{
		"X-Plex-Client-Identifier", "chrome-web-1",
		"X-Plex-Session-Identifier", "play-web-1",
		"X-Plex-Product", "Plex Web",
		"X-Plex-Platform", "Chrome",
		"X-Plex-Device", "Windows",
		"X-Plex-Device-Name", "Chrome",
		"X-Plex-Client-Profile-Extra", "add-transcode-target(type=videoProfile&context=streaming&protocol=dash&container=mp4&videoCodec=hevc,h264&audioCodec=aac)",
	}

	// 1. Start at Original: video copy (HEVC 1080p) + audio transcode (EAC3 -> AAC).
	origQuery := "path=%2Flibrary%2Fmetadata%2F1000000000000550&protocol=dash&directPlay=0&directStream=1&location=lan&session=sess-copy"
	if rec := f.get(t, "/video/:/transcode/universal/decision?"+origQuery, headers...); rec.Code != http.StatusOK {
		t.Fatalf("orig decision: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.get(t, "/video/:/transcode/universal/start.mpd?"+origQuery, headers...); rec.Code != http.StatusOK {
		t.Fatalf("orig start.mpd: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.get(t, "/:/timeline?ratingKey=1000000000000550&key=%2Flibrary%2Fmetadata%2F1000000000000550&state=playing&time=10000&duration=8340000", headers...); rec.Code != http.StatusOK {
		t.Fatalf("orig timeline: %d %s", rec.Code, rec.Body.String())
	}

	doc1 := parseSessionsXML(t, f.get(t, "/status/sessions").Body.String())
	if len(doc1.Videos) != 1 || doc1.Videos[0].TranscodeSession == nil || len(doc1.Videos[0].Media) != 1 {
		t.Fatalf("initial /status/sessions malformed: %+v", doc1)
	}
	v1 := doc1.Videos[0]
	if v1.TranscodeSession.Key != "/transcode/sessions/sess-copy" ||
		v1.TranscodeSession.VideoDecision != "copy" ||
		v1.TranscodeSession.AudioDecision != "transcode" ||
		v1.TranscodeSession.SourceVideoCodec != "hevc" ||
		v1.TranscodeSession.SourceAudioCodec != "eac3" ||
		v1.TranscodeSession.VideoCodec != "hevc" ||
		v1.TranscodeSession.AudioCodec != "aac" ||
		v1.TranscodeSession.AudioChannels != 2 ||
		v1.TranscodeSession.Width != 1920 ||
		v1.TranscodeSession.Height != 1080 ||
		v1.TranscodeSession.Protocol != "dash" ||
		v1.TranscodeSession.Container != "mp4" ||
		v1.TranscodeSession.TranscodeHwRequested != "0" ||
		v1.TranscodeSession.TranscodeHwFullPipeline != "" {
		t.Fatalf("initial TranscodeSession = %+v", v1.TranscodeSession)
	}
	if v1.Media[0].VideoResolution != "1080p" || v1.Media[0].Width != 1920 || v1.Media[0].Height != 1080 || v1.Media[0].Bitrate != 9045 || v1.Media[0].AudioChannels != 2 || v1.Media[0].AudioCodec != "aac" {
		t.Fatalf("initial Media = %+v", v1.Media[0])
	}
	if v1.Media[0].Parts[0].Decision != "transcode" {
		t.Fatalf("initial Part decision = %q, want transcode", v1.Media[0].Parts[0].Decision)
	}
	if v1.Session == nil || v1.Session.Bandwidth != 9497 {
		t.Fatalf("initial Session = %+v, want bandwidth 9497", v1.Session)
	}

	// 2. Quality change mid-stream to 2 Mbps 720p with a new transcode session ID.
	lowQuery := "path=%2Flibrary%2Fmetadata%2F1000000000000550&protocol=dash&directPlay=0&directStream=0&location=lan&maxVideoBitrate=2000&videoResolution=1280x720&offset=23&session=sess-2mbps"
	if rec := f.get(t, "/video/:/transcode/universal/decision?"+lowQuery, headers...); rec.Code != http.StatusOK {
		t.Fatalf("low decision: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.get(t, "/video/:/transcode/universal/start.mpd?"+lowQuery, headers...); rec.Code != http.StatusOK {
		t.Fatalf("low start.mpd: %d %s", rec.Code, rec.Body.String())
	}
	ds := dt.session("sess-2mbps")
	if ds == nil {
		t.Fatal("missing active DASH session sess-2mbps")
	}
	ds.encoder = &core.EncoderStatus{
		Decision:  ds.req.Decision,
		Processor: "gpu",
		Filters:   "gpu",
		Phase:     "running",
		Speed:     3.4,
	}

	// Check /status/sessions BEFORE any new /:/timeline arrives, and AGAIN after
	// a subsequent /:/timeline poll: both must report sess-2mbps, never sess-copy.
	checkTranscoding := func(stage string) {
		t.Helper()
		raw := f.get(t, "/status/sessions").Body.String()
		doc := parseSessionsXML(t, raw)
		if len(doc.Videos) != 1 || doc.Videos[0].TranscodeSession == nil || len(doc.Videos[0].Media) != 1 {
			t.Fatalf("%s: /status/sessions malformed: %s", stage, raw)
		}
		v := doc.Videos[0]
		ts := v.TranscodeSession
		if ts.Key != "/transcode/sessions/sess-2mbps" {
			t.Fatalf("%s: TranscodeSession key = %q, want /transcode/sessions/sess-2mbps", stage, ts.Key)
		}
		if ts.VideoDecision != "transcode" || ts.AudioDecision != "transcode" {
			t.Fatalf("%s: decisions = video:%q audio:%q, want transcode/transcode", stage, ts.VideoDecision, ts.AudioDecision)
		}
		if ts.SourceVideoCodec != "hevc" || ts.SourceAudioCodec != "eac3" || ts.VideoCodec != "h264" || ts.AudioCodec != "aac" || ts.AudioChannels != 2 {
			t.Fatalf("%s: codecs/channels = %+v", stage, ts)
		}
		if ts.Width != 852 || ts.Height != 480 || ts.Protocol != "dash" || ts.Container != "mp4" {
			t.Fatalf("%s: output geometry/transport = %+v", stage, ts)
		}
		if ts.TranscodeHwRequested != "1" || ts.TranscodeHwFullPipeline != "1" || ts.Speed != 3.4 {
			t.Fatalf("%s: hw/speed = %+v", stage, ts)
		}
		m := v.Media[0]
		if m.VideoResolution != "480p" || m.Width != 852 || m.Height != 480 || m.Bitrate != 2000 || m.VideoCodec != "h264" || m.AudioCodec != "aac" || m.AudioChannels != 2 {
			t.Fatalf("%s: Media = %+v", stage, m)
		}
		p := m.Parts[0]
		if p.Decision != "transcode" || p.Width != 852 || p.Height != 480 || p.Bitrate != m.Bitrate {
			t.Fatalf("%s: Part = %+v", stage, p)
		}
		vs := findWireStream(p.Streams, 1)
		as := findWireStream(p.Streams, 2)
		if vs == nil || vs.Decision != "transcode" || vs.Codec != "h264" || vs.Width != 852 || vs.Height != 480 || vs.Bitrate != 1808 {
			t.Fatalf("%s: video stream = %+v", stage, vs)
		}
		if as == nil || as.Decision != "transcode" || as.Codec != "aac" || as.Channels != 2 || as.Bitrate != 192 {
			t.Fatalf("%s: audio stream = %+v", stage, as)
		}
		if v.Session == nil || v.Session.Bandwidth != 2100 {
			t.Fatalf("%s: Session bandwidth = %+v, want 2100", stage, v.Session)
		}
	}

	checkTranscoding("immediately-after-start")

	if rec := f.get(t, "/:/timeline?ratingKey=1000000000000550&key=%2Flibrary%2Fmetadata%2F1000000000000550&state=playing&time=25000&duration=8340000", headers...); rec.Code != http.StatusOK {
		t.Fatalf("post-change timeline: %d %s", rec.Code, rec.Body.String())
	}
	checkTranscoding("after-timeline")

	// /transcode/sessions must report only the active session.
	tsList := f.get(t, "/transcode/sessions").Body.String()
	if !strings.Contains(tsList, "/transcode/sessions/sess-2mbps") || strings.Contains(tsList, "/transcode/sessions/sess-copy") {
		t.Fatalf("/transcode/sessions leaked closed session: %s", tsList)
	}

	// A late /universal/stop for the superseded copy session succeeds cleanly
	// without disturbing the active transcode session.
	if rec := f.get(t, "/video/:/transcode/universal/stop?session=sess-copy", headers...); rec.Code != http.StatusOK {
		t.Fatalf("late stop of superseded session: %d %s", rec.Code, rec.Body.String())
	}
	checkTranscoding("after-late-stop-of-old-session")
}

// Verifies that /status/sessions tracks live encoder changes on the SAME
// transcode session ID: both ABR rung step-downs AND base-rung failover /
// adaptive copy-to-encode transitions.
func TestStatusSessionsABRRungAndBaseFailover(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	f.playback.info = core.MediaInfo{
		Container:   "mkv",
		Duration:    sampleInfo.Duration,
		Size:        sampleInfo.Size,
		BitrateKbps: 9000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 1920, Height: 1080, FrameRate: 23.976, BitDepth: 10, BitrateKbps: 8360},
			{Index: 1, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, SampleRate: 48000, BitrateKbps: 640, Default: true},
		},
	}
	f.trans.decision = core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: false, AudioCodec: "aac", Container: "mpegts", AudioStream: 1}
	f.trans.rungs = []core.HLSRung{{ID: "base", Bandwidth: 9_000_000}, {ID: "720p4m", Bandwidth: 4_800_000}}

	headers := []string{
		"X-Plex-Client-Identifier", "atv-1",
		"X-Plex-Session-Identifier", "play-atv-1",
		"X-Plex-Product", "Plex for Apple TV",
		"X-Plex-Platform", "tvOS",
	}
	start := "/video/:/transcode/universal/start.m3u8?path=%2Flibrary%2Fmetadata%2F1000000000000550&protocol=hls&directPlay=0&directStream=1&session=hls-live"
	if rec := f.get(t, decisionPath(start), headers...); rec.Code != http.StatusOK {
		t.Fatalf("decision: %d", rec.Code)
	}
	if rec := f.get(t, start, headers...); rec.Code != http.StatusOK {
		t.Fatalf("start: %d", rec.Code)
	}
	if rec := f.get(t, "/:/timeline?ratingKey=1000000000000550&key=%2Flibrary%2Fmetadata%2F1000000000000550&state=playing&time=5000&duration=8340000", headers...); rec.Code != http.StatusOK {
		t.Fatalf("timeline: %d", rec.Code)
	}

	hs := f.trans.sessions["hls-live"]
	if hs == nil {
		t.Fatal("missing HLS session")
	}

	// Step down to an encoded ABR rung with partial HW pipeline (GPU encode, CPU filters).
	hs.encoder = &core.EncoderStatus{
		Rung:      "720p4m",
		Processor: "gpu",
		Filters:   "cpu",
		Decision: core.Decision{
			Kind:        core.DecisionTranscode,
			VideoCopy:   false,
			VideoCodec:  "h264",
			AudioCopy:   false,
			AudioCodec:  "aac",
			Container:   "mpegts",
			MaxHeight:   720,
			BitrateKbps: 4000,
			AudioStream: 1,
		},
	}
	docRung := parseSessionsXML(t, f.get(t, "/status/sessions").Body.String())
	vRung := docRung.Videos[0]
	if vRung.TranscodeSession == nil ||
		vRung.TranscodeSession.VideoDecision != "transcode" ||
		vRung.TranscodeSession.VideoCodec != "h264" ||
		vRung.TranscodeSession.Width != 1280 ||
		vRung.TranscodeSession.Height != 720 ||
		vRung.TranscodeSession.TranscodeHwRequested != "1" ||
		vRung.TranscodeSession.TranscodeHwFullPipeline != "0" {
		t.Fatalf("ABR rung TranscodeSession = %+v", vRung.TranscodeSession)
	}
	if vRung.Media[0].VideoResolution != "720p" || vRung.Media[0].Width != 1280 || vRung.Media[0].Height != 720 || vRung.Media[0].Bitrate != 4000 {
		t.Fatalf("ABR rung Media = %+v", vRung.Media[0])
	}
	if vRung.Session == nil || vRung.Session.Bandwidth != 4200 {
		t.Fatalf("ABR rung Session = %+v, want bandwidth 4200", vRung.Session)
	}

	// Base-rung failover / adaptive copy-to-encode (Rung == ""): must also update
	// /status/sessions without requiring a non-base rung name.
	hs.encoder = &core.EncoderStatus{
		Rung:      "",
		Processor: "cpu",
		Filters:   "cpu",
		Decision: core.Decision{
			Kind:        core.DecisionTranscode,
			VideoCopy:   false,
			VideoCodec:  "h264",
			AudioCopy:   false,
			AudioCodec:  "aac",
			Container:   "mpegts",
			MaxHeight:   720,
			BitrateKbps: 2500,
			AudioStream: 1,
		},
	}
	docFailover := parseSessionsXML(t, f.get(t, "/status/sessions").Body.String())
	vFailover := docFailover.Videos[0]
	if vFailover.TranscodeSession == nil ||
		vFailover.TranscodeSession.VideoDecision != "transcode" ||
		vFailover.TranscodeSession.VideoCodec != "h264" ||
		vFailover.TranscodeSession.Width != 1280 ||
		vFailover.TranscodeSession.Height != 720 ||
		vFailover.TranscodeSession.TranscodeHwRequested != "0" ||
		vFailover.TranscodeSession.TranscodeHwFullPipeline != "" {
		t.Fatalf("failover TranscodeSession = %+v", vFailover.TranscodeSession)
	}
	if vFailover.Media[0].VideoCodec != "h264" || vFailover.Media[0].VideoResolution != "720p" || vFailover.Media[0].Bitrate != 2500 {
		t.Fatalf("failover Media = %+v", vFailover.Media[0])
	}
	if vFailover.Session == nil || vFailover.Session.Bandwidth != 2625 {
		t.Fatalf("failover Session = %+v, want bandwidth 2625", vFailover.Session)
	}
}

// Reproduces the Roku report in #622: an unstarted earlier /decision entry
// coexists with a real HEVC -> H.264 HLS transcode whose native start.m3u8
// request omits X-Plex-Session-Identifier. /status/sessions must report the
// started transcode with Part@decision="transcode", never Direct Play.
func TestStatusSessionsRokuHEVCTranscodeNotDirectPlay(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	f.playback.info = core.MediaInfo{
		Container:   "mkv",
		Duration:    sampleInfo.Duration,
		Size:        sampleInfo.Size,
		BitrateKbps: 8000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 1920, Height: 1080, FrameRate: 23.976, BitDepth: 10, BitrateKbps: 7360},
			{Index: 1, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, SampleRate: 48000, BitrateKbps: 640, Default: true},
		},
	}
	rokuUI := []string{
		"X-Plex-Client-Identifier", "roku-living-room",
		"X-Plex-Session-Identifier", "roku-play-1",
		"X-Plex-Product", "Plex for Roku",
		"X-Plex-Platform", "Roku",
		"X-Plex-Device", "Roku Ultra",
	}
	// Earlier unstarted Direct Play decision probe on a separate session ID.
	f.trans.decision = core.Decision{Kind: core.DecisionDirectPlay, VideoCopy: true, AudioCopy: true, Container: "mkv"}
	if rec := f.get(t, "/video/:/transcode/universal/decision?path=%2Flibrary%2Fmetadata%2F1000000000000550&hasMDE=1&directPlay=1&session=roku-probe", rokuUI...); rec.Code != http.StatusOK {
		t.Fatalf("probe decision: %d %s", rec.Code, rec.Body.String())
	}

	// Real HEVC -> H.264 HLS transcode started by the native player without
	// X-Plex-Session-Identifier.
	f.trans.decision = core.Decision{
		Kind:        core.DecisionTranscode,
		VideoCopy:   false,
		VideoCodec:  "h264",
		AudioCopy:   false,
		AudioCodec:  "aac",
		Container:   "mpegts",
		MaxHeight:   1080,
		BitrateKbps: 6000,
		AudioStream: 1,
	}
	rokuPlayer := []string{
		"X-Plex-Client-Identifier", "roku-living-room",
		"X-Plex-Product", "Plex for Roku",
		"X-Plex-Platform", "Roku",
	}
	if rec := f.get(t, "/video/:/transcode/universal/decision?path=%2Flibrary%2Fmetadata%2F1000000000000550&protocol=hls&directPlay=0&directStream=0&session=roku-transcode", rokuPlayer...); rec.Code != http.StatusOK {
		t.Fatalf("roku transcode decision: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.get(t, "/video/:/transcode/universal/start.m3u8?path=%2Flibrary%2Fmetadata%2F1000000000000550&protocol=hls&directPlay=0&directStream=0&session=roku-transcode", rokuPlayer...); rec.Code != http.StatusOK {
		t.Fatalf("roku start.m3u8: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.get(t, "/:/timeline?ratingKey=1000000000000550&key=%2Flibrary%2Fmetadata%2F1000000000000550&state=playing&time=12000&duration=8340000", rokuUI...); rec.Code != http.StatusOK {
		t.Fatalf("roku timeline: %d %s", rec.Code, rec.Body.String())
	}

	doc := parseSessionsXML(t, f.get(t, "/status/sessions").Body.String())
	if len(doc.Videos) != 1 || doc.Videos[0].TranscodeSession == nil || len(doc.Videos[0].Media) != 1 {
		t.Fatalf("roku /status/sessions malformed: %+v", doc)
	}
	v := doc.Videos[0]
	if v.TranscodeSession.Key != "/transcode/sessions/roku-transcode" ||
		v.TranscodeSession.VideoDecision != "transcode" ||
		v.TranscodeSession.SourceVideoCodec != "hevc" ||
		v.TranscodeSession.VideoCodec != "h264" {
		t.Fatalf("roku TranscodeSession = %+v", v.TranscodeSession)
	}
	if v.Media[0].VideoCodec != "h264" || v.Media[0].Bitrate != 6000 {
		t.Fatalf("roku Media = %+v", v.Media[0])
	}
	if v.Media[0].Parts[0].Decision != "transcode" {
		t.Fatalf("roku Part decision = %q, want transcode", v.Media[0].Parts[0].Decision)
	}
	vs := findWireStream(v.Media[0].Parts[0].Streams, 1)
	if vs == nil || vs.Decision != "transcode" || vs.Codec != "h264" {
		t.Fatalf("roku video stream = %+v", vs)
	}
	if v.Session == nil || v.Session.Bandwidth != 6300 {
		t.Fatalf("roku Session = %+v, want bandwidth 6300", v.Session)
	}
}

// Reproduces the Firefox scope-movie report in #622: a 1918x800 source
// transcoded to 480p must report scaled output dimensions and 480p resolution,
// not the source 1918x800 or an inflated 576p/1080p tier.
func TestStatusSessionsScope480pOutputDimensions(t *testing.T) {
	f, dt := dashFixture()
	defer f.srv.Close()
	f.srv.setTranscoder(&qualityTranscoder{dt})

	f.playback.info = core.MediaInfo{
		Container:   "mkv",
		Duration:    sampleInfo.Duration,
		Size:        sampleInfo.Size,
		BitrateKbps: 8500,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 1918, Height: 800, FrameRate: 23.976, BitDepth: 10, BitrateKbps: 7860},
			{Index: 1, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, SampleRate: 48000, BitrateKbps: 640, Default: true},
		},
	}
	headers := []string{
		"X-Plex-Client-Identifier", "firefox-web-1",
		"X-Plex-Session-Identifier", "play-ff-1",
		"X-Plex-Product", "Plex Web",
		"X-Plex-Platform", "Firefox",
		"X-Plex-Device", "Windows",
		"X-Plex-Client-Profile-Extra", "add-transcode-target(type=videoProfile&context=streaming&protocol=dash&container=mp4&videoCodec=h264&audioCodec=aac)",
	}
	q := "path=%2Flibrary%2Fmetadata%2F1000000000000550&protocol=dash&directPlay=0&directStream=0&location=lan&maxVideoBitrate=1500&videoResolution=854x480&session=ff-480"
	if rec := f.get(t, "/video/:/transcode/universal/decision?"+q, headers...); rec.Code != http.StatusOK {
		t.Fatalf("firefox decision: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.get(t, "/video/:/transcode/universal/start.mpd?"+q, headers...); rec.Code != http.StatusOK {
		t.Fatalf("firefox start.mpd: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.get(t, "/:/timeline?ratingKey=1000000000000550&key=%2Flibrary%2Fmetadata%2F1000000000000550&state=playing&time=5000&duration=8340000", headers...); rec.Code != http.StatusOK {
		t.Fatalf("firefox timeline: %d %s", rec.Code, rec.Body.String())
	}

	doc := parseSessionsXML(t, f.get(t, "/status/sessions").Body.String())
	if len(doc.Videos) != 1 || doc.Videos[0].TranscodeSession == nil || len(doc.Videos[0].Media) != 1 {
		t.Fatalf("firefox /status/sessions malformed: %+v", doc)
	}
	v := doc.Videos[0]
	m := v.Media[0]
	if m.VideoResolution != "480p" || m.Width != 1150 || m.Height != 480 || m.VideoCodec != "h264" || m.Bitrate <= 0 || m.Bitrate > 1500 {
		t.Fatalf("firefox Media = %+v, want 480p 1150x480 h264 <=1500 kbps", m)
	}
	if v.TranscodeSession.Width != 1150 || v.TranscodeSession.Height != 480 || v.TranscodeSession.VideoDecision != "transcode" || v.TranscodeSession.VideoCodec != "h264" {
		t.Fatalf("firefox TranscodeSession = %+v", v.TranscodeSession)
	}
	vs := findWireStream(m.Parts[0].Streams, 1)
	if vs == nil || vs.Decision != "transcode" || vs.Codec != "h264" || vs.Width != 1150 || vs.Height != 480 {
		t.Fatalf("firefox video stream = %+v", vs)
	}
	if v.Session == nil || v.Session.Bandwidth <= 0 || v.Session.Bandwidth > 1600 {
		t.Fatalf("firefox Session = %+v, want bandwidth <= 1600", v.Session)
	}
}

// Verifies Direct Play still reports as Direct Play: no TranscodeSession,
// Part@decision omitted, original source Media, and Direct Play bandwidth.
func TestStatusSessionsDirectPlayOmitsTranscodeSession(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	headers := []string{
		"X-Plex-Client-Identifier", "dp-client",
		"X-Plex-Session-Identifier", "dp-play-1",
		"X-Plex-Product", "Plex Web",
		"X-Plex-Platform", "Chrome",
		"X-Plex-Device", "OSX",
	}
	if rec := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/1000000000000550&hasMDE=1&directPlay=1&session=dp-sess", headers...); rec.Code != http.StatusOK {
		t.Fatalf("decision: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.get(t, "/library/parts/1000000000000550/1/file.mkv", append(headers, "Range", "bytes=0-9")...); rec.Code != http.StatusPartialContent {
		t.Fatalf("part: %d", rec.Code)
	}
	if rec := f.get(t, "/:/timeline?ratingKey=1000000000000550&key=%2Flibrary%2Fmetadata%2F1000000000000550&state=playing&time=60000&duration=8340000", headers...); rec.Code != http.StatusOK {
		t.Fatalf("timeline: %d", rec.Code)
	}

	raw := f.get(t, "/status/sessions").Body.String()
	doc := parseSessionsXML(t, raw)
	if len(doc.Videos) != 1 || len(doc.Videos[0].Media) != 1 {
		t.Fatalf("direct play /status/sessions malformed: %s", raw)
	}
	v := doc.Videos[0]
	if v.TranscodeSession != nil {
		t.Fatalf("direct play emitted TranscodeSession: %+v", v.TranscodeSession)
	}
	if v.Media[0].Parts[0].Decision != "" {
		t.Fatalf("direct play Part decision = %q, want omitted", v.Media[0].Parts[0].Decision)
	}
	if v.Media[0].Width != 1920 || v.Media[0].Height != 1080 || v.Media[0].Bitrate != 7600 {
		t.Fatalf("direct play Media = %+v", v.Media[0])
	}
	if v.Session == nil || v.Session.Bandwidth != 15960 {
		t.Fatalf("direct play Session = %+v, want bandwidth 15960", v.Session)
	}
	if tsList := f.get(t, "/transcode/sessions").Body.String(); strings.Contains(tsList, "TranscodeSession") {
		t.Fatalf("direct play leaked into /transcode/sessions: %s", tsList)
	}
}
