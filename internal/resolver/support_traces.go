package resolver

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// maxSupportTraces bounds the resolutions one support capture reads.
const maxSupportTraces = 200

// SupportTrace is one resolution a support capture explains. Earlier is the
// last resolution of the same title that actually searched, when this one
// only repeated a remembered "not available" answer.
type SupportTrace struct {
	Trace   Trace
	Earlier *Trace
}

// SupportTraces returns the resolutions made for clientID (or, for traces
// without a client, userID) between from and until, oldest first. It reads
// the in-memory recent traces first, since the history write is queued, and
// then the persisted history.
func (s *Service) SupportTraces(ctx context.Context, from, until time.Time, userID int64, clientID string) ([]SupportTrace, error) {
	if userID == 0 && clientID == "" {
		return nil, nil
	}
	mine := func(tr Trace) bool {
		if tr.At.Before(from) || tr.At.After(until) {
			return false
		}
		if tr.ClientID != "" && clientID != "" {
			return tr.ClientID == clientID
		}
		return userID != 0 && tr.UserID == userID
	}
	type traceID struct {
		at  int64
		key core.RatingKey
	}
	seen := map[traceID]bool{}
	var out []Trace
	add := func(tr Trace) {
		id := traceID{tr.At.UnixNano(), tr.TitleKey}
		if seen[id] || !mine(tr) {
			return
		}
		seen[id] = true
		out = append(out, tr)
	}
	s.tracesMu.Lock()
	for _, tr := range s.traces {
		add(tr)
	}
	s.tracesMu.Unlock()
	if s.db == nil {
		s.con.mu.Lock()
		for _, h := range s.con.history {
			if h.Trace != nil {
				add(*h.Trace)
			}
		}
		s.con.mu.Unlock()
	} else {
		rows, err := s.db.QueryContext(ctx, `SELECT trace FROM resolver_history WHERE resolved_at>=? AND resolved_at<=? ORDER BY id DESC LIMIT ?`,
			from.Unix(), until.Unix()+1, maxSupportTraces*4)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			var tr Trace
			if json.Unmarshal([]byte(raw), &tr) == nil {
				add(tr)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	if len(out) > maxSupportTraces {
		out = out[len(out)-maxSupportTraces:]
	}
	res := make([]SupportTrace, 0, len(out))
	earlier := map[core.RatingKey]*Trace{}
	for _, tr := range out {
		st := SupportTrace{Trace: tr}
		if rememberedOnly(tr) {
			prev, ok := earlier[tr.TitleKey]
			if !ok {
				prev = s.lastSearchedTrace(ctx, tr.TitleKey, tr.At)
				earlier[tr.TitleKey] = prev
			}
			st.Earlier = prev
		}
		res = append(res, st)
	}
	return res, nil
}

// rememberedOnly reports a resolution that answered from remembered state
// without asking any indexer or ranking any release.
func rememberedOnly(tr Trace) bool {
	return len(tr.Candidates) == 0 && len(tr.Queries) == 0 && tr.Winner == "" &&
		(strings.Contains(tr.Err, "cooloff") || len(tr.Discovery) == 0)
}

// lastSearchedTrace is the newest resolution of key before at that ranked
// releases or asked an indexer.
func (s *Service) lastSearchedTrace(ctx context.Context, key core.RatingKey, at time.Time) *Trace {
	searched := func(tr Trace) bool { return len(tr.Candidates) > 0 || len(tr.Queries) > 0 || len(tr.Discovery) > 0 }
	var best *Trace
	consider := func(tr Trace) {
		if tr.TitleKey != key || !tr.At.Before(at) || !searched(tr) {
			return
		}
		if best == nil || tr.At.After(best.At) {
			c := tr
			best = &c
		}
	}
	s.tracesMu.Lock()
	for _, tr := range s.traces {
		consider(tr)
	}
	s.tracesMu.Unlock()
	if s.db == nil {
		s.con.mu.Lock()
		for _, h := range s.con.history {
			if h.Trace != nil {
				consider(*h.Trace)
			}
		}
		s.con.mu.Unlock()
		return best
	}
	rows, err := s.db.QueryContext(ctx, `SELECT trace FROM resolver_history WHERE rating_key=? AND resolved_at<=? ORDER BY resolved_at DESC, id DESC LIMIT 20`,
		int64(key), at.Unix())
	if err != nil {
		return best
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			return best
		}
		var tr Trace
		if json.Unmarshal([]byte(raw), &tr) == nil {
			consider(tr)
		}
	}
	return best
}
