package silo

import (
	"net/http"
	"strconv"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// trackPrefRoutes serves the app's per-series track preferences
// (/api/v2/{audio,subtitle}-prefs/{series_id}). The player writes one on
// every explicit audio or subtitle pick; the routes were missing, so every
// pick answered 404 and was forgotten when the player closed. Writes land in
// the same series scope the Settings page uses.
func (i *instance) trackPrefRoutes() error {
	for _, kind := range []string{"audio", "subtitle"} {
		kind := kind
		path := "/api/v2/" + kind + "-prefs/{series_id}"
		if err := i.router.Handle("GET", path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if v, _, err := i.viewer(r); err != nil || v == nil {
				http.Error(w, "Unauthorized", 401)
				return
			}
			// The scoped store answers merged values, not whether this
			// series has a row of its own, so report none: the app then
			// writes its pick as given.
			notFound(w)
		})); err != nil {
			return err
		}
		for _, method := range []string{"PUT", "DELETE"} {
			method := method
			if err := i.router.Handle(method, path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				i.trackPref(w, r, kind, method == "DELETE")
			})); err != nil {
				return err
			}
		}
	}
	return nil
}

func (i *instance) trackPref(w http.ResponseWriter, r *http.Request, kind string, remove bool) {
	v, _, err := i.viewer(r)
	if err != nil || v == nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	svc, ok := i.host.TrackPreferences.(frontend.ScopedTrackPreferenceService)
	if !ok {
		http.Error(w, "Unavailable", 503)
		return
	}
	key, err := ids.ParseRatingKeyGUID(r.PathValue("series_id"))
	if err != nil {
		notFound(w)
		return
	}
	id := strconv.FormatInt(int64(key), 10)
	if remove {
		fields := []string{"audio"}
		if kind == "subtitle" {
			fields = []string{"subtitle", "subtitle_mode"}
		}
		for _, field := range fields {
			if svc.DeleteScoped(r.Context(), v, "series", id, field) != nil {
				http.Error(w, "Unavailable", 503)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var body struct {
		AudioLanguage    string `json:"audio_language"`
		SubtitleLanguage string `json:"subtitle_language"`
		SubtitleMode     string `json:"subtitle_mode"`
	}
	if decode(r, &body) != nil {
		http.Error(w, "Bad Request", 400)
		return
	}
	var patch frontend.TrackPreferencePatch
	if kind == "audio" {
		if body.AudioLanguage != "" {
			langs := []string{body.AudioLanguage}
			patch.AudioLanguages = &langs
		}
	} else {
		if body.SubtitleLanguage != "" {
			langs := []string{body.SubtitleLanguage}
			patch.SubtitleLanguages = &langs
		}
		switch body.SubtitleMode {
		case "auto", "always", "off":
			mode := body.SubtitleMode
			patch.SubtitleMode = &mode
		}
	}
	if patch.AudioLanguages == nil && patch.SubtitleLanguages == nil && patch.SubtitleMode == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if svc.SetScoped(r.Context(), v, "series", id, patch) != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
