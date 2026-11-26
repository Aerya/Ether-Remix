package adapters

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plugins"
)

const (
	lyricsDeadline = 6 * time.Second
	lyricsMemoMax  = 4096
	lyricsHitTTL   = 24 * time.Hour
	lyricsMissTTL  = 6 * time.Hour
)

type lyricsMemo struct {
	at     time.Time
	found  bool
	lyrics core.Lyrics
}

// lyricsMemos is a bounded memo of plugin lyrics answers, misses included,
// so reopening the lyrics never asks the plugin again within a day.
type lyricsMemos struct {
	mu sync.Mutex
	m  map[core.RatingKey]lyricsMemo
}

func (l *lyricsMemos) get(key core.RatingKey) (lyricsMemo, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	memo, ok := l.m[key]
	if !ok {
		return lyricsMemo{}, false
	}
	ttl := lyricsMissTTL
	if memo.found {
		ttl = lyricsHitTTL
	}
	if time.Since(memo.at) > ttl {
		delete(l.m, key)
		return lyricsMemo{}, false
	}
	return memo, true
}

func (l *lyricsMemos) put(key core.RatingKey, memo lyricsMemo) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[core.RatingKey]lyricsMemo{}
	}
	if len(l.m) >= lyricsMemoMax {
		var oldest core.RatingKey
		var at time.Time
		for k, v := range l.m {
			if at.IsZero() || v.at.Before(at) {
				oldest, at = k, v.at
			}
		}
		delete(l.m, oldest)
	}
	l.m[key] = memo
}

var kindLyrics lyricsMemos

// LyricsAvailability reads the memo for an item with a lyric declaration.
func (c *KindCatalog) LyricsAvailability(key core.RatingKey) frontend.LyricAvailability {
	memo, ok := kindLyrics.get(key)
	switch {
	case !ok:
		return frontend.LyricsPossible
	case !memo.found:
		return frontend.LyricsAbsent
	case len(memo.lyrics.Lines) > 0:
		return frontend.LyricsTimed
	default:
		return frontend.LyricsPlain
	}
}

// Lyrics asks the item's kind plugin for its words when the kind declares
// lyrics. The item comes from the store; the plugin is called at most once
// per item a day (misses remembered for six hours).
func (c *KindCatalog) Lyrics(ctx context.Context, key core.RatingKey) (core.Lyrics, error) {
	if c == nil || c.items == nil || c.mgr == nil || !key.IsPluginKey() {
		return core.Lyrics{}, core.ErrNotFound
	}
	if memo, ok := kindLyrics.get(key); ok {
		if !memo.found {
			return core.Lyrics{}, core.ErrNotFound
		}
		return memo.lyrics, nil
	}
	item, libID, err := c.items.GetByKey(ctx, key)
	if err != nil || !item.Playable {
		return core.Lyrics{}, core.ErrNotFound
	}
	rec, err := c.items.GetKindByLibrary(ctx, libID)
	if err != nil || !rec.Decl.Lyrics {
		return core.Lyrics{}, core.ErrNotFound
	}
	body, err := json.Marshal(map[string]any{"item": item})
	if err != nil {
		return core.Lyrics{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, lyricsDeadline)
	defer cancel()
	resp, err := c.mgr.Call(callCtx, rec.PluginID, plugins.Request{Method: http.MethodPost, Path: "/ether/v1/kinds/" + rec.KindKey + "/lyrics", Body: body})
	if err != nil {
		return core.Lyrics{}, err
	}
	if resp.Status == http.StatusNotFound {
		kindLyrics.put(key, lyricsMemo{at: time.Now()})
		return core.Lyrics{}, core.ErrNotFound
	}
	if resp.Status != http.StatusOK {
		return core.Lyrics{}, core.ErrNotAvailable
	}
	var answer struct {
		SyncedLyrics string           `json:"syncedLyrics"`
		PlainLyrics  string           `json:"plainLyrics"`
		Instrumental bool             `json:"instrumental"`
		Source       string           `json:"source"`
		Lines        []wireLyricsLine `json:"lines"`
	}
	if err := json.Unmarshal(resp.Body, &answer); err != nil {
		return core.Lyrics{}, core.ErrNotAvailable
	}
	lines := linesFromWire(answer.Lines)
	if len(lines) == 0 {
		lines = core.ParseLRC(answer.SyncedLyrics)
	}
	lyrics := core.Lyrics{Lines: lines, Plain: strings.TrimSpace(answer.PlainLyrics), Instrumental: answer.Instrumental, Source: strings.TrimSpace(answer.Source)}
	found := len(lyrics.Lines) > 0 || lyrics.Plain != "" || lyrics.Instrumental
	kindLyrics.put(key, lyricsMemo{at: time.Now(), found: found, lyrics: lyrics})
	if !found {
		return core.Lyrics{}, core.ErrNotFound
	}
	return lyrics, nil
}

// wireLyricsLine is a plugin's structured line: times in milliseconds,
// optional per-word timing, translation and romanization.
type wireLyricsLine struct {
	StartMs      int64           `json:"startMs"`
	EndMs        int64           `json:"endMs"`
	Text         string          `json:"text"`
	Words        []wireLyricWord `json:"words"`
	Translation  string          `json:"translation"`
	Romanization string          `json:"romanization"`
}

type wireLyricWord struct {
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Text    string `json:"text"`
}

// maxLyricLines bounds a plugin's structured lyrics.
const maxLyricLines = 2000

func linesFromWire(in []wireLyricsLine) []core.LyricLine {
	if len(in) > maxLyricLines {
		in = in[:maxLyricLines]
	}
	out := make([]core.LyricLine, 0, len(in))
	ms := func(v int64) time.Duration { return time.Duration(max(v, 0)) * time.Millisecond }
	for _, l := range in {
		text := strings.TrimSpace(l.Text)
		if text == "" && len(l.Words) == 0 {
			continue
		}
		line := core.LyricLine{Start: ms(l.StartMs), End: ms(l.EndMs), Text: text, Translation: strings.TrimSpace(l.Translation), Romanization: strings.TrimSpace(l.Romanization)}
		for _, w := range l.Words {
			if t := strings.TrimSpace(w.Text); t != "" {
				line.Words = append(line.Words, core.LyricWord{Start: ms(w.StartMs), End: ms(w.EndMs), Text: t})
			}
		}
		if line.Text == "" {
			parts := make([]string, len(line.Words))
			for i, w := range line.Words {
				parts[i] = w.Text
			}
			line.Text = strings.Join(parts, " ")
		}
		out = append(out, line)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}
