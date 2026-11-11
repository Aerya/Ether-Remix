package submaker

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/langtag"
)

// vostfrRow stores the "VOSTFR on demand" switch beside the scopes (row 1).
const vostfrRow = 2

// VostfrHooks connect SubMaker to the titles chosen in VOSTFR on demand.
type VostfrHooks struct {
	// Keys lists the chosen movies and shows.
	Keys func(context.Context) []core.RatingKey
	// Languages returns the audio and subtitle languages announced by the
	// indexed releases of a movie or show, and how many releases there are.
	Languages func(ctx context.Context, key core.RatingKey) (releases int, audio, subtitles []string)
	// Search queues a priority release search; SubMaker prepares the results.
	Search func(context.Context, []core.RatingKey) (int, error)
}

// Subtitle states of a VOSTFR title, from the most to the least useful.
const (
	VostfrFrenchAudio = "french-audio" // a release already has French audio
	VostfrAnnounced   = "announced"    // a release announces French subtitles
	VostfrReady       = "ready"        // SubMaker found a French SRT
	VostfrTranslated  = "translated"   // SubMaker translated an SRT (AI)
	VostfrPending     = "pending"      // SubMaker is preparing one
	VostfrFailed      = "failed"       // the last attempt failed; retried later
	VostfrNone        = "none"         // releases, but no French subtitles yet
	VostfrUnindexed   = "unindexed"    // no release indexed yet
)

// VostfrTitle is the subtitle state of one chosen title.
type VostfrTitle struct {
	Key   string `json:"key"`
	State string `json:"state"`
}

// Vostfr reports whether the VOSTFR titles are in SubMaker's scope.
func (s *Service) Vostfr() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vostfr
}

// SetVostfr stores the switch.
func (s *Service) SetVostfr(ctx context.Context, on bool) error {
	value := "off"
	if on {
		value = "on"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO remix_submaker_scopes(id,value) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value", vostfrRow, value); err != nil {
		return err
	}
	s.vostfr = on
	return nil
}

// Ready reports whether SubMaker is enabled with a usable manifest.
func Ready() bool {
	c := Load()
	if !c.Enabled {
		return false
	}
	_, _, err := Endpoint(c.Manifest)
	return err == nil
}

func (s *Service) vostfrKeys(ctx context.Context) []core.RatingKey {
	if s.VOSTFR.Keys == nil {
		return nil
	}
	return s.VOSTFR.Keys(ctx)
}

// isVostfr reports whether a movie or show key is a chosen VOSTFR title.
func (s *Service) isVostfr(ctx context.Context, key core.RatingKey) bool {
	for _, k := range s.vostfrKeys(ctx) {
		if k == key {
			return true
		}
	}
	return false
}

// titleOf maps a job's key (movie or episode) onto its movie or show key.
func titleOf(k core.RatingKey) core.RatingKey {
	if k.Kind() == core.KindEpisode {
		return core.ShowKey(k.TMDB())
	}
	return k
}

// VostfrStates returns the subtitle state of every chosen title.
func (s *Service) VostfrStates(ctx context.Context) []VostfrTitle {
	keys := s.vostfrKeys(ctx)
	out := make([]VostfrTitle, 0, len(keys))
	if len(keys) == 0 {
		return out
	}
	jobs := map[core.RatingKey]string{}
	rank := map[string]int{VostfrReady: 4, VostfrTranslated: 4, VostfrPending: 3, VostfrFailed: 2}
	rows, err := s.DB.QueryContext(ctx, "SELECT title_key,state,translated FROM remix_submaker_jobs ORDER BY updated DESC")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var key int64
			var state string
			var translated bool
			if rows.Scan(&key, &state, &translated) != nil {
				continue
			}
			switch state {
			case "ready":
				state = VostfrReady
				if translated {
					state = VostfrTranslated
				}
			case "queued", "running":
				state = VostfrPending
			default:
				state = VostfrFailed
			}
			title := titleOf(core.RatingKey(key))
			if rank[state] > rank[jobs[title]] {
				jobs[title] = state
			}
		}
	}
	target := Load().Target
	french := func(langs []string) bool {
		for _, l := range langs {
			if langtag.MatchTier(target, l) > 0 {
				return true
			}
		}
		return false
	}
	for _, k := range keys {
		state := jobs[k]
		releases, audio, subs := 0, []string(nil), []string(nil)
		if s.VOSTFR.Languages != nil {
			releases, audio, subs = s.VOSTFR.Languages(ctx, k)
		}
		switch {
		case french(audio):
			state = VostfrFrenchAudio
		case french(subs):
			state = VostfrAnnounced
		case state != "":
		case releases == 0:
			state = VostfrUnindexed
		default:
			state = VostfrNone
		}
		out = append(out, VostfrTitle{Key: k.String(), State: state})
	}
	return out
}

func (s *Service) vostfrHandler(reply func(http.ResponseWriter, int, any)) (get, put http.HandlerFunc) {
	view := func(ctx context.Context) map[string]any {
		return map[string]any{"available": Ready(), "enabled": s.Vostfr(), "titles": s.VostfrStates(ctx)}
	}
	get = func(w http.ResponseWriter, r *http.Request) { reply(w, 200, view(r.Context())) }
	put = func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body) != nil {
			reply(w, 400, map[string]string{"error": "Invalid body"})
			return
		}
		if body.Enabled && !Ready() {
			reply(w, 409, map[string]string{"error": "Enable SubMaker with a valid manifest first"})
			return
		}
		if err := s.SetVostfr(r.Context(), body.Enabled); err != nil {
			reply(w, 500, map[string]string{"error": "Could not save"})
			return
		}
		out := view(r.Context())
		if body.Enabled && s.VOSTFR.Search != nil {
			// Titles indexed before the switch get a new search, whose
			// results SubMaker then prepares within its daily budget.
			var missing []core.RatingKey
			for _, t := range out["titles"].([]VostfrTitle) {
				if t.State == VostfrNone || t.State == VostfrUnindexed || t.State == VostfrFailed {
					if k, err := core.ParseRatingKey(t.Key); err == nil {
						missing = append(missing, k)
					}
				}
			}
			if len(missing) > 0 {
				n, _ := s.VOSTFR.Search(r.Context(), missing)
				out["queued"] = n
			}
		}
		reply(w, 200, out)
	}
	return get, put
}
