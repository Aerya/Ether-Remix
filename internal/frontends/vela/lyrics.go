package vela

import (
	"context"
	"errors"
	"net/http"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func (i *instance) lyricsAvailable() bool {
	source, ok := i.host.Experience.(frontend.TitleLyrics)
	return ok && source.LyricsAvailable(context.Background())
}

// titleLyrics serves GET /vela/v0/titles/{id}/lyrics: timed lines
// ({startMs, text}) when the source has them, else plain text; 404 when the
// title has none.
func (i *instance) titleLyrics(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	source, ok := i.host.Experience.(frontend.TitleLyrics)
	if !ok {
		i.unavailable(w, r)
		return
	}
	k, err := key(r)
	if err != nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	lyrics, err := source.TitleLyrics(r.Context(), v, k)
	if errors.Is(err, frontend.ErrNotFound) {
		problem(w, 404, "not-found", "not_found")
		return
	}
	if err != nil {
		fail(w, err, r)
		return
	}
	lines := make([]object, len(lyrics.Lines))
	for n, line := range lyrics.Lines {
		wire := object{"startMs": line.Start.Milliseconds(), "text": line.Text}
		if line.End > line.Start {
			wire["endMs"] = line.End.Milliseconds()
		}
		if len(line.Words) > 0 {
			words := make([]object, len(line.Words))
			for k, w := range line.Words {
				word := object{"startMs": w.Start.Milliseconds(), "text": w.Text}
				if w.End > w.Start {
					word["endMs"] = w.End.Milliseconds()
				}
				words[k] = word
			}
			wire["words"] = words
		}
		if line.Translation != "" {
			wire["translation"] = line.Translation
		}
		if line.Romanization != "" {
			wire["romanization"] = line.Romanization
		}
		lines[n] = wire
	}
	out := object{"lines": lines, "instrumental": lyrics.Instrumental}
	if lyrics.Plain != "" {
		out["plain"] = lyrics.Plain
	}
	if lyrics.Source != "" {
		out["source"] = lyrics.Source
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	send(w, 200, out)
}
