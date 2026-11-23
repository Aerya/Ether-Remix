package resolver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
)

// Viewer remembered version choices
//
// Extends the owner pin model (pins.go) with a viewer-scoped record stored
// alongside resolver_pins. When a viewer explicitly selects a version and
// plays past the real-play threshold, Ether records:
//   - a title-scoped choice (movie or episode) holding the concrete release
//     and optional slot selector;
//   - for episodes, also a show-scoped profile preference (resolution +
//     dynamic range + source class, e.g. "4K DV" or "1080p Remux") so the
//     next episode resolves to its matching slot automatically.
//
// Precedence in the Auto slot:
//   1. Viewer remembered choice (title, then show profile for episodes)
//   2. Owner pin
//   3. Owner quality rules
//
// Safety gates always apply: blocklisted, recently failed, rejected, or
// excluded releases are never forced, and an unmatched remembered choice
// falls back silently to the next tier.

func init() {
	store.Register("resolver", 11, `
CREATE TABLE resolver_viewer_choices (
  user_id INTEGER NOT NULL,
  rating_key INTEGER NOT NULL,
  scope TEXT NOT NULL DEFAULT 'title',
  selector TEXT NOT NULL DEFAULT '',
  release_id TEXT NOT NULL DEFAULT '',
  release_json TEXT NOT NULL DEFAULT '',
  profile_height INTEGER NOT NULL DEFAULT 0,
  profile_hdr TEXT NOT NULL DEFAULT '',
  profile_source TEXT NOT NULL DEFAULT '',
  chosen_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, rating_key)
);
CREATE INDEX resolver_viewer_choices_user ON resolver_viewer_choices(user_id);`)
	store.RegisterViewerTable("resolver", "resolver_viewer_choices", "user_id", store.ViewerMergeMove, "rating_key")
}

type viewerChoiceKey struct {
	userID int64
	key    core.RatingKey
}

// ViewerChoice is a viewer's remembered version choice for a title (movie or
// episode) or a show-level profile preference for episodes.
type ViewerChoice struct {
	UserID   int64               `json:"userId"`
	Key      core.RatingKey      `json:"key,string"`
	Scope    string              `json:"scope"` // "title" or "show"
	Selector string              `json:"selector,omitempty"`
	Release  core.Release        `json:"release,omitempty"`
	Profile  core.VersionProfile `json:"profile,omitempty"`
	ChosenAt time.Time           `json:"chosenAt"`
}

func viewerChoiceKeyAllowed(k core.RatingKey) bool {
	return pinnableKey(k) || k.IsPluginKey()
}

func showKeyForTitle(t *core.Title) core.RatingKey {
	if t == nil {
		return 0
	}
	if t.Key.Kind() == core.KindShow || (t.Key.IsPluginKey() && t.Type == core.TypeShow) {
		return t.Key
	}
	if t.ShowKey.Valid() {
		return t.ShowKey
	}
	if t.Key.Kind() == core.KindEpisode && t.Key.TMDB() > 0 {
		return core.ShowKey(t.Key.TMDB())
	}
	return 0
}

func canonicalHeightTier(height int) int {
	switch {
	case height >= 1800:
		return 2160
	case height >= 1000:
		return 1080
	case height >= 700:
		return 720
	case height >= 500:
		return 576
	case height > 0:
		return 480
	default:
		return 0
	}
}

// ProfileFromRelease derives a show-level matching profile (resolution tier +
// dynamic range + source class) from probed media facts when available, falling
// back to parsed release metadata.
func ProfileFromRelease(rel core.Release, probed *core.MediaInfo) core.VersionProfile {
	info := rel.Info
	if info.Height == 0 && info.Source == core.SourceUnknown && rel.Name != "" {
		info = releaseparse.Parse(rel.Name)
	}
	var p core.VersionProfile
	if probed != nil && !probed.Estimated {
		if v := probed.Video(); v != nil {
			if tier := core.ResolutionTier(v.Width, v.Height); tier > 0 {
				p.Height = tier
			}
			switch {
			case v.DolbyVision || v.DolbyVisionProfile > 0 || (v.ProbedDolbyVisionProfile != nil && *v.ProbedDolbyVisionProfile > 0):
				p.DynamicRange = "dv"
			case v.HDR10Plus != nil && *v.HDR10Plus:
				p.DynamicRange = "hdr10+"
			case v.ColorTransfer == "smpte2084":
				p.DynamicRange = "hdr10"
			case v.ColorTransfer == "arib-std-b67":
				p.DynamicRange = "hlg"
			default:
				p.DynamicRange = "sdr"
			}
		}
	}
	if p.Height == 0 && info.Height > 0 {
		p.Height = canonicalHeightTier(info.Height)
	}
	if p.DynamicRange == "" && (p.Height > 0 || info.DolbyVision || info.HDR10 || info.Source != core.SourceUnknown) {
		switch {
		case info.DolbyVision:
			p.DynamicRange = "dv"
		case info.HDR10:
			p.DynamicRange = "hdr10"
		default:
			p.DynamicRange = "sdr"
		}
	}
	if sc := sourceClass(info.Source); sc != "" && sc != "other" {
		p.SourceClass = sc
	}
	return p
}

// VersionProfileLabel renders a clean human label for a VersionProfile
// (e.g. "4K Dolby Vision Remux" or "1080p Remux").
func VersionProfileLabel(p core.VersionProfile) string {
	var parts []string
	switch {
	case p.Height >= 2160:
		parts = append(parts, "4K")
	case p.Height > 0:
		parts = append(parts, fmt.Sprintf("%dp", p.Height))
	}
	switch p.DynamicRange {
	case "dv":
		parts = append(parts, "Dolby Vision")
	case "hdr10+":
		parts = append(parts, "HDR10+")
	case "hdr10":
		parts = append(parts, "HDR10")
	case "hlg":
		parts = append(parts, "HLG")
	}
	switch p.SourceClass {
	case "remux":
		parts = append(parts, "Remux")
	case "disc":
		parts = append(parts, "Blu-ray")
	case "web":
		parts = append(parts, "WEB-DL")
	}
	return strings.Join(parts, " ")
}

func (s *Service) profileMatchesRelease(p core.VersionProfile, rel core.Release) bool {
	if p.Height == 0 && p.DynamicRange == "" && p.SourceClass == "" {
		return false
	}
	var probed *core.MediaInfo
	if info, ok := s.probedMediaFor(rel); ok && !info.Estimated {
		probed = &info
	}
	rp := ProfileFromRelease(rel, probed)
	if p.Height > 0 && rp.Height != p.Height {
		return false
	}
	if p.DynamicRange != "" && rp.DynamicRange != p.DynamicRange {
		// Treat HDR10+ as compatible with HDR10 when matching across episodes.
		if !((p.DynamicRange == "hdr10+" && rp.DynamicRange == "hdr10") || (p.DynamicRange == "hdr10" && rp.DynamicRange == "hdr10+")) {
			return false
		}
	}
	if p.SourceClass != "" && p.SourceClass != "other" && rp.SourceClass != p.SourceClass {
		return false
	}
	return true
}

// RecordViewerChoice stores a viewer's remembered choice for a title (and for
// episodes, also updates the viewer's show-level profile preference). Callers
// should invoke this only after an explicitly chosen non-Auto version crosses
// the real-play threshold.
func (s *Service) RecordViewerChoice(ctx context.Context, userID int64, title *core.Title, selector string, rel core.Release, probed *core.MediaInfo) error {
	if s == nil || userID <= 0 || title == nil || !viewerChoiceKeyAllowed(title.Key) || rel.ID == "" {
		return nil
	}
	if autoVersion(selector) {
		return nil
	}
	if s.isBadRelease(rel) || s.rejectedFor(title.Key, rel) {
		return nil
	}
	if rel.Info.Height == 0 && rel.Info.Source == core.SourceUnknown && rel.Name != "" {
		rel.Info = releaseparse.Parse(rel.Name)
	}
	// Only store non-auto logical slot selectors; concrete release ids live in
	// rel.ID so slots can be reused across episodes.
	storedSelector := ""
	if slot, ok := parseVersionSlot(selector); ok && slot.Kind != "auto" {
		storedSelector = selector
	}
	now := s.now()
	profile := ProfileFromRelease(rel, probed)
	titleChoice := ViewerChoice{
		UserID:   userID,
		Key:      title.Key,
		Scope:    "title",
		Selector: storedSelector,
		Release:  rel,
		Profile:  profile,
		ChosenAt: now,
	}
	choices := []ViewerChoice{titleChoice}
	if title.Key.Kind() == core.KindEpisode || (title.Key.IsPluginKey() && title.Type == core.TypeEpisode) {
		if showKey := showKeyForTitle(title); showKey.Valid() && showKey != title.Key && (profile.Height > 0 || profile.DynamicRange != "" || profile.SourceClass != "" || storedSelector != "") {
			choices = append(choices, ViewerChoice{
				UserID:   userID,
				Key:      showKey,
				Scope:    "show",
				Selector: storedSelector,
				Profile:  profile,
				ChosenAt: now,
			})
		}
	}
	for _, vc := range choices {
		if err := s.saveViewerChoice(ctx, vc); err != nil {
			return err
		}
	}
	s.InvalidateDetailVariants()
	return nil
}

func (s *Service) saveViewerChoice(ctx context.Context, vc ViewerChoice) error {
	if s.db != nil {
		raw := ""
		if vc.Scope == "title" && vc.Release.ID != "" {
			b, err := json.Marshal(vc.Release)
			if err != nil {
				return err
			}
			raw = string(b)
		}
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO resolver_viewer_choices(user_id, rating_key, scope, selector, release_id, release_json, profile_height, profile_hdr, profile_source, chosen_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?)
			 ON CONFLICT(user_id, rating_key) DO UPDATE SET
			   scope=excluded.scope,
			   selector=excluded.selector,
			   release_id=excluded.release_id,
			   release_json=excluded.release_json,
			   profile_height=excluded.profile_height,
			   profile_hdr=excluded.profile_hdr,
			   profile_source=excluded.profile_source,
			   chosen_at=excluded.chosen_at`,
			vc.UserID, int64(vc.Key), vc.Scope, vc.Selector, vc.Release.ID, raw, vc.Profile.Height, vc.Profile.DynamicRange, vc.Profile.SourceClass, vc.ChosenAt.Unix())
		if err != nil {
			return err
		}
	}
	s.con.mu.Lock()
	if s.con.viewerChoices == nil {
		s.con.viewerChoices = map[viewerChoiceKey]ViewerChoice{}
	}
	s.con.viewerChoices[viewerChoiceKey{userID: vc.UserID, key: vc.Key}] = vc
	s.con.mu.Unlock()
	s.notifyViewerChoice(vc.UserID, vc.Key)
	return nil
}

// ClearViewerChoice removes a viewer's remembered version choice for a title
// and/or its show. scope is "all" (default), "title", or "show".
func (s *Service) ClearViewerChoice(ctx context.Context, userID int64, title *core.Title, scope string) (bool, error) {
	if s == nil || userID <= 0 || title == nil {
		return false, nil
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		scope = "all"
	}
	var keys []core.RatingKey
	switch scope {
	case "all":
		if viewerChoiceKeyAllowed(title.Key) {
			keys = append(keys, title.Key)
		}
		if sk := showKeyForTitle(title); sk.Valid() && sk != title.Key {
			keys = append(keys, sk)
		}
	case "title":
		if viewerChoiceKeyAllowed(title.Key) {
			keys = append(keys, title.Key)
		}
	case "show":
		if sk := showKeyForTitle(title); sk.Valid() {
			keys = append(keys, sk)
		}
	default:
		return false, fmt.Errorf("resolver: invalid viewer choice scope %q", scope)
	}
	cleared := false
	for _, k := range keys {
		s.con.mu.Lock()
		_, had := s.con.viewerChoices[viewerChoiceKey{userID: userID, key: k}]
		delete(s.con.viewerChoices, viewerChoiceKey{userID: userID, key: k})
		s.con.mu.Unlock()
		if had {
			cleared = true
		}
		if s.db != nil {
			res, err := s.db.ExecContext(ctx, `DELETE FROM resolver_viewer_choices WHERE user_id=? AND rating_key=?`, userID, int64(k))
			if err != nil {
				return cleared, err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				cleared = true
			}
		}
		s.notifyViewerChoice(userID, k)
	}
	if cleared {
		s.InvalidateDetailVariants()
	}
	return cleared, nil
}

// ViewerChoiceFor returns the stored choice for (userID, key), if any.
func (s *Service) ViewerChoiceFor(userID int64, key core.RatingKey) (ViewerChoice, bool) {
	if s == nil || userID <= 0 || !key.Valid() {
		return ViewerChoice{}, false
	}
	s.con.mu.Lock()
	defer s.con.mu.Unlock()
	vc, ok := s.con.viewerChoices[viewerChoiceKey{userID: userID, key: key}]
	return vc, ok
}

// ViewerChoiceSummary returns the viewer's remembered choice metadata for a
// title (preferring a title-level choice, then a show-level episode choice).
func (s *Service) ViewerChoiceSummary(_ context.Context, viewer *core.Viewer, title *core.Title) (*core.ViewerChoiceSummary, error) {
	if s == nil || viewer == nil || viewer.ID <= 0 || title == nil {
		return nil, nil
	}
	if vc, ok := s.ViewerChoiceFor(viewer.ID, title.Key); ok && vc.Scope == "title" && vc.Release.ID != "" {
		active := !s.isBadRelease(vc.Release) && !s.recentlyFailedRelease(vc.Release) && !s.rejectedFor(title.Key, vc.Release)
		label := VersionProfileLabel(vc.Profile)
		if label == "" {
			label = qualityPhrase(vc.Release.Info)
		}
		if label == "" {
			label = strings.TrimSpace(vc.Release.Name)
		}
		return &core.ViewerChoiceSummary{
			Scope:     "title",
			Active:    active,
			Label:     label,
			Profile:   vc.Profile,
			Selector:  vc.Selector,
			ReleaseID: vc.Release.ID,
		}, nil
	}
	if title.Key.Kind() == core.KindEpisode || title.Key.Kind() == core.KindShow || (title.Key.IsPluginKey() && (title.Type == core.TypeEpisode || title.Type == core.TypeShow)) {
		if sk := showKeyForTitle(title); sk.Valid() {
			if vc, ok := s.ViewerChoiceFor(viewer.ID, sk); ok && vc.Scope == "show" {
				return &core.ViewerChoiceSummary{
					Scope:    "show",
					Active:   false, // caller sets true when a variant matched
					Label:    VersionProfileLabel(vc.Profile),
					Profile:  vc.Profile,
					Selector: vc.Selector,
				}, nil
			}
		}
	}
	return nil, nil
}

// MatchViewerChoice finds the first release in releases matching the viewer's
// remembered title choice, or (for episodes) remembered show profile. Returns
// (-1, "") when no remembered choice applies or matches.
func (s *Service) MatchViewerChoice(userID int64, title *core.Title, releases []core.Release) (int, string) {
	if s == nil || userID <= 0 || title == nil || len(releases) == 0 {
		return -1, ""
	}
	if vc, ok := s.ViewerChoiceFor(userID, title.Key); ok && vc.Scope == "title" && vc.Release.ID != "" {
		if !s.isBadRelease(vc.Release) && !s.recentlyFailedRelease(vc.Release) && !s.rejectedFor(title.Key, vc.Release) {
			for i, rel := range releases {
				if rel.ID == vc.Release.ID && !s.isBadRelease(rel) && !s.recentlyFailedRelease(rel) && !s.rejectedFor(title.Key, rel) {
					return i, "title"
				}
			}
		}
	}
	if title.Key.Kind() == core.KindEpisode || (title.Key.IsPluginKey() && title.Type == core.TypeEpisode) {
		if sk := showKeyForTitle(title); sk.Valid() {
			if vc, ok := s.ViewerChoiceFor(userID, sk); ok && vc.Scope == "show" {
				for i, rel := range releases {
					if s.isBadRelease(rel) || s.recentlyFailedRelease(rel) || s.rejectedFor(title.Key, rel) {
						continue
					}
					if s.profileMatchesRelease(vc.Profile, rel) {
						return i, "show"
					}
				}
			}
		}
	}
	return -1, ""
}

func (s *Service) viewerTitleChoiceForResolve(req core.ResolveRequest, exclude map[string]bool) *ViewerChoice {
	if s == nil || req.Title == nil || req.User == nil || req.User.ID <= 0 || req.Client.Download {
		return nil
	}
	if req.Prefer != "" && !autoVersion(req.Prefer) {
		return nil
	}
	vc, ok := s.ViewerChoiceFor(req.User.ID, req.Title.Key)
	if !ok || vc.Scope != "title" || vc.Release.ID == "" {
		return nil
	}
	if exclude[vc.Release.ID] || excludedByRelease(req.ExcludeReleases, vc.Release) || s.isBadRelease(vc.Release) || s.recentlyFailedRelease(vc.Release) || s.rejectedFor(req.Title.Key, vc.Release) {
		return nil
	}
	return &vc
}

func (s *Service) viewerShowChoiceForResolve(req core.ResolveRequest) *ViewerChoice {
	if s == nil || req.Title == nil || req.User == nil || req.User.ID <= 0 || req.Client.Download {
		return nil
	}
	if req.Title.Key.Kind() != core.KindEpisode && !(req.Title.Key.IsPluginKey() && req.Title.Type == core.TypeEpisode) {
		return nil
	}
	if req.Prefer != "" && !autoVersion(req.Prefer) {
		return nil
	}
	sk := showKeyForTitle(req.Title)
	if !sk.Valid() {
		return nil
	}
	vc, ok := s.ViewerChoiceFor(req.User.ID, sk)
	if !ok || vc.Scope != "show" {
		return nil
	}
	if vc.Profile.Height == 0 && vc.Profile.DynamicRange == "" && vc.Profile.SourceClass == "" {
		return nil
	}
	return &vc
}

// ActiveViewerChoiceRelease returns the concrete release ID for an active
// viewer title choice when one applies to req, else "". Used by playback so
// stale warm/resume entries do not shadow a newer remembered choice.
func (s *Service) ActiveViewerChoiceRelease(req core.ResolveRequest) string {
	if vc := s.viewerTitleChoiceForResolve(req, nil); vc != nil {
		return vc.Release.ID
	}
	return ""
}

// viewerChoiceFlightKey isolates single-flight resolutions between viewers who
// have different remembered title or show choices.
func (s *Service) viewerChoiceFlightKey(req core.ResolveRequest) string {
	if vc := s.viewerTitleChoiceForResolve(req, nil); vc != nil {
		return "t:" + vc.Release.ID
	}
	if vc := s.viewerShowChoiceForResolve(req); vc != nil {
		return fmt.Sprintf("s:%d:%s:%s:%s", vc.Profile.Height, vc.Profile.DynamicRange, vc.Profile.SourceClass, vc.Selector)
	}
	return ""
}

func (s *Service) matchShowChoiceCandidates(vc *ViewerChoice, cands []candidate) []candidate {
	if vc == nil || len(cands) == 0 {
		return nil
	}
	var out []candidate
	for _, c := range cands {
		if s.isBadRelease(c.rel) || s.recentlyFailedRelease(c.rel) {
			continue
		}
		if s.profileMatchesRelease(vc.Profile, c.rel) {
			out = append(out, c)
		}
	}
	return out
}

const viewerChoiceColumns = `user_id, rating_key, scope, selector, release_id, release_json, profile_height, profile_hdr, profile_source, chosen_at`

func scanViewerChoice(row interface{ Scan(...any) error }) (ViewerChoice, error) {
	var userID, key, chosenAt int64
	var scope, selector, relID, raw, hdr, src string
	var height int
	if err := row.Scan(&userID, &key, &scope, &selector, &relID, &raw, &height, &hdr, &src, &chosenAt); err != nil {
		return ViewerChoice{}, err
	}
	vc := ViewerChoice{
		UserID:   userID,
		Key:      core.RatingKey(key),
		Scope:    scope,
		Selector: selector,
		Profile:  core.VersionProfile{Height: height, DynamicRange: hdr, SourceClass: src},
		ChosenAt: time.Unix(chosenAt, 0),
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &vc.Release); err != nil {
			return ViewerChoice{}, fmt.Errorf("resolver viewer choice %d/%d unreadable: %v", userID, key, err)
		}
	}
	if vc.Release.ID == "" && relID != "" {
		vc.Release.ID = relID
	}
	return vc, nil
}

func (s *Service) loadViewerChoices(ctx context.Context) {
	if s.db == nil {
		return
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+viewerChoiceColumns+` FROM resolver_viewer_choices`)
	if err != nil {
		s.log.Warn("resolver viewer choices unavailable", "err", err)
		return
	}
	defer rows.Close()
	choices := map[viewerChoiceKey]ViewerChoice{}
	for rows.Next() {
		vc, err := scanViewerChoice(rows)
		if err != nil {
			s.log.Warn("resolver viewer choice unreadable", "err", err)
			continue
		}
		choices[viewerChoiceKey{userID: vc.UserID, key: vc.Key}] = vc
	}
	if rows.Err() != nil {
		return
	}
	s.con.mu.Lock()
	s.con.viewerChoices = choices
	s.con.mu.Unlock()
}

func (s *Service) reloadViewerChoice(ctx context.Context, arg string) {
	if s.db == nil {
		return
	}
	uStr, kStr, ok := strings.Cut(arg, ":")
	if !ok {
		return
	}
	userID, err1 := strconv.ParseInt(uStr, 10, 64)
	keyInt, err2 := strconv.ParseInt(kStr, 10, 64)
	if err1 != nil || err2 != nil || userID <= 0 || keyInt == 0 {
		return
	}
	key := core.RatingKey(keyInt)
	vc, err := scanViewerChoice(s.db.QueryRowContext(ctx,
		`SELECT `+viewerChoiceColumns+` FROM resolver_viewer_choices WHERE user_id=? AND rating_key=?`, userID, keyInt))
	s.con.mu.Lock()
	if s.con.viewerChoices == nil {
		s.con.viewerChoices = map[viewerChoiceKey]ViewerChoice{}
	}
	switch {
	case err == nil:
		s.con.viewerChoices[viewerChoiceKey{userID: userID, key: key}] = vc
	case errors.Is(err, sql.ErrNoRows):
		delete(s.con.viewerChoices, viewerChoiceKey{userID: userID, key: key})
	}
	s.con.mu.Unlock()
	s.InvalidateDetailVariants()
}

func (s *Service) notifyViewerChoice(userID int64, key core.RatingKey) {
	s.notify("vchoice:" + strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(int64(key), 10))
}

// SetLocalCacheLookup attaches a cheap O(1) local cache probe (e.g. local
// segment cache) used only to classify version availability as "instant".
func (s *Service) SetLocalCacheLookup(fn func(releaseID string) bool) {
	s.cfgMu.Lock()
	s.localCacheHas = fn
	s.cfgMu.Unlock()
}

func (s *Service) classifyVariantAvailability(ctx context.Context, titleKey core.RatingKey, rel core.Release, info core.MediaInfo, placeholder, searchWarm bool) (core.VersionAvailability, string) {
	if placeholder {
		if searchWarm {
			return core.VersionUnavailable, "No matching release found"
		}
		return core.VersionFetch, ""
	}
	if s.isBadRelease(rel) || s.recentlyFailedRelease(rel) || (titleKey.Valid() && s.rejectedFor(titleKey, rel)) {
		return core.VersionUnavailable, "Release is unavailable"
	}
	s.cfgMu.RLock()
	hasLocal := s.localCacheHas
	s.cfgMu.RUnlock()
	if hasLocal != nil && hasLocal(rel.ID) {
		return core.VersionInstant, ""
	}
	if s.knownCached(rel) || s.recentlyVerified(ctx, rel.ID) || (!info.Estimated && len(info.Streams) > 0) {
		return core.VersionCached, ""
	}
	if rel.Protocol == core.ProtocolTorrent {
		return core.VersionFetch, "May need to fetch before playback"
	}
	return core.VersionFetch, ""
}
