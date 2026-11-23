package plexguid

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/braedonsaunders/ether/internal/core"
)

const (
	maxBoundedCreditFlights = 8
	creditFailCooldown      = 5 * time.Minute
	maxCreditFails          = 2048
	maxRecordedPersons      = 8192
)

type creditFlight struct {
	done chan struct{}
	out  TitleCredits
	ok   bool
}

type recordedPerson struct {
	tmdb  int64
	name  string
	thumb string
}

// Credit is one person record returned by metadata.provider.plex.tv for a
// verified title. TagKey is always a validated 24-hex plex://person id when
// non-empty; Thumb is always a sanitized Plex CDN HTTPS URL when non-empty.
type Credit struct {
	TagKey string `json:"k"`
	Name   string `json:"n"`
	Role   string `json:"r,omitempty"`
	Thumb  string `json:"t,omitempty"`
	TMDB   int64  `json:"m,omitempty"`
}

// TitleCredits is the durably cached provider credit payload for one title,
// paired with the provider slug and Common Sense Media age rating that arrive
// on the same /library/metadata/{hex} response.
type TitleCredits struct {
	Slug             string   `json:"slug,omitempty"`
	ContentRatingAge int      `json:"age,omitempty"`
	Director         []Credit `json:"d,omitempty"`
	Writer           []Credit `json:"w,omitempty"`
	Role             []Credit `json:"r,omitempty"`
	Producer         []Credit `json:"p,omitempty"`
}

// PersonMapping links a real 24-hex Plex person tagKey to Ether's TMDB person
// id so /library/people/{tagKey} and /library/people/{tagKey}/media resolve
// without scanning titles.
type PersonMapping struct {
	TagKey     string
	PersonTMDB int64
	Name       string
	Thumb      string
}

// IsPersonTagKey reports whether s is a real 24-lowercase-hex Plex person id
// (such as "5d77682654f42c001f8c2650"). Fabricated ids like "tmdb-person-31"
// are always rejected (#653).
func IsPersonTagKey(s string) bool {
	if len(s) != 24 {
		return false
	}
	for i := 0; i < 24; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func validCreditsGUID(guid string) (kind, hex string, ok bool) {
	for _, k := range []string{"movie", "show", "episode"} {
		if h := guidHex(guid, k); h != "" {
			return k, h, true
		}
	}
	return "", "", false
}

func sanitizePlexThumb(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Path == "" || u.Path == "/" {
		return ""
	}
	switch strings.ToLower(u.Hostname()) {
	case "metadata-static.plex.tv", "image.plex.tv", "images.plex.tv", "metadata.provider.plex.tv":
		return raw
	}
	return ""
}

func extractPersonTagKey(p providerPerson) string {
	for _, candidate := range []string{
		p.TagKey,
		p.ID,
		strings.TrimPrefix(strings.TrimSpace(p.Key), "/library/people/"),
		strings.TrimPrefix(strings.TrimSpace(p.GUID), "plex://person/"),
	} {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if IsPersonTagKey(candidate) {
			return candidate
		}
	}
	return ""
}

func extractPersonTMDB(p providerPerson) int64 {
	for _, g := range p.IDs {
		id := strings.TrimSpace(g.ID)
		for _, prefix := range []string{"tmdb://", "themoviedb://"} {
			if rest, ok := strings.CutPrefix(id, prefix); ok {
				if n, err := strconv.ParseInt(rest, 10, 64); err == nil && n > 0 {
					return n
				}
			}
		}
	}
	return 0
}

func extractContentRatingAge(it providerItem) int {
	if it.ContentRatingAge > 0 {
		return it.ContentRatingAge
	}
	fallback := 0
	for _, csm := range it.CommonSenseMedia {
		for _, ar := range csm.AgeRating {
			if ar.Age <= 0 {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(ar.Type), "official") {
				return ar.Age
			}
			if fallback == 0 {
				fallback = ar.Age
			}
		}
	}
	return fallback
}

func creditsFromPeople(in []providerPerson) []Credit {
	if len(in) == 0 {
		return nil
	}
	out := make([]Credit, 0, len(in))
	for _, p := range in {
		name := strings.TrimSpace(p.Tag)
		tagKey := extractPersonTagKey(p)
		if name == "" || tagKey == "" {
			continue
		}
		out = append(out, Credit{
			TagKey: tagKey,
			Name:   name,
			Role:   strings.TrimSpace(p.Role),
			Thumb:  sanitizePlexThumb(p.Thumb),
			TMDB:   extractPersonTMDB(p),
		})
	}
	return out
}

func creditsFromProviderItem(it providerItem) TitleCredits {
	return TitleCredits{
		Slug:             strings.TrimSpace(it.Slug),
		ContentRatingAge: extractContentRatingAge(it),
		Director:         creditsFromPeople(it.Director),
		Writer:           creditsFromPeople(it.Writer),
		Role:             creditsFromPeople(it.Role),
		Producer:         creditsFromPeople(it.Producer),
	}
}

// HasThumb reports whether raw is a sanitized Plex CDN portrait URL present on
// any credit in tc.
func (tc TitleCredits) HasThumb(raw string) bool {
	if raw == "" || sanitizePlexThumb(raw) == "" {
		return false
	}
	for _, list := range [][]Credit{tc.Role, tc.Director, tc.Writer, tc.Producer} {
		for _, c := range list {
			if c.Thumb == raw {
				return true
			}
		}
	}
	return false
}

// Match maps an Ether person of kind ("actor", "director", "writer",
// "producer") onto a verified Plex provider Credit in tc.
//
// Priority:
//  1. Normalized name + character/role match within the same kind (actors).
//  2. Provider TMDB person id match when the provider supplies it.
//  3. Unique normalized name match within the same kind (all matches agree on
//     one 24-hex tagKey; ambiguous namesakes are never guessed).
//  4. Unique normalized name match across all credit kinds on the same title.
func (tc TitleCredits) Match(kind string, p core.Person) (Credit, bool) {
	var primary []Credit
	switch kind {
	case "actor":
		primary = tc.Role
	case "director":
		primary = tc.Director
	case "writer":
		primary = tc.Writer
	case "producer":
		primary = tc.Producer
	}
	all := [][]Credit{tc.Role, tc.Director, tc.Writer, tc.Producer}
	wantName := normCreditName(p.Name)

	// 1. Name + character match for cast roles.
	if kind == "actor" && wantName != "" && p.Role != "" {
		if c, ok := uniqueCreditMatch(primary, func(c Credit) bool {
			return normCreditName(c.Name) == wantName && creditRolesMatch(c.Role, p.Role)
		}); ok {
			return c, true
		}
	}

	// 2. Explicit TMDB id match when the provider supplies it.
	if p.TMDB > 0 {
		for _, c := range primary {
			if c.TMDB == p.TMDB && IsPersonTagKey(c.TagKey) {
				return c, true
			}
		}
		for _, list := range all {
			for _, c := range list {
				if c.TMDB == p.TMDB && IsPersonTagKey(c.TagKey) {
					return c, true
				}
			}
		}
	}

	if wantName == "" {
		return Credit{}, false
	}

	// 3. Unique normalized name match within the same kind.
	if c, ok := uniqueCreditMatch(primary, func(c Credit) bool {
		return normCreditName(c.Name) == wantName
	}); ok {
		return c, true
	}

	// 4. Unique normalized name match across all kinds on the same title.
	var (
		best   Credit
		found  bool
		tagKey string
	)
	for _, list := range all {
		for _, c := range list {
			if !IsPersonTagKey(c.TagKey) || normCreditName(c.Name) != wantName {
				continue
			}
			if !found {
				best, found, tagKey = c, true, c.TagKey
				continue
			}
			if c.TagKey != tagKey {
				return Credit{}, false
			}
			if best.Thumb == "" && c.Thumb != "" {
				best = c
			}
		}
	}
	return best, found
}

func uniqueCreditMatch(list []Credit, pred func(Credit) bool) (Credit, bool) {
	var (
		best   Credit
		found  bool
		tagKey string
	)
	for _, c := range list {
		if !IsPersonTagKey(c.TagKey) || !pred(c) {
			continue
		}
		if !found {
			best, found, tagKey = c, true, c.TagKey
			continue
		}
		if c.TagKey != tagKey {
			return Credit{}, false
		}
		if best.Thumb == "" && c.Thumb != "" {
			best = c
		}
	}
	return best, found
}

func normCreditName(s string) string {
	s = foldName(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		} else {
			space = true
		}
	}
	return b.String()
}

func stripParens(s string) string {
	if !strings.ContainsRune(s, '(') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	depth := 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func creditRolesMatch(a, b string) bool {
	na := normCreditName(stripParens(a))
	nb := normCreditName(stripParens(b))
	if na == "" || nb == "" {
		return false
	}
	if na == nb {
		return true
	}
	partsA := splitRoleParts(stripParens(a))
	partsB := splitRoleParts(stripParens(b))
	for _, pa := range partsA {
		for _, pb := range partsB {
			if pa == pb {
				return true
			}
		}
	}
	return false
}

func splitRoleParts(s string) []string {
	raw := strings.Split(s, "/")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		if n := normCreditName(part); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// catalogStamp returns max(detail_at, fetched_at) from catalog_titles for k.
// Any catalogue refresh that updates detail_at or fetched_at advances this
// stamp, automatically invalidating plexguid_title_credits for k.
func (s *Service) catalogStamp(ctx context.Context, k core.RatingKey) int64 {
	var stamp int64
	_ = s.db.QueryRowContext(ctx, `SELECT CASE WHEN COALESCE(detail_at,0) > COALESCE(fetched_at,0) THEN COALESCE(detail_at,0) ELSE COALESCE(fetched_at,0) END FROM catalog_titles WHERE key=?`, int64(k)).Scan(&stamp)
	return stamp
}

// StoreCredits durably caches tc for (k, guid) stamped against the title's
// current catalogue refresh timestamp.
func (s *Service) StoreCredits(ctx context.Context, k core.RatingKey, guid string, tc TitleCredits) error {
	if s == nil || s.db == nil {
		return nil
	}
	if _, _, ok := validCreditsGUID(guid); !ok {
		return nil
	}
	raw, err := json.Marshal(tc)
	if err != nil {
		return err
	}
	stamp := s.catalogStamp(ctx, k)
	_, err = s.db.ExecContext(ctx, `INSERT INTO plexguid_title_credits(rating_key,guid,catalog_stamp,fetched_at,payload) VALUES(?,?,?,?,?)
		ON CONFLICT(rating_key) DO UPDATE SET guid=excluded.guid, catalog_stamp=excluded.catalog_stamp, fetched_at=excluded.fetched_at, payload=excluded.payload`,
		int64(k), guid, stamp, time.Now().Unix(), string(raw))
	return err
}

// CachedCredits returns the durably cached provider credits for (k, guid) if
// present and not invalidated by a GUID change or catalogue refresh. Never
// touches the network.
func (s *Service) CachedCredits(ctx context.Context, k core.RatingKey, guid string) (TitleCredits, bool) {
	if s == nil || s.db == nil || guid == "" {
		return TitleCredits{}, false
	}
	if _, _, ok := validCreditsGUID(guid); !ok {
		return TitleCredits{}, false
	}
	var (
		storedGUID string
		stored     int64
		cur        int64
		payload    string
	)
	err := s.db.QueryRowContext(ctx, `SELECT c.guid, c.catalog_stamp,
		CASE WHEN COALESCE(t.detail_at,0) > COALESCE(t.fetched_at,0) THEN COALESCE(t.detail_at,0) ELSE COALESCE(t.fetched_at,0) END,
		c.payload
		FROM plexguid_title_credits c
		LEFT JOIN catalog_titles t ON t.key = c.rating_key
		WHERE c.rating_key=?`, int64(k)).Scan(&storedGUID, &stored, &cur, &payload)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TitleCredits{}, false
		}
		// Fallback for minimal test databases without catalog_titles.
		if e2 := s.db.QueryRowContext(ctx, `SELECT guid, payload FROM plexguid_title_credits WHERE rating_key=?`, int64(k)).Scan(&storedGUID, &payload); e2 != nil {
			return TitleCredits{}, false
		}
		stored, cur = 0, 0
	}
	if storedGUID != guid || cur != stored {
		return TitleCredits{}, false
	}
	var tc TitleCredits
	if err := json.Unmarshal([]byte(payload), &tc); err != nil {
		return TitleCredits{}, false
	}
	return tc, true
}

// CachedCreditsMany batch-loads valid cached provider credits for keys that
// hold a verified plex:// guid in guids. Never touches the network.
func (s *Service) CachedCreditsMany(ctx context.Context, keys []core.RatingKey, guids map[core.RatingKey]string) map[core.RatingKey]TitleCredits {
	if s == nil || s.db == nil || len(keys) == 0 || len(guids) == 0 {
		return nil
	}
	want := make([]core.RatingKey, 0, len(keys))
	seen := make(map[core.RatingKey]bool, len(keys))
	for _, k := range keys {
		if k == 0 || seen[k] {
			continue
		}
		seen[k] = true
		if _, _, ok := validCreditsGUID(guids[k]); ok {
			want = append(want, k)
		}
	}
	if len(want) == 0 {
		return nil
	}
	out := make(map[core.RatingKey]TitleCredits, len(want))
	const chunk = 200
	for start := 0; start < len(want); start += chunk {
		part := want[start:min(start+chunk, len(want))]
		args := make([]any, len(part))
		ph := make([]string, len(part))
		for i, k := range part {
			args[i] = int64(k)
			ph[i] = "?"
		}
		in := strings.Join(ph, ",")
		rows, err := s.db.QueryContext(ctx, `SELECT c.rating_key, c.guid, c.catalog_stamp,
			CASE WHEN COALESCE(t.detail_at,0) > COALESCE(t.fetched_at,0) THEN COALESCE(t.detail_at,0) ELSE COALESCE(t.fetched_at,0) END,
			c.payload
			FROM plexguid_title_credits c
			LEFT JOIN catalog_titles t ON t.key = c.rating_key
			WHERE c.rating_key IN (`+in+`)`, args...)
		noCatalog := false
		if err != nil {
			rows, err = s.db.QueryContext(ctx, `SELECT rating_key, guid, 0, 0, payload FROM plexguid_title_credits WHERE rating_key IN (`+in+`)`, args...)
			if err != nil {
				return out
			}
			noCatalog = true
		}
		for rows.Next() {
			var (
				rk         int64
				storedGUID string
				stored     int64
				cur        int64
				payload    string
			)
			if rows.Scan(&rk, &storedGUID, &stored, &cur, &payload) != nil {
				continue
			}
			k := core.RatingKey(rk)
			if storedGUID != guids[k] || (!noCatalog && cur != stored) {
				continue
			}
			var tc TitleCredits
			if json.Unmarshal([]byte(payload), &tc) == nil {
				out[k] = tc
			}
		}
		rows.Close()
	}
	return out
}

// ResolveCreditsBounded returns cached provider credits for (k, guid)
// immediately, or waits at most wait for one deduplicated, concurrency-bounded
// fetch from metadata.provider.plex.tv. If wait expires first (or wait <= 0),
// the fetch completes in the background within the service lifecycle so the
// next open hits the durable cache.
func (s *Service) ResolveCreditsBounded(ctx context.Context, k core.RatingKey, guid string, wait time.Duration) (TitleCredits, bool) {
	if s == nil || s.db == nil || s.ctx.Err() != nil {
		return TitleCredits{}, false
	}
	kind, hex, ok := validCreditsGUID(guid)
	if !ok {
		return TitleCredits{}, false
	}
	if tc, hit := s.CachedCredits(ctx, k, guid); hit {
		return tc, true
	}
	if s.o.Token == nil || s.o.Token() == "" {
		return TitleCredits{}, false
	}
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return TitleCredits{}, false
	}
	if until, failed := s.creditFails[k]; failed && time.Now().Before(until) {
		s.mu.Unlock()
		return TitleCredits{}, false
	}
	f := s.creditFlights[k]
	if f == nil && len(s.creditFlights) < maxBoundedCreditFlights {
		if s.creditFlights == nil {
			s.creditFlights = map[core.RatingKey]*creditFlight{}
		}
		f = &creditFlight{done: make(chan struct{})}
		s.creditFlights[k] = f
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			bg, cancel := context.WithTimeout(s.ctx, bgLookupTimeout)
			defer cancel()
			tc, fetched := s.fetchAndStoreCredits(bg, k, kind, hex, guid)
			f.out = tc
			f.ok = fetched
			s.mu.Lock()
			delete(s.creditFlights, k)
			if !fetched {
				if s.creditFails == nil {
					s.creditFails = map[core.RatingKey]time.Time{}
				}
				if len(s.creditFails) >= maxCreditFails {
					s.creditFails = map[core.RatingKey]time.Time{}
				}
				s.creditFails[k] = time.Now().Add(creditFailCooldown)
			} else {
				delete(s.creditFails, k)
			}
			s.mu.Unlock()
			close(f.done)
		}()
	}
	s.mu.Unlock()
	if f == nil || wait <= 0 {
		return TitleCredits{}, false
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-f.done:
		return f.out, f.ok
	case <-timer.C:
		return TitleCredits{}, false
	case <-ctx.Done():
		return TitleCredits{}, false
	}
}

func (s *Service) fetchAndStoreCredits(ctx context.Context, k core.RatingKey, kind, hex, guid string) (TitleCredits, bool) {
	items, err := s.getItems(ctx, "/library/metadata/"+hex, url.Values{"includeGuids": {"1"}})
	if err != nil {
		return TitleCredits{}, false
	}
	for _, it := range items {
		if it.GUID != "" && it.GUID != guid {
			continue
		}
		if it.Type != "" && it.Type != kind {
			continue
		}
		tc := creditsFromProviderItem(it)
		_ = s.StoreCredits(ctx, k, guid, tc)
		return tc, true
	}
	return TitleCredits{}, false
}

// RecordPersonMappings persists newly observed real 24-hex Plex person tagKey
// to TMDB id mappings. Hot opens hit the bounded in-memory dedup table and
// perform zero database writes.
func (s *Service) RecordPersonMappings(ctx context.Context, mappings []PersonMapping) {
	if s == nil || s.db == nil || len(mappings) == 0 {
		return
	}
	var toWrite []PersonMapping
	seen := make(map[string]bool, len(mappings))
	s.mu.Lock()
	if s.recordedPersons == nil {
		s.recordedPersons = map[string]recordedPerson{}
	}
	for _, m := range mappings {
		tagKey := strings.ToLower(strings.TrimSpace(m.TagKey))
		if !IsPersonTagKey(tagKey) || m.PersonTMDB <= 0 || seen[tagKey] {
			continue
		}
		seen[tagKey] = true
		thumb := sanitizePlexThumb(m.Thumb)
		name := strings.TrimSpace(m.Name)
		prev, ok := s.recordedPersons[tagKey]
		if ok && prev.tmdb == m.PersonTMDB && (thumb == "" || prev.thumb == thumb) && (name == "" || prev.name == name) {
			continue
		}
		toWrite = append(toWrite, PersonMapping{
			TagKey:     tagKey,
			PersonTMDB: m.PersonTMDB,
			Name:       name,
			Thumb:      thumb,
		})
	}
	s.mu.Unlock()
	if len(toWrite) == 0 {
		return
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for _, m := range toWrite {
		if _, err := tx.ExecContext(ctx, `INSERT INTO plexguid_person_map(tag_key,person_tmdb,name,thumb,updated_at) VALUES(?,?,?,?,?)
			ON CONFLICT(tag_key) DO UPDATE SET
				person_tmdb=excluded.person_tmdb,
				name=CASE WHEN excluded.name<>'' THEN excluded.name ELSE plexguid_person_map.name END,
				thumb=CASE WHEN excluded.thumb<>'' THEN excluded.thumb ELSE plexguid_person_map.thumb END,
				updated_at=excluded.updated_at`,
			m.TagKey, m.PersonTMDB, m.Name, m.Thumb, now); err != nil {
			return
		}
	}
	if err := tx.Commit(); err != nil {
		return
	}
	s.mu.Lock()
	if len(s.recordedPersons)+len(toWrite) > maxRecordedPersons {
		s.recordedPersons = map[string]recordedPerson{}
	}
	for _, m := range toWrite {
		prev := s.recordedPersons[m.TagKey]
		if m.Name == "" {
			m.Name = prev.name
		}
		if m.Thumb == "" {
			m.Thumb = prev.thumb
		}
		s.recordedPersons[m.TagKey] = recordedPerson{tmdb: m.PersonTMDB, name: m.Name, thumb: m.Thumb}
	}
	s.mu.Unlock()
}

// PersonByTagKey resolves a real 24-hex Plex person tagKey to its mapped TMDB
// person id in O(1) via plexguid_person_map's primary key.
func (s *Service) PersonByTagKey(ctx context.Context, tagKey string) (PersonMapping, bool) {
	if s == nil || s.db == nil {
		return PersonMapping{}, false
	}
	tagKey = strings.ToLower(strings.TrimSpace(tagKey))
	if !IsPersonTagKey(tagKey) {
		return PersonMapping{}, false
	}
	var m PersonMapping
	if err := s.db.QueryRowContext(ctx, `SELECT tag_key, person_tmdb, name, thumb FROM plexguid_person_map WHERE tag_key=?`, tagKey).
		Scan(&m.TagKey, &m.PersonTMDB, &m.Name, &m.Thumb); err != nil || m.PersonTMDB <= 0 {
		return PersonMapping{}, false
	}
	return m, true
}

// PersonByTMDB resolves a TMDB person id to its known real 24-hex Plex person
// tagKey (when previously observed) via plexguid_person_map_tmdb.
func (s *Service) PersonByTMDB(ctx context.Context, tmdb int64) (PersonMapping, bool) {
	if s == nil || s.db == nil || tmdb <= 0 {
		return PersonMapping{}, false
	}
	var m PersonMapping
	if err := s.db.QueryRowContext(ctx, `SELECT tag_key, person_tmdb, name, thumb FROM plexguid_person_map WHERE person_tmdb=? ORDER BY updated_at DESC LIMIT 1`, tmdb).
		Scan(&m.TagKey, &m.PersonTMDB, &m.Name, &m.Thumb); err != nil || !IsPersonTagKey(m.TagKey) {
		return PersonMapping{}, false
	}
	return m, true
}

// PeopleByTMDB batch-resolves TMDB person ids to their known real 24-hex Plex
// person tagKeys for search directory pills.
func (s *Service) PeopleByTMDB(ctx context.Context, ids []int64) map[int64]PersonMapping {
	if s == nil || s.db == nil || len(ids) == 0 {
		return nil
	}
	uniq := make([]int64, 0, len(ids))
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	if len(uniq) == 0 {
		return nil
	}
	out := make(map[int64]PersonMapping, len(uniq))
	const chunk = 200
	for start := 0; start < len(uniq); start += chunk {
		part := uniq[start:min(start+chunk, len(uniq))]
		args := make([]any, len(part))
		ph := make([]string, len(part))
		for i, id := range part {
			args[i] = id
			ph[i] = "?"
		}
		rows, err := s.db.QueryContext(ctx, `SELECT tag_key, person_tmdb, name, thumb, updated_at FROM plexguid_person_map WHERE person_tmdb IN (`+strings.Join(ph, ",")+`) ORDER BY updated_at DESC`, args...)
		if err != nil {
			return out
		}
		for rows.Next() {
			var (
				m   PersonMapping
				upd int64
			)
			if rows.Scan(&m.TagKey, &m.PersonTMDB, &m.Name, &m.Thumb, &upd) != nil || !IsPersonTagKey(m.TagKey) {
				continue
			}
			if _, exists := out[m.PersonTMDB]; !exists {
				out[m.PersonTMDB] = m
			}
		}
		rows.Close()
	}
	return out
}
