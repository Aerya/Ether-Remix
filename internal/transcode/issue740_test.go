package transcode

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/subtitles"
)

func TestDASHCollectBalancesTracksByMediaTime(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, dashInitFile(0)), testVideoInit(1920, 1080), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, dashInitFile(1)), testAudioInit(2, 48000), 0o644); err != nil {
		t.Fatal(err)
	}
	// 2s video GOPs (60 chunks = 120s) paired with 1s AAC audio (120 chunks = 120s),
	// 6s segments (20 segments total), matching #740's copy-video + AAC-audio ratio.
	for n := 1; n <= 60; n++ {
		v := testChunk(uint32(n), int64(n-1)*2*90000, 3750, 48, []byte("v"))
		if err := os.WriteFile(filepath.Join(runDir, dashChunkFile(0, n)), v, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for n := 1; n <= 120; n++ {
		a := testChunk(uint32(n), int64(n-1)*48000, 960, 50, []byte("a"))
		if err := os.WriteFile(filepath.Join(runDir, dashChunkFile(1, n)), a, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s := &Session{
		proto:  core.ProtocolDASH,
		m:      &Manager{opts: options{}},
		dir:    dir,
		segDur: 6 * time.Second,
		total:  20,
		log:    slog.New(slog.DiscardHandler),
		sessionDelivery: sessionDelivery{
			done:    []map[int]bool{{}, {}},
			changed: make(chan struct{}),
			dash:    newDASHState([]dashRep{{ID: 0, Kind: "video"}, {ID: 1, Kind: "audio"}}, nil, "balance"),
		},
	}
	proc := &fakeProc{done: make(chan struct{})}
	r := &run{
		seq:         1,
		proc:        proc,
		maxDone:     -1,
		leadDone:    -1,
		dashDiskSeg: -1,
		started:     time.Now(),
		dash:        newDASHRun(runDir, []int{0, 1}, 0, s.segDur, s.total, 0),
	}
	s.cur = r

	// Pass 1: both tracks advance by dashCollectMediaWindow (32s) -> 16 video chunks, 32 audio chunks.
	s.collectDASH(r, false)
	vTrack, aTrack := r.dash.tracks[0], r.dash.tracks[1]
	if vTrack.next != 17 || aTrack.next != 33 {
		t.Fatalf("pass 1 next: video=%d audio=%d, want 17 and 33", vTrack.next, aTrack.next)
	}
	if vTrack.mediaEnd != 32*time.Second || aTrack.mediaEnd != 32*time.Second {
		t.Fatalf("pass 1 mediaEnd: video=%v audio=%v, want 32s each", vTrack.mediaEnd, aTrack.mediaEnd)
	}
	if vTrack.sl.last != aTrack.sl.last || vTrack.sl.last != 4 {
		t.Fatalf("pass 1 published last seg: video=%d audio=%d, want 4 each", vTrack.sl.last, aTrack.sl.last)
	}

	// Pass 2: both advance another 32s in lockstep -> 32 video chunks (64s), 64 audio chunks (64s).
	s.collectDASH(r, false)
	if vTrack.next != 33 || aTrack.next != 65 {
		t.Fatalf("pass 2 next: video=%d audio=%d, want 33 and 65", vTrack.next, aTrack.next)
	}
	if vTrack.sl.last != aTrack.sl.last || vTrack.sl.last != 9 {
		t.Fatalf("pass 2 published last seg: video=%d audio=%d, want 9 each", vTrack.sl.last, aTrack.sl.last)
	}

	// Simulate audio lagging behind video: video is already past minEnd+32s,
	// so video must hold (0 chunks consumed) while audio catches up.
	t.Run("ahead-track-holds-while-lagging-track-catches-up", func(t *testing.T) {
		dir2 := t.TempDir()
		runDir2 := filepath.Join(dir2, "run-1")
		if err := os.MkdirAll(runDir2, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(runDir2, dashInitFile(0)), testVideoInit(1920, 1080), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(runDir2, dashInitFile(1)), testAudioInit(2, 48000), 0o644); err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= 40; n++ {
			v := testChunk(uint32(n), int64(n-1)*2*90000, 3750, 48, []byte("v"))
			if err := os.WriteFile(filepath.Join(runDir2, dashChunkFile(0, n)), v, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		// Audio has not written any chunks yet.
		s2 := &Session{
			proto:  core.ProtocolDASH,
			m:      &Manager{opts: options{}},
			dir:    dir2,
			segDur: 6 * time.Second,
			total:  20,
			log:    slog.New(slog.DiscardHandler),
			sessionDelivery: sessionDelivery{
				done:    []map[int]bool{{}, {}},
				changed: make(chan struct{}),
				dash:    newDASHState([]dashRep{{ID: 0, Kind: "video"}, {ID: 1, Kind: "audio"}}, nil, "catchup"),
			},
		}
		r2 := &run{
			seq:         1,
			proc:        &fakeProc{done: make(chan struct{})},
			maxDone:     -1,
			leadDone:    -1,
			dashDiskSeg: -1,
			started:     time.Now(),
			dash:        newDASHRun(runDir2, []int{0, 1}, 0, s2.segDur, s2.total, 0),
		}
		s2.cur = r2

		// Pass 1: video advances to 32s; audio has nothing yet.
		s2.collectDASH(r2, false)
		vt, at := r2.dash.tracks[0], r2.dash.tracks[1]
		if vt.next != 17 || at.next != 1 {
			t.Fatalf("pass 1: video.next=%d audio.next=%d, want 17 and 1", vt.next, at.next)
		}

		// Pass 2: audio writes 10s of chunks; targetEnd is still 0+32s=32s, so video
		// must consume 0 additional chunks while audio catches up to 10s.
		for n := 1; n <= 10; n++ {
			a := testChunk(uint32(n), int64(n-1)*48000, 960, 50, []byte("a"))
			if err := os.WriteFile(filepath.Join(runDir2, dashChunkFile(1, n)), a, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		s2.collectDASH(r2, false)
		if vt.next != 17 {
			t.Fatalf("ahead video consumed chunks while audio caught up: next %d, want 17", vt.next)
		}
		if at.mediaEnd != 10*time.Second {
			t.Fatalf("audio mediaEnd = %v, want 10s", at.mediaEnd)
		}

		// Pass 3: audio now has 60s on disk; minEnd=10s -> targetEnd=42s.
		// Video advances only 10s (5 chunks -> 42s) while audio advances 32s (32 chunks -> 42s).
		for n := 11; n <= 60; n++ {
			a := testChunk(uint32(n), int64(n-1)*48000, 960, 50, []byte("a"))
			if err := os.WriteFile(filepath.Join(runDir2, dashChunkFile(1, n)), a, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		s2.collectDASH(r2, false)
		if vt.mediaEnd != 42*time.Second || at.mediaEnd != 42*time.Second {
			t.Fatalf("pass 3 convergence: video=%v audio=%v, want 42s each", vt.mediaEnd, at.mediaEnd)
		}
	})
}

func TestThrottleLaggingDASHTrack(t *testing.T) {
	t.Run("pauses-on-leading-track-and-drains-lagging-without-kill", func(t *testing.T) {
		dir := t.TempDir()
		runDir := filepath.Join(dir, "run-1")
		if err := os.MkdirAll(runDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(runDir, dashInitFile(0)), testVideoInit(1920, 1080), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(runDir, dashInitFile(1)), testAudioInit(2, 48000), 0o644); err != nil {
			t.Fatal(err)
		}
		// Video is far ahead (40 x 2s = 80s), audio has chunks on disk (40 x 1s = 40s)
		// while playhead stays at 0 and aheadLimit is 4 (24s / 6s).
		for n := 1; n <= 40; n++ {
			v := testChunk(uint32(n), int64(n-1)*2*90000, 3750, 48, []byte("v"))
			a := testChunk(uint32(n), int64(n-1)*48000, 960, 50, []byte("a"))
			if err := os.WriteFile(filepath.Join(runDir, dashChunkFile(0, n)), v, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(runDir, dashChunkFile(1, n)), a, 0o644); err != nil {
				t.Fatal(err)
			}
		}

		p := &fakeProc{done: make(chan struct{}), kill: make(chan struct{})}
		s := &Session{
			proto:  core.ProtocolDASH,
			m:      &Manager{opts: options{maxAhead: 24 * time.Second, keepBehind: 24 * time.Second}},
			dir:    dir,
			segDur: 6 * time.Second,
			total:  100,
			info:   core.MediaInfo{Duration: 10 * time.Minute},
			log:    slog.New(slog.DiscardHandler),
			sessionEncoding: sessionEncoding{
				resumeAt: -1,
			},
			sessionDelivery: sessionDelivery{
				done:    []map[int]bool{{}, {}},
				changed: make(chan struct{}),
				dash:    newDASHState([]dashRep{{ID: 0, Kind: "video"}, {ID: 1, Kind: "audio"}}, nil, "lag"),
			},
		}
		r := &run{
			seq:         1,
			proc:        p,
			maxDone:     -1,
			leadDone:    -1,
			dashDiskSeg: -1,
			started:     time.Now(),
			dash:        newDASHRun(runDir, []int{0, 1}, 0, s.segDur, s.total, 0),
		}
		// Simulate audio initially lagging behind video before collection catches up.
		r.dash.tracks[0].mediaEnd = 36 * time.Second
		r.dash.tracks[0].hasMedia = true
		r.dash.tracks[0].sl.last = 5
		r.dash.tracks[0].sl.count = 6
		r.dash.tracks[0].next = 19
		s.cur = r

		// Even with audio at segment 0 (maxDone lagging), throttle must pause on the
		// leading track + disk backlog instead of letting ffmpeg run unconstrained.
		s.collectDASH(r, false)
		if !p.isPaused() || p.wasKilled() {
			t.Fatalf("after pass 1: paused=%v killed=%v, want paused && !killed", p.isPaused(), p.wasKilled())
		}

		// Draining audio's already-written chunks while paused must NOT look like
		// pause failure (pausedLead holds the ceiling).
		s.collectDASH(r, false)
		if !p.isPaused() || p.wasKilled() {
			t.Fatalf("while draining lagging track: paused=%v killed=%v, want paused && !killed", p.isPaused(), p.wasKilled())
		}

		// Video must never have published past publishCap (lastReq + aheadLimit + 1 = 5),
		// so pruneLocked must not delete any freshly written video segments.
		s.mu.Lock()
		for n := range s.done[0] {
			if n > 5 {
				s.mu.Unlock()
				t.Fatalf("video published segment %d beyond publishCap 5", n)
			}
		}
		before := len(s.done[0])
		s.pruneLocked()
		after := len(s.done[0])
		s.mu.Unlock()
		if before != after {
			t.Fatalf("pruneLocked evicted freshly written video segments: %d -> %d", before, after)
		}
	})

	t.Run("pauses-on-out-time-when-produced-is-zero", func(t *testing.T) {
		p := &fakeProc{done: make(chan struct{}), kill: make(chan struct{})}
		s := &Session{
			proto:  core.ProtocolDASH,
			m:      &Manager{opts: options{maxAhead: 24 * time.Second}},
			segDur: 6 * time.Second,
			total:  200,
			info:   core.MediaInfo{Duration: 20 * time.Minute},
			log:    slog.New(slog.DiscardHandler),
			sessionEncoding: sessionEncoding{
				resumeAt: -1,
			},
			sessionDelivery: sessionDelivery{
				lastReq: 10,
				changed: make(chan struct{}),
			},
		}
		r := &run{
			seq:         1,
			startSeg:    10,
			spec:        runSpec{Start: 60 * time.Second},
			proc:        p,
			maxDone:     -1,
			leadDone:    -1,
			dashDiskSeg: -1,
			progress:    Progress{OutTime: 180 * time.Second}, // seg 30, 20 ahead of playhead 10
		}
		s.cur = r

		s.mu.Lock()
		s.throttleLocked()
		s.mu.Unlock()
		if !p.isPaused() {
			t.Fatal("expected ffmpeg to pause on OutTime ahead of playhead even with produced == 0")
		}
	})

	t.Run("does-not-deadlock-when-drained-track-starved-below-playhead", func(t *testing.T) {
		p := &fakeProc{done: make(chan struct{}), kill: make(chan struct{})}
		s := &Session{
			proto:  core.ProtocolDASH,
			m:      &Manager{opts: options{maxAhead: 24 * time.Second}},
			segDur: 6 * time.Second,
			total:  200,
			info:   core.MediaInfo{Duration: 20 * time.Minute},
			log:    slog.New(slog.DiscardHandler),
			sessionEncoding: sessionEncoding{
				resumeAt: -1,
			},
			sessionDelivery: sessionDelivery{
				lastReq: 10,
				changed: make(chan struct{}),
			},
		}
		r := &run{
			seq:           1,
			startSeg:      10,
			proc:          p,
			maxDone:       -1,
			leadDone:      18,
			hasLead:       true,
			minDrainedSeg: -1, // other track drained on disk and still below lastReq=10
			hasMinDrained: true,
		}
		s.cur = r

		s.mu.Lock()
		s.throttleLocked()
		s.mu.Unlock()
		if p.isPaused() {
			t.Fatal("paused while another track is drained below playhead (would deadlock long GOP)")
		}
	})

	t.Run("no-pause-caps-runaway-and-resumes-at-run-start", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		p := &fakeProc{done: done, kill: make(chan struct{})}
		s := &Session{
			proto:   core.ProtocolDASH,
			m:       &Manager{opts: options{maxAhead: 24 * time.Second}},
			segDur:  6 * time.Second,
			total:   1000,
			info:    core.MediaInfo{Duration: 100 * time.Minute},
			log:     slog.New(slog.DiscardHandler),
			noPause: true,
			sessionEncoding: sessionEncoding{
				resumeAt: -1,
			},
			sessionDelivery: sessionDelivery{
				lastReq: 341,
				changed: make(chan struct{}),
			},
		}
		r := &run{
			seq:            1,
			startSeg:       341,
			proc:           p,
			maxDone:        -1, // produced == 0 because one track lagged
			leadDone:       357,
			hasLead:        true,
			dashDiskSeg:    400, // 59 segments ahead (> max(2*4, 4+8) = 12)
			hasDashDiskSeg: true,
		}
		s.cur = r

		s.mu.Lock()
		s.throttleLocked()
		cur, resumeAt := s.cur, s.resumeAt
		s.mu.Unlock()
		s.watchers.Wait()

		if cur != nil {
			t.Fatal("expected runaway run to be stopped under noPause")
		}
		if resumeAt != 341 {
			t.Fatalf("resumeAt = %d, want 341 (r.next() when produced == 0)", resumeAt)
		}
	})
}

func TestThrottleDASHBacklogCap(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, dashInitFile(0)), testVideoInit(1920, 1080), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, dashInitFile(1)), testAudioInit(2, 48000), 0o644); err != nil {
		t.Fatal(err)
	}
	// Publish enough chunks to hit publishCap (5), plus uncollected chunks that
	// push run-1/ past dashBacklogPause (4 KiB) and then past dashBacklogStop (8 KiB).
	payload := make([]byte, 512)
	for n := 1; n <= 40; n++ {
		v := testChunk(uint32(n), int64(n-1)*2*90000, 3750, 48, payload)
		a := testChunk(uint32(n), int64(n-1)*48000, 960, 50, []byte("a"))
		if err := os.WriteFile(filepath.Join(runDir, dashChunkFile(0, n)), v, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(runDir, dashChunkFile(1, n)), a, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	done := make(chan struct{})
	close(done)
	p := &fakeProc{done: done, kill: make(chan struct{})}
	s := &Session{
		proto: core.ProtocolDASH,
		m: &Manager{opts: options{
			maxAhead:         24 * time.Second,
			dashBacklogPause: 4 << 10,
			dashBacklogStop:  8 << 10,
		}},
		dir:    dir,
		segDur: 6 * time.Second,
		total:  200,
		info:   core.MediaInfo{Duration: 20 * time.Minute},
		log:    slog.New(slog.DiscardHandler),
		sessionEncoding: sessionEncoding{
			resumeAt: -1,
		},
		sessionDelivery: sessionDelivery{
			done:    []map[int]bool{{}, {}},
			changed: make(chan struct{}),
			dash:    newDASHState([]dashRep{{ID: 0, Kind: "video"}, {ID: 1, Kind: "audio"}}, nil, "backlog"),
		},
	}
	r := &run{
		seq:         1,
		proc:        p,
		maxDone:     -1,
		leadDone:    -1,
		dashDiskSeg: -1,
		started:     time.Now(),
		dash:        newDASHRun(runDir, []int{0, 1}, 0, s.segDur, s.total, 0),
	}
	s.cur = r

	s.collectDASH(r, false)
	if !p.isPaused() {
		t.Fatalf("expected pause on chunk backlog (%d bytes)", r.dashBacklogBytes)
	}
	if r.dashBacklogBytes < 4<<10 {
		t.Fatalf("dashBacklogBytes = %d, want >= %d", r.dashBacklogBytes, 4<<10)
	}

	// Simulate an encoder that ignored Pause and kept writing chunks past the hard stop cap.
	for n := 41; n <= 65; n++ {
		v := testChunk(uint32(n), int64(n-1)*2*90000, 3750, 48, payload)
		if err := os.WriteFile(filepath.Join(runDir, dashChunkFile(0, n)), v, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s.collectDASH(r, false)
	s.mu.Lock()
	cur := s.cur
	s.mu.Unlock()
	s.watchers.Wait()
	if cur != nil {
		t.Fatal("expected encoder to stop once uncollected chunk backlog grew past hard cap")
	}
}

func TestTextSubtitlePersistenceDoesNotBlockCollection(t *testing.T) {
	tempDir := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	var (
		startOnce sync.Once
		mu        sync.Mutex
		calls     int
		persisted = make(chan struct{}, 16)
	)

	m := &Manager{
		tempDir: tempDir,
		log:     slog.New(slog.DiscardHandler),
		opts:    options{},
	}
	m.textCues.writeFn = func(dir string, minute int64, cues []subtitles.Cue) error {
		startOnce.Do(func() { close(started) })
		<-release
		mu.Lock()
		calls++
		mu.Unlock()
		err := persistTextCuesFile(dir, minute, cues)
		select {
		case persisted <- struct{}{}:
		default:
		}
		return err
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		if done := m.textCues.close(); done != nil {
			<-done
		}
	}()

	assPath := filepath.Join(tempDir, "text-2-1.ass")
	writeASS := func(content string) {
		t.Helper()
		f, err := os.OpenFile(assPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(content); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	writeASS("[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,First line\n")

	// Set up a DASH run + an HLS playlist to verify both protocols collect while
	// the background subtitle writer is blocked.
	runDir := filepath.Join(tempDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, dashInitFile(0)), testVideoInit(1280, 720), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, dashInitFile(1)), testAudioInit(2, 48000), 0o644); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 6; n++ {
		v := testChunk(uint32(n), int64(n-1)*90000, 3750, 24, []byte("v"))
		a := testChunk(uint32(n), int64(n-1)*48000, 960, 50, []byte("a"))
		if err := os.WriteFile(filepath.Join(runDir, dashChunkFile(0, n)), v, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(runDir, dashChunkFile(1, n)), a, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	info := core.MediaInfo{
		Size:     12345,
		Duration: 10 * time.Minute,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc"},
			{Index: 1, Type: "audio", Codec: "aac"},
			{Index: 2, Type: "subtitle", Codec: "ass"},
		},
	}
	s := &Session{
		m:                   m,
		proto:               core.ProtocolDASH,
		dir:                 tempDir,
		segDur:              5 * time.Second,
		total:               120,
		info:                info,
		log:                 slog.New(slog.DiscardHandler),
		textSubtitleSource:  "release-740",
		textSubtitleStreams: []int{2},
		sessionDelivery: sessionDelivery{
			done:    []map[int]bool{{}, {}},
			changed: make(chan struct{}),
			dash:    newDASHState([]dashRep{{ID: 0, Kind: "video"}, {ID: 1, Kind: "audio"}}, nil, "sub-nonblock"),
		},
	}
	proc := &fakeProc{done: make(chan struct{})}
	r := &run{
		seq:         1,
		proc:        proc,
		maxDone:     -1,
		leadDone:    -1,
		dashDiskSeg: -1,
		started:     time.Now(),
		spec:        runSpec{TextSubtitlePaths: map[int]string{2: assPath}},
		dash:        newDASHRun(runDir, []int{0, 1}, 0, s.segDur, s.total, 0),
	}
	s.cur = r

	// Collect subtitles and DASH segments: must complete without waiting on release.
	s.collectTextSubtitles(r)
	<-started // writer goroutine is now blocked inside writeFn
	s.collectDASH(r, false)
	if !s.done[0][0] || !s.done[1][0] {
		t.Fatalf("DASH collection failed while subtitle writer blocked: done=%v", s.done)
	}

	// Verify HLS poll also runs immediately alongside a blocked subtitle writer.
	hlsDir := filepath.Join(tempDir, "hls")
	if err := os.MkdirAll(hlsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hlsDir, "00000.ts"), []byte("ts0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hlsDir, "00001.ts"), []byte("ts1"), 0o644); err != nil {
		t.Fatal(err)
	}
	playlist := filepath.Join(hlsDir, "run-1.m3u8")
	if err := os.WriteFile(playlist, []byte("#EXTM3U\n#EXTINF:4.0,\n00000.ts\n#EXTINF:4.0,\n00001.ts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hlsSess := &Session{
		m:                   m,
		container:           ContainerMPEGTS,
		dir:                 hlsDir,
		segDur:              4 * time.Second,
		total:               10,
		info:                info,
		log:                 slog.New(slog.DiscardHandler),
		textSubtitleSource:  "release-740",
		textSubtitleStreams: []int{2},
		sessionDelivery: sessionDelivery{
			done:    []map[int]bool{{}},
			changed: make(chan struct{}),
		},
	}
	hlsRun := &run{
		seq:     1,
		proc:    proc,
		maxDone: -1,
		spec:    runSpec{Dir: hlsDir, Playlist: "run-1.m3u8", TextSubtitlePaths: map[int]string{2: assPath}},
	}
	hlsSess.cur = hlsRun
	hlsSess.collectTextSubtitles(hlsRun)
	hlsSess.poll(hlsRun, playlist, false)
	if !hlsSess.done[0][0] {
		t.Fatal("HLS poll failed while subtitle writer blocked")
	}

	// Subtitle requests must see the cue immediately from memory before disk write completes.
	cues, err := s.TextSubtitleCues(2, 0, 20*time.Second)
	if err != nil || len(cues) != 1 || cues[0].Text != "First line" {
		t.Fatalf("in-memory cues before persist: %+v err=%v", cues, err)
	}

	// Append two more lines in the SAME minute while the writer is still blocked:
	// both must be visible immediately and coalesce into a single follow-up disk write.
	writeASS("Dialogue: 0,0:00:05.00,0:00:07.00,Default,,0,0,0,,Second line\n")
	s.collectTextSubtitles(r)
	writeASS("Dialogue: 0,0:00:09.00,0:00:11.00,Default,,0,0,0,,Third line\n")
	s.collectTextSubtitles(r)

	cues, err = s.TextSubtitleCues(2, 0, 20*time.Second)
	if err != nil || len(cues) != 3 {
		t.Fatalf("coalesced in-memory cues: got %d (%+v), err=%v", len(cues), cues, err)
	}

	// Release the writer and wait for the background worker to drain cleanly.
	close(release)
	<-persisted // first write
	<-persisted // single coalesced follow-up write for lines 2+3

	m.textCues.mu.Lock()
	done := m.textCues.done
	m.textCues.mu.Unlock()
	if done != nil {
		<-done
	}

	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 2 {
		t.Fatalf("persist calls = %d, want 2 (initial + single coalesced follow-up)", gotCalls)
	}

	// A fresh Manager reading purely from disk must see all 3 cues.
	diskReader := &Session{
		m:                   &Manager{tempDir: tempDir},
		info:                info,
		textSubtitleSource:  "release-740",
		textSubtitleStreams: []int{2},
	}
	diskCues, err := diskReader.TextSubtitleCues(2, 0, 20*time.Second)
	if err != nil || len(diskCues) != 3 {
		t.Fatalf("persisted disk cues: got %d (%+v), err=%v", len(diskCues), diskCues, err)
	}
}

func TestTextCueStoreBoundsPendingWindows(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	st := &textCueStore{
		maxWin: 3,
		writeFn: func(dir string, minute int64, cues []subtitles.Cue) error {
			once.Do(func() { close(started) })
			<-block
			return nil
		},
	}
	defer func() {
		close(block)
		if done := st.close(); done != nil {
			<-done
		}
	}()

	for m := int64(0); m < 6; m++ {
		st.enqueue("/tmp/sub", m, []subtitles.Cue{{Start: time.Duration(m) * time.Minute, End: time.Duration(m)*time.Minute + time.Second, Text: fmt.Sprintf("m%d", m)}}, nil)
		if m == 0 {
			<-started
		}
	}
	st.mu.Lock()
	n := len(st.windows)
	st.mu.Unlock()
	if n > 3 {
		t.Fatalf("pending windows = %d, want <= maxWin (3)", n)
	}
}

func TestDASHResumeStartSegmentBounds(t *testing.T) {
	cases := []struct {
		name           string
		duration       time.Duration
		firstSegmentAt time.Duration
		keyframes      []core.KeyframePoint
		wantStart      int
	}{
		{
			name:           "first-segment-clamps-at-zero",
			duration:       10 * time.Minute,
			firstSegmentAt: 2 * time.Second,
			wantStart:      0,
		},
		{
			name:           "second-segment-backs-up-to-zero",
			duration:       10 * time.Minute,
			firstSegmentAt: 5 * time.Second,
			wantStart:      0,
		},
		{
			name:           "near-end-clamps-to-total-minus-2",
			duration:       60 * time.Second,
			firstSegmentAt: 59 * time.Second, // at=11 (total=12) -> at-1=10 == total-2
			wantStart:      10,
		},
		{
			name:           "keyframe-planned-backs-up-one-segment",
			duration:       10 * time.Minute,
			firstSegmentAt: 32 * time.Second,
			keyframes: func() []core.KeyframePoint {
				var ks []core.KeyframePoint
				for ts := time.Duration(0); ts < 10*time.Minute; ts += 2 * time.Second {
					ks = append(ks, core.KeyframePoint{Time: ts})
				}
				return ks
			}(),
			// 5s target snaps to 6s keyframes: seg 0=[0..6), 1=[6..12), 2=[12..18), 3=[18..24), 4=[24..30), 5=[30..36) -> at=5, at-1=4.
			wantStart: 4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fr := newFakeRunner()
			m := newTestManager(t, fr, config.Transcode{})
			req := dashRequest(t, "resume-"+tc.name, tc.duration)
			req.FirstSegmentAt = tc.firstSegmentAt
			req.Info.Keyframes = tc.keyframes
			s := startDASHSession(t, m, req)
			if s.lastReq != tc.wantStart {
				t.Fatalf("start segment = %d, want %d", s.lastReq, tc.wantStart)
			}
		})
	}
}
