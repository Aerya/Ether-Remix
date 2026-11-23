package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/events"
)

// ActivePosterBadgeSet describes one enabled, non-shadow plugin's declared
// poster badge set for overlay rendering and console settings (#594).
type ActivePosterBadgeSet struct {
	PluginID    string `json:"pluginId"`
	PluginName  string `json:"pluginName"`
	Key         string `json:"key"`
	LayerKey    string `json:"layerKey"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

var imdbBadgeRe = regexp.MustCompile(`^tt[0-9]{1,12}$`)

// NormalizeBadgeLabel uppercases ASCII lowercase characters and validates that
// raw is 1..16 characters from [A-Z0-9 +&'./-] and not blank (#594).
func NormalizeBadgeLabel(raw string) (string, error) {
	if len(raw) < 1 || len(raw) > MaxBadgeLabelLen {
		return "", fmt.Errorf("must be 1 to %d characters", MaxBadgeLabelLen)
	}
	buf := make([]byte, len(raw))
	nonSpace := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c >= 'a' && c <= 'z' {
			c = c - 'a' + 'A'
		}
		switch {
		case c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '+', c == '&', c == '\'', c == '.', c == '/', c == '-':
			nonSpace = true
		case c == ' ':
		default:
			return "", fmt.Errorf("must contain only [A-Z0-9 +&'./-]")
		}
		buf[i] = c
	}
	if !nonSpace {
		return "", fmt.Errorf("must not be blank")
	}
	return string(buf), nil
}

// ExternalBadgeKeys returns the deterministic external-id fallback keys
// ("tmdb:<type>:<id>", "imdb:<type>:<id>") for a normalized BadgeTitleRef.
func ExternalBadgeKeys(ref BadgeTitleRef) (tmdbKey, imdbKey string) {
	if ref.TMDB > 0 && (ref.Type == "movie" || ref.Type == "show") {
		tmdbKey = fmt.Sprintf("tmdb:%s:%d", ref.Type, ref.TMDB)
	}
	if ref.IMDB != "" && (ref.Type == "movie" || ref.Type == "show") {
		imdbKey = fmt.Sprintf("imdb:%s:%s", ref.Type, strings.ToLower(strings.TrimSpace(ref.IMDB)))
	}
	return tmdbKey, imdbKey
}

func (m *Manager) badgeTitleQuota() int64 {
	m.mu.Lock()
	q := m.badgeQuotaTitles
	m.mu.Unlock()
	if q > 0 {
		return q
	}
	return PosterBadgeQuotaTitles
}

// SetBadgeQuotaForTest overrides the per-plugin badged-title quota (<=0 restores
// PosterBadgeQuotaTitles). Intended for unit tests.
func (m *Manager) SetBadgeQuotaForTest(quota int64) {
	m.mu.Lock()
	m.badgeQuotaTitles = quota
	m.mu.Unlock()
}

// SetResolveBadgeTitles wires the batched local catalogue lookup used by
// BadgesSet to map (type, tmdb, imdb) refs to canonical rating keys.
func (m *Manager) SetResolveBadgeTitles(fn func(ctx context.Context, refs []BadgeTitleRef) ([]string, error)) {
	m.mu.Lock()
	m.deps.Backends.ResolveBadgeTitles = fn
	m.mu.Unlock()
}

func (m *Manager) resolveBadgeTitlesFn() func(ctx context.Context, refs []BadgeTitleRef) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deps.Backends.ResolveBadgeTitles
}

func (m *Manager) buildPosterBadgeSets(ctx context.Context, r Record) []ActivePosterBadgeSet {
	if !r.Enabled || r.Shadow {
		return nil
	}
	man := r.Manifest
	man.Ether = man.EffectiveEther()
	if man.Ether == nil || !man.HasCapability(CapPosterBadges) {
		return nil
	}
	if g, err := m.store.grants(ctx, r.ID); err == nil && g.revoked(string(CapPosterBadges)) {
		return nil
	}
	decls := man.Ether.Contributes.PosterBadges
	if len(decls) == 0 {
		return nil
	}
	out := make([]ActivePosterBadgeSet, 0, len(decls))
	for _, pb := range decls {
		lbl := pb.Label
		if strings.HasPrefix(lbl, "$t:") {
			if resolved, ok := m.ResolveText(ctx, r.ID, strings.TrimPrefix(lbl, "$t:"), "en"); ok && resolved != "" {
				lbl = resolved
			} else {
				lbl = pb.Key
			}
		}
		desc := pb.Description
		if strings.HasPrefix(desc, "$t:") {
			if resolved, ok := m.ResolveText(ctx, r.ID, strings.TrimPrefix(desc, "$t:"), "en"); ok {
				desc = resolved
			} else {
				desc = ""
			}
		}
		out = append(out, ActivePosterBadgeSet{
			PluginID:    r.ID,
			PluginName:  r.Name,
			Key:         pb.Key,
			LayerKey:    "plugin:" + r.ID + ":" + pb.Key,
			Label:       lbl,
			Description: desc,
		})
	}
	return out
}

// syncPosterBadgeSets updates the in-memory active badge-set index when a
// plugin is installed, updated, enabled, disabled, shadowed or removed.
//
// Disabled and shadow plugins retain their stored rows in plugin_poster_badges
// and are filtered out at read time via ActivePosterBadgeSets, so re-enabling a
// plugin restores its badges immediately without drawing them while disabled.
func (m *Manager) syncPosterBadgeSets(ctx context.Context, r Record) {
	sets := m.buildPosterBadgeSets(ctx, r)
	m.mu.Lock()
	if m.posterBadgeSets == nil {
		m.posterBadgeSets = map[string][]ActivePosterBadgeSet{}
	}
	if len(sets) == 0 {
		delete(m.posterBadgeSets, r.ID)
	} else {
		m.posterBadgeSets[r.ID] = sets
	}
	m.mu.Unlock()
}

// ActivePosterBadgeSets returns declared poster badge sets for all currently
// enabled, non-shadow plugins in deterministic order without hitting SQLite
// once loaded.
func (m *Manager) ActivePosterBadgeSets(ctx context.Context) []ActivePosterBadgeSet {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	loaded := m.posterBadgesLoaded
	m.mu.Unlock()
	if !loaded {
		if recs, err := m.store.list(ctx); err == nil {
			built := make(map[string][]ActivePosterBadgeSet, len(recs))
			for _, r := range recs {
				if sets := m.buildPosterBadgeSets(ctx, r); len(sets) > 0 {
					built[r.ID] = sets
				}
			}
			m.mu.Lock()
			m.posterBadgeSets = built
			m.posterBadgesLoaded = true
			m.mu.Unlock()
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.posterBadgeSets) == 0 {
		return nil
	}
	ids := make([]string, 0, len(m.posterBadgeSets))
	for id := range m.posterBadgeSets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []ActivePosterBadgeSet
	for _, id := range ids {
		out = append(out, m.posterBadgeSets[id]...)
	}
	return out
}

func (h *host) badgeCheck(ctx context.Context, setKey string, needSet bool) (Record, PosterBadgeSetMeta, error) {
	r, g, err := h.record(ctx)
	if err != nil {
		return Record{}, PosterBadgeSetMeta{}, err
	}
	man := r.Manifest
	man.Ether = man.EffectiveEther()
	if g.revoked(string(CapPosterBadges)) || man.Ether == nil || !man.HasCapability(CapPosterBadges) {
		h.noteDenied(string(CapPosterBadges))
		return Record{}, PosterBadgeSetMeta{}, fmt.Errorf("%w: %s", ErrPermissionDenied, CapPosterBadges)
	}
	if !needSet {
		return r, PosterBadgeSetMeta{}, nil
	}
	if !posterBadgeKeyRe.MatchString(setKey) {
		return Record{}, PosterBadgeSetMeta{}, &ValidationError{Fields: []FieldError{{
			Field:   "setKey",
			Message: "must match ^[a-z][a-z0-9_]{0,31}$",
		}}}
	}
	for _, pb := range man.Ether.Contributes.PosterBadges {
		if pb.Key == setKey {
			return r, pb, nil
		}
	}
	return Record{}, PosterBadgeSetMeta{}, &ValidationError{Fields: []FieldError{{
		Field:   "setKey",
		Message: fmt.Sprintf("badge set %q is not declared in ether.contributes.posterBadges", setKey),
	}}}
}

type normalizedBadgeWrite struct {
	ref      BadgeTitleRef
	labels   []string
	titleKey string
	aliases  []string
}

// BadgesSet pushes or deletes (empty Labels) poster badges for up to 500
// canonical titles in one declared badge set (#594).
//
// Title resolution runs once per batch against the local catalogue
// (TMDB first, then IMDb). Known titles store title_key = ratingKey.String();
// titles not yet in catalog_titles store title_key = "tmdb:<type>:<id>" (or
// "imdb:<type>:<id>"), and overlays queries both canonical and external keys
// in its per-page batch so pre-pushed badges light up as soon as a title
// enters the catalogue without any ingest hook.
func (h *host) BadgesSet(ctx context.Context, setKey string, entries []BadgeEntry) error {
	if err := h.requireWritableToolContext(ctx); err != nil {
		return err
	}
	r, _, err := h.badgeCheck(ctx, setKey, true)
	if err != nil {
		return err
	}
	if len(entries) > MaxBadgeEntriesPerCall {
		return &ValidationError{Fields: []FieldError{{
			Field:   "entries",
			Message: fmt.Sprintf("must contain at most %d entries per call", MaxBadgeEntriesPerCall),
		}}}
	}
	if len(entries) == 0 {
		return nil
	}

	writes := make([]normalizedBadgeWrite, len(entries))
	refs := make([]BadgeTitleRef, len(entries))
	hasUpsert := false
	for i, e := range entries {
		base := fmt.Sprintf("entries[%d]", i)
		typ := strings.TrimSpace(e.Title.Type)
		if typ != "movie" && typ != "show" {
			return &ValidationError{Fields: []FieldError{{
				Field:   base + ".title.type",
				Message: "must be movie or show",
			}}}
		}
		if e.Title.TMDB < 0 {
			return &ValidationError{Fields: []FieldError{{
				Field:   base + ".title.tmdb",
				Message: "must be a positive TMDB id",
			}}}
		}
		imdb := strings.ToLower(strings.TrimSpace(e.Title.IMDB))
		if imdb != "" && !imdbBadgeRe.MatchString(imdb) {
			return &ValidationError{Fields: []FieldError{{
				Field:   base + ".title.imdb",
				Message: "must match tt<digits>",
			}}}
		}
		if e.Title.TMDB == 0 && imdb == "" {
			return &ValidationError{Fields: []FieldError{{
				Field:   base + ".title",
				Message: "tmdb or imdb is required",
			}}}
		}
		if len(e.Labels) > MaxBadgeLabelsPerTitle {
			return &ValidationError{Fields: []FieldError{{
				Field:   base + ".labels",
				Message: fmt.Sprintf("at most %d labels per title per set", MaxBadgeLabelsPerTitle),
			}}}
		}
		var cleanLabels []string
		if len(e.Labels) > 0 {
			seenLabel := make(map[string]bool, len(e.Labels))
			cleanLabels = make([]string, 0, len(e.Labels))
			for j, raw := range e.Labels {
				norm, lerr := NormalizeBadgeLabel(raw)
				if lerr != nil {
					return &ValidationError{Fields: []FieldError{{
						Field:   fmt.Sprintf("%s.labels[%d]", base, j),
						Message: lerr.Error(),
					}}}
				}
				if !seenLabel[norm] {
					seenLabel[norm] = true
					cleanLabels = append(cleanLabels, norm)
				}
			}
			hasUpsert = true
		}
		ref := BadgeTitleRef{Type: typ, TMDB: e.Title.TMDB, IMDB: imdb}
		refs[i] = ref
		writes[i] = normalizedBadgeWrite{ref: ref, labels: cleanLabels}
	}

	if r.Shadow {
		return h.journal(ctx, r, "badges.set", setKey, map[string]any{
			"op":      "badges.set",
			"setKey":  setKey,
			"entries": len(entries),
		})
	}

	var resolved []string
	if resolveFn := h.m.resolveBadgeTitlesFn(); resolveFn != nil {
		resolved, err = resolveFn(ctx, refs)
		if err != nil {
			return err
		}
	}
	for i := range writes {
		tmdbKey, imdbKey := ExternalBadgeKeys(writes[i].ref)
		var primary string
		if i < len(resolved) && resolved[i] != "" {
			primary = resolved[i]
		} else if tmdbKey != "" {
			primary = tmdbKey
		} else {
			primary = imdbKey
		}
		writes[i].titleKey = primary
		for _, candidate := range []string{primary, tmdbKey, imdbKey} {
			if candidate == "" {
				continue
			}
			dup := false
			for _, existing := range writes[i].aliases {
				if existing == candidate {
					dup = true
					break
				}
			}
			if !dup {
				writes[i].aliases = append(writes[i].aliases, candidate)
			}
		}
	}

	quota := h.m.badgeTitleQuota()
	err = h.m.store.badgesSet(ctx, h.plugin, setKey, writes, hasUpsert, quota)
	if errors.Is(err, ErrQuotaExceeded) {
		used, _ := h.m.store.badgesTitleCount(ctx, h.plugin)
		h.m.emitBusEvent(events.EventPluginQuotaExceeded, h.plugin, events.PluginQuotaExceededPayload{
			PluginID:  h.plugin,
			Name:      r.Name,
			QuotaKind: "badges",
			Used:      used,
			Limit:     quota,
		}, false)
	}
	return err
}

// BadgesClear removes every badge in one declared set for the calling plugin.
func (h *host) BadgesClear(ctx context.Context, setKey string) error {
	if err := h.requireWritableToolContext(ctx); err != nil {
		return err
	}
	r, _, err := h.badgeCheck(ctx, setKey, true)
	if err != nil {
		return err
	}
	if r.Shadow {
		return h.journal(ctx, r, "badges.clear", setKey, map[string]any{
			"op":     "badges.clear",
			"setKey": setKey,
		})
	}
	return h.m.store.badgesClear(ctx, h.plugin, setKey)
}

// BadgesUsage returns the calling plugin's distinct badged title count and quota.
func (h *host) BadgesUsage(ctx context.Context) (BadgeUsage, error) {
	if _, _, err := h.badgeCheck(ctx, "", false); err != nil {
		return BadgeUsage{}, err
	}
	titles, err := h.m.store.badgesTitleCount(ctx, h.plugin)
	if err != nil {
		return BadgeUsage{}, err
	}
	return BadgeUsage{
		Titles:      titles,
		QuotaTitles: h.m.badgeTitleQuota(),
	}, nil
}

func (s *storage) badgesSet(ctx context.Context, pluginID, setKey string, writes []normalizedBadgeWrite, hasUpsert bool, quota int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// Serialize concurrent badge writes for this plugin across connections so
	// the per-plugin distinct-title quota cannot be raced past.
	if _, err := tx.ExecContext(ctx, `UPDATE plugins_grants SET grants=grants WHERE plugin=?`, pluginID); err != nil {
		return err
	}

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM plugins_installed WHERE id=?`, pluginID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return ErrPermissionDenied
	} else if err != nil {
		return err
	}

	now := s.now().Unix()
	for _, w := range writes {
		if len(w.labels) == 0 {
			for _, k := range w.aliases {
				if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_poster_badges WHERE plugin_id=? AND set_key=? AND title_key=?`, pluginID, setKey, k); err != nil {
					return err
				}
			}
			continue
		}
		for _, k := range w.aliases {
			if k != w.titleKey {
				if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_poster_badges WHERE plugin_id=? AND set_key=? AND title_key=?`, pluginID, setKey, k); err != nil {
					return err
				}
			}
		}
		raw, err := json.Marshal(w.labels)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_poster_badges(plugin_id,set_key,title_key,labels_json,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(plugin_id,set_key,title_key) DO UPDATE SET labels_json=excluded.labels_json,updated_at=excluded.updated_at`,
			pluginID, setKey, w.titleKey, string(raw), now); err != nil {
			return err
		}
	}

	if hasUpsert {
		var titles int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT title_key) FROM plugin_poster_badges WHERE plugin_id=?`, pluginID).Scan(&titles); err != nil {
			return err
		}
		if titles > quota {
			return fmt.Errorf("%w: poster badges are limited to %d titles per plugin", ErrQuotaExceeded, quota)
		}
	}

	return tx.Commit()
}

func (s *storage) badgesClear(ctx context.Context, pluginID, setKey string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM plugin_poster_badges WHERE plugin_id=? AND set_key=?`, pluginID, setKey)
	return err
}

func (s *storage) badgesTitleCount(ctx context.Context, pluginID string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT title_key) FROM plugin_poster_badges WHERE plugin_id=?`, pluginID).Scan(&n)
	return n, err
}

// ValidateBadgeClearCall checks poster_badges capability and setKey against a
// manifest, for use by conformance and Studio sandboxes (#594).
func ValidateBadgeClearCall(man Manifest, setKey string) error {
	man.Ether = man.EffectiveEther()
	if man.Ether == nil || !man.HasCapability(CapPosterBadges) {
		return fmt.Errorf("%w: %s", ErrPermissionDenied, CapPosterBadges)
	}
	if !posterBadgeKeyRe.MatchString(setKey) {
		return &ValidationError{Fields: []FieldError{{Field: "setKey", Message: "must match ^[a-z][a-z0-9_]{0,31}$"}}}
	}
	for _, pb := range man.Ether.Contributes.PosterBadges {
		if pb.Key == setKey {
			return nil
		}
	}
	return &ValidationError{Fields: []FieldError{{
		Field:   "setKey",
		Message: fmt.Sprintf("badge set %q is not declared in ether.contributes.posterBadges", setKey),
	}}}
}

// ValidateBadgeSetCall validates setKey and entries against manifest and label
// bounds, returning normalized entries for sandbox hosts (#594).
func ValidateBadgeSetCall(man Manifest, setKey string, entries []BadgeEntry) ([]BadgeEntry, error) {
	if err := ValidateBadgeClearCall(man, setKey); err != nil {
		return nil, err
	}
	if len(entries) > MaxBadgeEntriesPerCall {
		return nil, &ValidationError{Fields: []FieldError{{
			Field:   "entries",
			Message: fmt.Sprintf("must contain at most %d entries per call", MaxBadgeEntriesPerCall),
		}}}
	}
	out := make([]BadgeEntry, len(entries))
	for i, e := range entries {
		base := fmt.Sprintf("entries[%d]", i)
		typ := strings.TrimSpace(e.Title.Type)
		if typ != "movie" && typ != "show" {
			return nil, &ValidationError{Fields: []FieldError{{Field: base + ".title.type", Message: "must be movie or show"}}}
		}
		if e.Title.TMDB < 0 {
			return nil, &ValidationError{Fields: []FieldError{{Field: base + ".title.tmdb", Message: "must be a positive TMDB id"}}}
		}
		imdb := strings.ToLower(strings.TrimSpace(e.Title.IMDB))
		if imdb != "" && !imdbBadgeRe.MatchString(imdb) {
			return nil, &ValidationError{Fields: []FieldError{{Field: base + ".title.imdb", Message: "must match tt<digits>"}}}
		}
		if e.Title.TMDB == 0 && imdb == "" {
			return nil, &ValidationError{Fields: []FieldError{{Field: base + ".title", Message: "tmdb or imdb is required"}}}
		}
		if len(e.Labels) > MaxBadgeLabelsPerTitle {
			return nil, &ValidationError{Fields: []FieldError{{Field: base + ".labels", Message: fmt.Sprintf("at most %d labels per title per set", MaxBadgeLabelsPerTitle)}}}
		}
		var clean []string
		if len(e.Labels) > 0 {
			seen := make(map[string]bool, len(e.Labels))
			for j, raw := range e.Labels {
				norm, lerr := NormalizeBadgeLabel(raw)
				if lerr != nil {
					return nil, &ValidationError{Fields: []FieldError{{Field: fmt.Sprintf("%s.labels[%d]", base, j), Message: lerr.Error()}}}
				}
				if !seen[norm] {
					seen[norm] = true
					clean = append(clean, norm)
				}
			}
		}
		out[i] = BadgeEntry{Title: BadgeTitleRef{Type: typ, TMDB: e.Title.TMDB, IMDB: imdb}, Labels: clean}
	}
	return out, nil
}
