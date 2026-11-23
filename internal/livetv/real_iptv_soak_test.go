package livetv

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// The real IPTV soak runs Steady end to end on this host's ffmpeg against a
// local provider that restreams like Mad Net: a live 60p transport stream
// delivered in bursts (everything produced since the previous burst, every
// 2-4 s, with an occasional longer gap), an initial backlog at connect, and
// a one-connection account that refuses a second connection. It plays the
// output like a client for -real-iptv-soak and writes the server log to
// -real-iptv-log. Opt-in measurement, never part of the normal test run.
var (
	realIPTVSoak = flag.Duration("real-iptv-soak", 0, "run the real-ffmpeg Mad Net soak for this long")
	realIPTVLog  = flag.String("real-iptv-log", "", "write the soak's server log here")
	realIPTVGap  = flag.Duration("real-iptv-gap", 4*time.Second, "longest provider burst gap")
	realIPTVHt   = flag.Int("real-iptv-height", 720, "source height")
	realIPTVDrop = flag.Duration("real-iptv-drop", 0, "lose two seconds of provider media this often (a missing segment)")
)

type madnetEmu struct {
	mu      sync.Mutex
	cond    *sync.Cond
	chunks  [][]byte
	at      []time.Time
	base    int // absolute index of chunks[0]
	active  int
	refused int
	opens   int
}

func (m *madnetEmu) produce(ctx context.Context, r io.Reader) {
	buf := make([]byte, 188*64)
	for ctx.Err() == nil {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			m.mu.Lock()
			m.chunks = append(m.chunks, append([]byte(nil), buf[:n]...))
			m.at = append(m.at, time.Now())
			if len(m.chunks) > 20000 {
				m.chunks, m.at, m.base = m.chunks[5000:], m.at[5000:], m.base+5000
			}
			m.cond.Broadcast()
			m.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (m *madnetEmu) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.opens++
	if m.active > 0 {
		m.refused++
		m.mu.Unlock()
		http.Error(w, "max connections", 458)
		return
	}
	m.active++
	// Initial backlog: the provider's own buffer, about one segment.
	from := len(m.chunks) - 1
	for from > 0 && time.Since(m.at[from-1]) < 4*time.Second {
		from--
	}
	next := m.base + from
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.active--; m.mu.Unlock() }()
	w.Header().Set("Content-Type", "video/mp2t")
	w.WriteHeader(http.StatusOK)
	flusher := w.(http.Flusher)
	dropAt := time.Now().Add(*realIPTVDrop)
	for {
		m.mu.Lock()
		var out []byte
		for i := next - m.base; i >= 0 && i < len(m.chunks); i++ {
			if *realIPTVDrop > 0 && m.at[i].After(dropAt) && m.at[i].Before(dropAt.Add(2*time.Second)) {
				next++
				continue
			}
			out = append(out, m.chunks[i]...)
			next++
		}
		if *realIPTVDrop > 0 && time.Now().After(dropAt.Add(2*time.Second)) {
			dropAt = dropAt.Add(*realIPTVDrop)
		}
		m.mu.Unlock()
		if len(out) > 0 {
			if _, err := w.Write(out); err != nil {
				return
			}
			flusher.Flush()
		}
		gap := time.Duration(2000+rand.IntN(2000)) * time.Millisecond
		if rand.IntN(12) == 0 {
			gap = *realIPTVGap
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(gap):
		}
	}
}

func TestRealIPTVSoak(t *testing.T) {
	if *realIPTVSoak <= 0 {
		t.Skip("opt-in: -real-iptv-soak=5m")
	}
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg absent")
	}
	fp, _ := exec.LookPath("ffprobe")
	ctx, cancel := context.WithTimeout(context.Background(), *realIPTVSoak+2*time.Minute)
	defer cancel()
	dir := t.TempDir()

	emu := &madnetEmu{}
	emu.cond = sync.NewCond(&emu.mu)
	h := *realIPTVHt
	src := exec.CommandContext(ctx, ff, "-v", "error", "-re", "-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=60000/1001", h*16/9, h),
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-c:v", "libx264", "-preset", "veryfast", "-tune", "zerolatency", "-b:v", "6M", "-g", "60",
		"-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2", "-f", "mpegts", "-mpegts_flags", "resend_headers", "pipe:1")
	out, _ := src.StdoutPipe()
	if err := src.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = src.Wait() }()
	go emu.produce(ctx, out)
	prov := httptest.NewServer(emu)
	defer prov.Close()
	time.Sleep(6 * time.Second) // provider backlog

	logOut := io.Writer(io.Discard)
	if *realIPTVLog != "" {
		f, err := os.Create(*realIPTVLog)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		logOut = f
	}
	logger := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: slog.LevelInfo}))
	manager, err := transcode.New(config.Transcode{FFmpegPath: ff, FFprobePath: fp, TempDir: filepath.Join(dir, "out"), HWAccel: "videotoolbox"}, logger.With("component", "transcode"))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	db, err := store.OpenPath(ctx, filepath.Join(dir, "ether.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := testService(t, Options{DB: db, Logger: logger.With("component", "livetv"), Transcoder: manager, HTTPClient: &http.Client{},
		Stability: func() string { return stabilityBalanced }})
	if _, err := s.ImportSource(ctx, Source{ID: "madnet", Name: "Mad Net", MaxConnections: 1,
		Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"fox\",Fox News\n" + prov.URL + "/live/fox.ts\n"}); err != nil {
		t.Fatal(err)
	}
	channels, err := s.Channels(ctx)
	if err != nil || len(channels) != 1 {
		t.Fatalf("channels = %d, err = %v", len(channels), err)
	}
	ch := channels[0]
	ch.Transcode = true
	if _, err := s.SaveChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	user := &core.User{ID: 1, IsOwner: true}
	started := time.Now()
	info, err := s.Tune(ctx, user, ch.ID, core.ClientCaps{Protocol: "vela"})
	if err != nil {
		t.Fatalf("tune: %v", err)
	}
	t.Logf("tuned in %v", time.Since(started))

	// A client: reload the playlist every second, fetch each new segment,
	// and model a player buffer starting at the first listed segment.
	var buffered time.Duration // media fetched ahead of the playhead
	playing := false
	stalls, stallTime := 0, time.Duration(0)
	last := int64(-1)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	prev := time.Now()
	for time.Since(started) < *realIPTVSoak {
		<-tick.C
		now := time.Now()
		dt := now.Sub(prev)
		prev = now
		if playing {
			if buffered >= dt {
				buffered -= dt
			} else {
				stalls++
				playing = false
				buffered = 0
				fmt.Fprintf(logOut, "time=%s CLIENT stall at=%v\n", now.Format(time.RFC3339Nano), now.Sub(started).Round(time.Millisecond))
			}
		} else if last >= 0 {
			stallTime += dt
		}
		ss, err := s.getSession(ctx, info.ID, user)
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		rec := httptest.NewRecorder()
		s.serveBroadcastPlaylist(rec, httptest.NewRequest(http.MethodGet, "/livetv/sessions/"+info.ID+"/index.m3u8", nil), ss)
		if rec.Code != http.StatusOK {
			continue
		}
		var dur time.Duration
		for _, line := range strings.Split(rec.Body.String(), "\n") {
			if strings.HasPrefix(line, "#EXTINF:") {
				var sec float64
				fmt.Sscanf(strings.TrimPrefix(line, "#EXTINF:"), "%f", &sec)
				dur = time.Duration(sec * float64(time.Second))
			}
			var seq int64
			if _, err := fmt.Sscanf(line, "relay/%d.ts", &seq); err == nil && seq > last {
				seg := httptest.NewRecorder()
				s.serveBroadcastTS(seg, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/livetv/sessions/%s/relay/%d.ts", info.ID, seq), nil), ss, seq)
				last = seq
				buffered += dur
			}
		}
		if !playing && buffered >= 2*time.Second {
			if stalls > 0 || last >= 0 {
				fmt.Fprintf(logOut, "time=%s CLIENT playing buffered=%v\n", now.Format(time.RFC3339Nano), buffered)
			}
			playing = true
		}
	}
	emu.mu.Lock()
	opens, refused := emu.opens, emu.refused
	emu.mu.Unlock()
	t.Logf("client stalls=%d stalledFor=%v provider opens=%d refused=%d", stalls, stallTime.Round(time.Millisecond), opens, refused)
}
