package plex

import (
	"context"
	"net/http"
	"sort"
	"strconv"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/reqmemo"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// TrackPreferences is optional. Selections are stream slice positions; saved
// show choices match language/flags so they survive changes in release order.
type TrackPreferences = frontend.PlaybackTrackPreferences
type MarkerProvider = frontend.MarkerRanges

func (s *Server) trackScope(ctx context.Context, key frontend.RatingKey) frontend.TrackScope {
	scope := frontend.TrackScope{Device: FromContext(ctx).ClientID}
	wanted := frontend.TypeMovie
	if key.Kind() == frontend.KindShow || key.Kind() == frontend.KindSeason || key.Kind() == frontend.KindEpisode {
		wanted = frontend.TypeShow
		scope.Series = strconv.FormatInt(int64(frontend.ShowKey(key.TMDB())), 10)
	}
	for _, section := range s.getSectionsCtx(ctx) {
		if section.Type == wanted {
			scope.Library = strconv.Itoa(section.ID)
			break
		}
	}
	return scope
}

func (s *Server) trackPreferences() frontend.TrackPreferenceSelector {
	return s.exp().NewTrackPreferenceSelector(frontend.TrackPreferenceSelectorOptions{Source: s.svc().trackPrefs, GetTitle: func(ctx context.Context, key frontend.RatingKey) (*frontend.Title, error) {
		if s.catalog() == nil {
			return nil, frontend.ErrNotAvailable
		}
		return &frontend.Title{Key: key, Language: s.showLanguage(ctx, key)}, nil
	}})
}
func (s *Server) preferredSelection(ctx context.Context, u *frontend.Viewer, key frontend.RatingKey, info frontend.MediaInfo, original string, sel selection) selection {
	if s.svc().trackPrefs == nil || u == nil {
		return sel
	}
	selected, err := s.trackPreferences().SelectScoped(ctx, u, key, info, original, s.trackScope(ctx, key), frontend.TrackSelection{Audio: sel.audio, AudioSet: sel.audioSet, Subtitle: sel.subtitle, SubtitleSet: sel.subtitleSet}, func(err error) { s.log.Warn("scoped stream preferences unavailable", "error", err) })
	if err != nil {
		s.log.Warn("stream preferences unavailable", "error", err)
		return sel
	}
	sel.audio, sel.audioSet = selected.Audio, selected.AudioSet
	sel.subtitle, sel.subtitleSet = selected.Subtitle, selected.SubtitleSet
	return sel
}
func (s *Server) applyStreamPreferences(ctx context.Context, pr *Request, p *playRequest, t *frontend.Title, info frontend.MediaInfo) {
	sel := selection{audio: p.audioSel, audioSet: p.audioSel >= 0, subtitle: p.subSel, subtitleSet: p.subSet}
	sel = s.preferredSelection(ctx, pr.User, p.key, info, t.Language, sel)
	p.audioSel = sel.audio
	p.subSel, p.subSet = sel.subtitle, sel.subtitleSet
	if s.svc().trackPrefs != nil && pr.User != nil {
		prefs := s.trackPreferences()
		if filters, err := prefs.AudioFilters(ctx, pr.User, info, frontend.TrackSelection{Audio: p.audioSel}); err == nil {
			p.opts.AudioFilters = filters
		}
		scope := s.trackScope(ctx, p.key)
		if pr.ClientID != "" {
			scope.Device = pr.ClientID
		}
		if opts, err := prefs.ScopedLimits(ctx, pr.User, scope, p.opts); err == nil {
			p.opts = opts
		}
	}
}
func (s *Server) rememberStreamRequest(r *http.Request, user *frontend.Viewer, key frontend.RatingKey) {
	if s.svc().trackPrefs == nil || user == nil {
		return
	}
	for _, kind := range []string{"audio", "subtitle"} {
		q := r.URL.Query()
		v := q.Get(kind + "StreamID")
		if !q.Has(kind + "StreamID") {
			continue
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			if kind == "audio" {
				continue
			}
			id = 0 // A malformed subtitle selector means None.
		}
		if id == -1 {
			if err := s.svc().trackPrefs.ForgetChoice(r.Context(), user.ID, key, kind); err != nil {
				s.log.Warn("reset stream preference", "error", err)
			}
			continue
		}
		var track *frontend.StreamInfo
		if kind == "subtitle" && id == 0 {
		} else {
			pr := FromContext(r.Context())
			binding, rerr := s.resolveIssuedStream(frontend.WithStreamServe(r.Context(), pr.Remote, ""), user, pr.ClientID, id)
			if rerr != nil {
				if !streamMiss(rerr) {
					return
				}
				continue
			}
			if binding.Key != key || binding.Info.Type != kind {
				continue
			}
			if kind == "audio" && binding.Estimated {
				// Pre-probe placeholder audio IDs carry no real track identity
				// and must not persist as a manual preference.
				continue
			}
			track = &binding.Info
		}
		// Timeline telemetry repeats the server-selected stream. Persisting
		// that echo turns a rule choice into a permanent manual veto. The
		// part-selection endpoint is an explicit command, even for the same
		// track, and Off (track == nil) must always be remembered.
		if kind == "subtitle" && r.URL.Path == "/:/timeline" && s.subtitleSelectionEcho(FromContext(r.Context()), key, track) {
			continue
		}
		if err = s.svc().trackPrefs.Remember(r.Context(), user.ID, key, kind, track); err != nil {
			s.log.Warn("save stream preference", "error", err)
		}
	}
}
func (s *Server) addStreamFeatures(ctx context.Context, pr *Request, t *frontend.Title, m *model.Metadata) {
	info, _ := s.mediaInfoFor(ctx, pr, t)
	s.addProbedStreamFeatures(ctx, t.Key, info, m)
}
func (s *Server) addProbedStreamFeatures(ctx context.Context, key frontend.RatingKey, info frontend.MediaInfo, m *model.Metadata) {
	if info.Estimated {
		return
	}
	if info.Duration > 0 {
		m.Duration = info.Duration.Milliseconds()
	}
	partID := int64(key)
	if len(m.Media) > 0 && len(m.Media[0].Part) > 0 {
		partID = m.Media[0].Part[0].ID
	}
	for _, media := range m.Media {
		if media.Selected != nil && *media.Selected && len(media.Part) > 0 {
			partID = media.Part[0].ID
			break
		}
	}
	for i, c := range info.Chapters {
		if c.Start < 0 || c.End <= c.Start || c.End > info.Duration {
			continue
		}
		m.Chapter = append(m.Chapter, model.Chapter{ID: tagID("chapter", strconv.FormatInt(partID, 10)+"/"+strconv.Itoa(i)+"/"+strconv.FormatInt(c.Start.Milliseconds(), 10)+"/"+strconv.FormatInt(c.End.Milliseconds(), 10)), Index: i + 1, Title: c.Title, StartTimeOffset: c.Start.Milliseconds(), EndTimeOffset: c.End.Milliseconds()})
	}
	if len(m.Chapter) > 0 {
		m.ChapterSource = "media"
	}
	ranges, err := s.exp().PlaybackMarkerRanges(ctx, s.markers, key, info)
	if err != nil {
		s.log.Debug("marker provider unavailable", "error", err)
	}

	// PMS lists markers in timeline order and puts final="1" only on the
	// last credits marker (a stinger scene splits credits into several).
	sort.SliceStable(ranges, func(i, j int) bool { return ranges[i].StartMS < ranges[j].StartMS })
	lastCredits := -1
	for i, v := range ranges {
		if v.Type == "credits" {
			lastCredits = i
		}
	}
	id := int64(0)
	for i, v := range ranges {
		if v.Type != "intro" && v.Type != "credits" {
			continue
		}
		id++
		m.Marker = append(m.Marker, model.Marker{ID: id, Type: v.Type, StartTimeOffset: v.StartMS, EndTimeOffset: v.EndMS, Final: model.Ptr(v.Final && i == lastCredits)})
	}
}

// SubtitleSource resolves cached OpenSubtitles and synchronized sidecars.
type SubtitleSource = frontend.SubtitleSidecarSource

func showLanguageMemoKey(show frontend.RatingKey) string {
	return "plex:show-language:" + strconv.FormatInt(int64(show), 10)
}

// showLanguage is a series' original language, the default an episode's
// track selection falls back to. A read request asks once per show.
func (s *Server) showLanguage(ctx context.Context, show frontend.RatingKey) string {
	lang, _ := reqmemo.Do(ctx, showLanguageMemoKey(show), func() (string, error) {
		if t, err := s.catalog().Get(ctx, show); err == nil && t != nil {
			return t.Language, nil
		}
		return "", nil
	})
	return lang
}

// resumePagePreloader is an optional playback manager hook that batch-loads
// resume records, probes, and blocklist checks for a list page.
type resumePagePreloader interface {
	PreloadResumes(context.Context, *frontend.User, []frontend.RatingKey) (context.Context, []frontend.RatingKey)
}

// preloadPageStreams preloads playback resumes, show languages, and remembered
// audio/subtitle choices for every title that will emit Stream elements on
// this render (detail items plus light list items with probed/resumed sources).
func (s *Server) preloadPageStreams(ctx context.Context, pr *Request, ts []frontend.Title, o renderOpts) context.Context {
	if pr == nil || pr.User == nil || !o.media {
		return ctx
	}
	var playable []frontend.RatingKey
	for i := range ts {
		if isPlayable(ts[i].Type) {
			playable = append(playable, ts[i].Key)
		}
	}
	var probedKeys []frontend.RatingKey
	if len(playable) > 0 && s.playback() != nil {
		if pre, ok := s.playback().(resumePagePreloader); ok {
			ctx, probedKeys = pre.PreloadResumes(ctx, pr.User, playable)
		}
		if !o.detail && !o.detailEpisodes {
			seen := make(map[frontend.RatingKey]bool, len(probedKeys))
			for _, k := range probedKeys {
				seen[k] = true
			}
			wp, _ := s.playback().(frontend.WarmProber)
			for i := range ts {
				if !isPlayable(ts[i].Type) || seen[ts[i].Key] {
					continue
				}
				if ps, ok := s.playback().SessionFor(pr.User.ID, pr.ClientID, ts[i].Key); ok && ps != nil && !ps.Info.Estimated && len(ps.Info.Streams) > 0 {
					seen[ts[i].Key] = true
					probedKeys = append(probedKeys, ts[i].Key)
					continue
				}
				if wp != nil {
					if _, warm := wp.WarmProbeInfo(pr.User, s.clientCapsForKey(pr, ts[i].Key), ts[i].Key); warm {
						seen[ts[i].Key] = true
						probedKeys = append(probedKeys, ts[i].Key)
					}
				}
			}
		}
	}
	s.preloadShowLanguages(ctx, pr, ts, o, probedKeys...)
	s.preloadTrackChoices(ctx, pr, ts, o, probedKeys...)
	return ctx
}

// preloadShowLanguages resolves, in one batch, the series of every episode
// whose streams this render selects without a language of its own. A list
// of episodes from distinct shows otherwise loaded each show on its own
// (On Deck: hundreds). Shows the batch cannot vouch for are left to Get.
func (s *Server) preloadShowLanguages(ctx context.Context, pr *Request, ts []frontend.Title, o renderOpts, probedKeys ...frontend.RatingKey) {
	if !reqmemo.Active(ctx) || s.svc().trackPrefs == nil || s.catalog() == nil || pr == nil || pr.User == nil || !o.media || (!o.detail && !o.detailEpisodes && len(probedKeys) == 0) {
		return
	}
	resolver, ok := s.catalog().(frontend.ShowParentResolver)
	if !ok {
		return
	}
	var allowed map[frontend.RatingKey]bool
	if !o.detail && !o.detailEpisodes {
		allowed = make(map[frontend.RatingKey]bool, len(probedKeys))
		for _, k := range probedKeys {
			allowed[k] = true
		}
	}
	seen := map[frontend.RatingKey]bool{}
	var shows []frontend.RatingKey
	for i := range ts {
		if ts[i].Type != frontend.TypeEpisode || ts[i].Language != "" || ts[i].Key.TMDB() <= 0 {
			continue
		}
		if allowed != nil && !allowed[ts[i].Key] {
			continue
		}
		show := frontend.ShowKey(ts[i].Key.TMDB())
		if !seen[show] {
			seen[show] = true
			shows = append(shows, show)
		}
	}
	if len(shows) < 2 {
		return
	}
	got, err := resolver.ResolveShowParents(ctx, shows)
	if err != nil {
		return
	}
	for key, t := range got {
		lang := t.Language
		_, _ = reqmemo.Do(ctx, showLanguageMemoKey(key), func() (string, error) { return lang, nil })
	}
}

// preloadTrackChoices keeps detailed or probed list rendering from reading
// each title's remembered audio and subtitle choices separately.
func (s *Server) preloadTrackChoices(ctx context.Context, pr *Request, ts []frontend.Title, o renderOpts, probedKeys ...frontend.RatingKey) {
	if s.svc().trackPrefs == nil || pr == nil || pr.User == nil || !o.media {
		return
	}
	var keys []frontend.RatingKey
	if o.detail || o.detailEpisodes {
		keys = make([]frontend.RatingKey, 0, len(ts))
		for _, t := range ts {
			keys = append(keys, t.Key)
		}
	} else if len(probedKeys) > 0 {
		keys = probedKeys
	}
	if len(keys) == 0 {
		return
	}
	if err := s.svc().trackPrefs.PreloadChoices(ctx, pr.User.ID, keys); err != nil {
		s.log.Warn("preload stream preferences", "err", err)
	}
}
