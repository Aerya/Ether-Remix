package core

import (
	"sync"
	"time"

	"log/slog"
)

// A person opens a handful of detail pages a minute. A library sync
// (Tracearr walking every season and episode, a metadata scraper) fetches
// hundreds. Detail warm-ups and the searches behind them are speculative
// work for a person about to press Play, so a client that reads detail
// pages faster than any person could gets none: no resolve, no indexer
// search. Playback itself is never affected.
const (
	detailBurstWindow   = time.Minute
	detailBurstDistinct = 8
)

type detailBurstKey struct {
	userID   int64
	clientID string
}

type detailBurstState struct {
	since time.Time
	keys  map[RatingKey]struct{}
	held  bool
}

type detailBursts struct {
	mu sync.Mutex
	by map[detailBurstKey]*detailBurstState
}

var bursts detailBursts

// DetailBurst records a detail read of key by this client and reports
// whether the client is reading detail pages like a crawler. Repeat reads
// of the same title do not count.
func DetailBurst(userID int64, clientID string, key RatingKey) bool {
	return bursts.note(time.Now(), userID, clientID, key)
}

func (b *detailBursts) note(now time.Time, userID int64, clientID string, key RatingKey) bool {
	if clientID == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.by == nil {
		b.by = map[detailBurstKey]*detailBurstState{}
	}
	for k, st := range b.by {
		if now.Sub(st.since) > 2*detailBurstWindow {
			delete(b.by, k)
		}
	}
	id := detailBurstKey{userID, clientID}
	st := b.by[id]
	if st == nil || now.Sub(st.since) > detailBurstWindow {
		// A client that was crawling stays held for one more window, so a
		// sync paced just under the limit cannot reset it every minute.
		held := st != nil && len(st.keys) > detailBurstDistinct
		st = &detailBurstState{since: now, keys: map[RatingKey]struct{}{}, held: held}
		b.by[id] = st
	}
	st.keys[key] = struct{}{}
	crawling := st.held || len(st.keys) > detailBurstDistinct
	if crawling && len(st.keys) == detailBurstDistinct+1 && !st.held {
		slog.Info("client is reading detail pages like a library sync; skipping detail warm-ups for it",
			"user", userID, "client", clientID, "distinct_titles", len(st.keys), "window", detailBurstWindow)
	}
	return crawling
}
