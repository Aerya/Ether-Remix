package transcode

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/subtitles"
)

// One minute windows are addressed by exact release, source size, duration,
// track and codec. They survive HLS session retirement and process restart.
// Only cues observed by the active playback FFmpeg are written here.
// Persistence runs asynchronously on a bounded, coalescing writer without
// fsync (this directory is a rebuildable cache), while reads serve in-memory
// windows first so subtitle requests see cues immediately.

const defaultTextCueMaxWindows = 2048

type textCueKey struct {
	dir    string
	minute int64
}

type textCueWindow struct {
	cues     []subtitles.Cue
	seen     map[subtitles.Cue]bool
	gen      uint64
	queued   bool
	inFlight bool
}

// textCueStore buffers recently observed subtitle cues in memory and persists
// them to disk asynchronously on a single on-demand background goroutine.
// Its zero value is ready to use.
type textCueStore struct {
	mu      sync.Mutex
	windows map[textCueKey]*textCueWindow
	queue   []textCueKey
	running bool
	closed  bool
	done    chan struct{}
	maxWin  int
	writeFn func(dir string, minute int64, cues []subtitles.Cue) error
}

func (st *textCueStore) enqueue(dir string, minute int64, cues []subtitles.Cue, log *slog.Logger) {
	if len(cues) == 0 || dir == "" {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.windows == nil {
		st.windows = make(map[textCueKey]*textCueWindow)
	}
	key := textCueKey{dir: dir, minute: minute}
	w := st.windows[key]
	if w == nil {
		limit := st.maxWin
		if limit <= 0 {
			limit = defaultTextCueMaxWindows
		}
		for len(st.windows) >= limit {
			evicted := false
			for i, qk := range st.queue {
				if ew := st.windows[qk]; ew != nil && !ew.inFlight {
					copy(st.queue[i:], st.queue[i+1:])
					st.queue[len(st.queue)-1] = textCueKey{}
					st.queue = st.queue[:len(st.queue)-1]
					delete(st.windows, qk)
					evicted = true
					break
				}
			}
			if !evicted {
				for wk, ew := range st.windows {
					if !ew.inFlight {
						delete(st.windows, wk)
						evicted = true
						break
					}
				}
			}
			if !evicted {
				break
			}
		}
		w = &textCueWindow{seen: make(map[subtitles.Cue]bool, len(cues))}
		st.windows[key] = w
	}
	added := false
	for _, cue := range cues {
		if !w.seen[cue] {
			w.seen[cue] = true
			w.cues = append(w.cues, cue)
			added = true
		}
	}
	if !added {
		return
	}
	w.gen++
	if st.closed {
		return
	}
	if !w.queued && !w.inFlight {
		w.queued = true
		st.queue = append(st.queue, key)
	}
	if !st.running {
		st.running = true
		st.done = make(chan struct{})
		go st.run(log)
	}
}

func (st *textCueStore) run(log *slog.Logger) {
	for {
		st.mu.Lock()
		if st.closed || len(st.queue) == 0 {
			st.running = false
			done := st.done
			st.done = nil
			st.mu.Unlock()
			if done != nil {
				close(done)
			}
			return
		}
		key := st.queue[0]
		st.queue[0] = textCueKey{}
		st.queue = st.queue[1:]
		if len(st.queue) == 0 {
			st.queue = nil
		}
		w := st.windows[key]
		if w == nil {
			st.mu.Unlock()
			continue
		}
		w.queued = false
		w.inFlight = true
		snapGen := w.gen
		snapCues := append([]subtitles.Cue(nil), w.cues...)
		write := st.writeFn
		st.mu.Unlock()

		var err error
		if write != nil {
			err = write(key.dir, key.minute, snapCues)
		} else {
			err = persistTextCuesFile(key.dir, key.minute, snapCues)
		}
		if err != nil && log != nil {
			log.Warn("persist playback subtitle cues", "error", err)
		}

		st.mu.Lock()
		if cur := st.windows[key]; cur == w {
			w.inFlight = false
			if w.gen != snapGen {
				if !st.closed && !w.queued {
					w.queued = true
					st.queue = append(st.queue, key)
				}
			} else if err == nil {
				delete(st.windows, key)
			}
		}
		st.mu.Unlock()
	}
}

// readWindow checks in-memory pending cues before disk. Because run renames
// the persisted file on disk before removing the window from memory under mu,
// reading memory first then disk never misses a cue across the handoff.
func (st *textCueStore) readWindow(dir string, minute int64) []subtitles.Cue {
	st.mu.Lock()
	var mem []subtitles.Cue
	if w := st.windows[textCueKey{dir: dir, minute: minute}]; w != nil && len(w.cues) > 0 {
		mem = append([]subtitles.Cue(nil), w.cues...)
	}
	st.mu.Unlock()

	disk := readTextCues(filepath.Join(dir, fmt.Sprintf("%08d.json", minute)))
	if len(mem) == 0 {
		return disk
	}
	if len(disk) == 0 {
		return mem
	}
	seen := make(map[subtitles.Cue]bool, len(disk)+len(mem))
	out := make([]subtitles.Cue, 0, len(disk)+len(mem))
	for _, cue := range disk {
		if !seen[cue] {
			seen[cue] = true
			out = append(out, cue)
		}
	}
	for _, cue := range mem {
		if !seen[cue] {
			seen[cue] = true
			out = append(out, cue)
		}
	}
	return out
}

func (st *textCueStore) close() <-chan struct{} {
	st.mu.Lock()
	st.closed = true
	st.queue = nil
	done := st.done
	st.mu.Unlock()
	return done
}

type textSubtitleRead struct {
	offset  int64
	pending []byte
	format  string
}

func (s *Session) textSubtitleDir(index int) string {
	track := findStream(s.info, index, "subtitle")
	if s.textSubtitleSource == "" || track == nil || s.m == nil {
		return ""
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("v1\x00%s\x00%d\x00%d\x00%d\x00%s", s.textSubtitleSource, s.info.Size, s.info.Duration, index, track.Codec)))
	return filepath.Join(s.m.tempDir, "subtitle-cues", hex.EncodeToString(key[:]))
}

func (s *Session) collectTextSubtitles(r *run) {
	if len(r.spec.TextSubtitlePaths) == 0 {
		return
	}
	if r.textSubtitles == nil {
		r.textSubtitles = make(map[int]*textSubtitleRead)
	}
	for index, path := range r.spec.TextSubtitlePaths {
		state := r.textSubtitles[index]
		if state == nil {
			state = &textSubtitleRead{}
			r.textSubtitles[index] = state
		}
		st, err := os.Stat(path)
		if err != nil || st.Size() <= state.offset {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.NewSectionReader(file, state.offset, 1<<20))
		file.Close()
		if err != nil || len(data) == 0 {
			continue
		}
		state.offset += int64(len(data))
		state.pending = append(state.pending, data...)
		var cues []subtitles.Cue
		for {
			i := bytes.IndexByte(state.pending, '\n')
			if i < 0 {
				break
			}
			line := strings.TrimSpace(string(state.pending[:i]))
			state.pending = state.pending[i+1:]
			if strings.HasPrefix(line, "Format:") {
				state.format = line
			}
			if strings.HasPrefix(line, "Dialogue:") && state.format != "" {
				got, e := subtitles.ParseASS("[Events]\n" + state.format + "\n" + line + "\n")
				if e == nil {
					cues = append(cues, got...)
				}
			}
		}
		if len(state.pending) > 1<<20 {
			state.pending = nil
		}
		if len(cues) == 0 {
			continue
		}
		s.mu.Lock()
		dir := s.textSubtitleDir(index)
		s.mu.Unlock()
		if dir == "" {
			continue
		}
		windows := make(map[int64][]subtitles.Cue)
		for _, cue := range cues {
			for minute := int64(cue.Start / time.Minute); minute <= int64(cue.End/time.Minute) && minute <= int64(cue.Start/time.Minute)+10; minute++ {
				windows[minute] = append(windows[minute], cue)
			}
		}
		for minute, cues := range windows {
			s.m.enqueueTextCues(dir, minute, cues)
		}
	}
}

func (m *Manager) enqueueTextCues(dir string, minute int64, cues []subtitles.Cue) {
	m.textCues.enqueue(dir, minute, cues, m.log)
}

func (m *Manager) readTextCueWindow(dir string, minute int64) []subtitles.Cue {
	if m == nil {
		return readTextCues(filepath.Join(dir, fmt.Sprintf("%08d.json", minute)))
	}
	return m.textCues.readWindow(dir, minute)
}

func persistTextCuesFile(dir string, minute int64, cues []subtitles.Cue) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("%08d.json", minute))
	existing := readTextCues(path)
	seen := make(map[subtitles.Cue]bool, len(existing)+len(cues))
	for _, cue := range existing {
		seen[cue] = true
	}
	added := false
	for _, cue := range cues {
		if !seen[cue] {
			existing = append(existing, cue)
			seen[cue] = true
			added = true
		}
	}
	if !added && len(existing) > 0 {
		return nil
	}
	raw, err := json.Marshal(existing)
	if err != nil {
		return err
	}
	if len(raw) > 8<<20 {
		return fmt.Errorf("subtitle window too large")
	}
	temp, err := os.CreateTemp(dir, ".window-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(raw)
	closeErr := temp.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temp.Name(), path)
}

func readTextCues(path string) []subtitles.Cue {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	var cues []subtitles.Cue
	if json.NewDecoder(io.LimitReader(file, 8<<20)).Decode(&cues) != nil {
		return nil
	}
	return cues
}

// TextSubtitleCues reads a bounded window from memory and local disk, never the source.
func (s *Session) TextSubtitleCues(index int, from, to time.Duration) ([]subtitles.Cue, error) {
	s.mu.Lock()
	dir := s.textSubtitleDir(index)
	enabled := false
	for _, i := range s.textSubtitleStreams {
		if i == index {
			enabled = true
		}
	}
	s.mu.Unlock()
	if !enabled || dir == "" || from < 0 || to < from || to-from > 3*time.Minute {
		return nil, fmt.Errorf("subtitle track unavailable")
	}
	seen := make(map[subtitles.Cue]bool)
	var cues []subtitles.Cue
	for minute := int64(from / time.Minute); minute <= int64(to/time.Minute); minute++ {
		for _, cue := range s.m.readTextCueWindow(dir, minute) {
			if cue.End >= from && cue.Start <= to && !seen[cue] {
				cues = append(cues, cue)
				seen[cue] = true
			}
		}
	}
	return cues, nil
}
