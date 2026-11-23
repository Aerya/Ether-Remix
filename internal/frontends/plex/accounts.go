package plex

import (
	"context"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// plexAccountOwnerID is the account ID real Plex Media Server always assigns
// to the server owner across /accounts, /accounts/1, /status/sessions,
// /status/sessions/history/all, /library/all/top,
// /library/metadata/{key}/users/top, /statistics/bandwidth, and
// /statistics/media, regardless of the owner's plex.tv account ID.
const plexAccountOwnerID int64 = 1

// plexAccountID returns the canonical PMS wire account ID for u.
//
// Verified against real PMS 1.43.4:
//   - the server owner is always id 1;
//   - every non-owner Plex account — friends, Plex Home members, and managed
//     Home users — uses its stable plex.tv account ID (u.PlexID), matching
//     what /accounts lists;
//   - local-only Ether viewers with no plex.tv account (u.PlexID == 0) use
//     their local viewer ID (u.ID).
//
// Ether's internal stores (watch history, sessions, dashboard metrics,
// notification routing) stay keyed by local u.ID; only the Plex wire boundary
// translates to and from plexAccountID.
func plexAccountID(u frontend.User) int64 {
	if u.IsOwner {
		return plexAccountOwnerID
	}
	if u.PlexID > 0 {
		return u.PlexID
	}
	return u.ID
}

// plexAccountName returns the non-empty account name to emit for u. Managed
// Plex Home users have no plex.tv username, so Title supplies the display name.
func plexAccountName(u frontend.User) string {
	if u.Username != "" {
		return u.Username
	}
	return u.Title
}

func (s *Server) lookupUser(ctx context.Context, id int64) (frontend.User, bool) {
	if s == nil || id <= 0 {
		return frontend.User{}, false
	}
	ident := s.getHost().Identity
	if ident == nil {
		return frontend.User{}, false
	}
	if src, ok := ident.(frontend.UserLookupSource); ok {
		if u, err := src.User(ctx, id); err == nil && u != nil {
			return *u, true
		}
	}
	if users, err := s.listUsers(ctx); err == nil {
		for _, u := range users {
			if u.ID == id {
				return u, true
			}
		}
	}
	return frontend.User{}, false
}

func (s *Server) listKnownUsers(ctx context.Context) ([]frontend.User, bool) {
	if s == nil {
		return nil, false
	}
	ident := s.getHost().Identity
	if ident == nil {
		return nil, false
	}
	if src, ok := ident.(frontend.KnownUsersSource); ok {
		if users, err := src.KnownUsers(ctx); err == nil {
			return users, true
		}
	}
	return nil, false
}

// plexAccountIDFor returns the canonical PMS account ID for u, recognising a
// delegated token issued by the server owner (whose IsOwner flag is cleared
// for privilege restriction while still representing the owner's account).
func (s *Server) plexAccountIDFor(ctx context.Context, u *frontend.User) int64 {
	if u == nil {
		return 0
	}
	if u.IsOwner {
		return plexAccountOwnerID
	}
	if u.Delegated {
		if orig, ok := s.lookupUser(ctx, u.ID); ok && orig.IsOwner {
			return plexAccountOwnerID
		}
	}
	return plexAccountID(*u)
}

type plexAccountEntry struct {
	user      frontend.User
	canonical int64
}

// plexAccountDir maps between Ether's local viewer IDs and canonical Plex wire
// account IDs for one request. Inbound resolution accepts both the canonical
// ID and the alternate/legacy ID (owner's plex.tv ID or a friend's local ID)
// so existing Plex clients keep working, while outbound emission always uses
// the canonical ID.
type plexAccountDir struct {
	s           *Server
	includeAll  bool
	entries     []plexAccountEntry
	byLocal     map[int64]plexAccountEntry
	knownLoaded bool
	known       []plexAccountEntry
	missed      map[int64]bool
}

func (s *Server) newPlexAccountDir(ctx context.Context, caller *frontend.User, includeAll bool) *plexAccountDir {
	dir, err := s.newPlexAccountDirErr(ctx, caller, includeAll)
	if err != nil {
		s.log.Warn("list users", "err", err)
		return s.plexAccountDirFrom(ctx, caller, nil, includeAll)
	}
	return dir
}

func (s *Server) newPlexAccountDirErr(ctx context.Context, caller *frontend.User, includeAll bool) (*plexAccountDir, error) {
	var users []frontend.User
	if includeAll && s.getHost().Identity != nil {
		us, err := s.listUsers(ctx)
		if err != nil {
			return nil, err
		}
		users = us
	}
	return s.plexAccountDirFrom(ctx, caller, users, includeAll), nil
}

func (s *Server) plexAccountDirFrom(ctx context.Context, caller *frontend.User, users []frontend.User, includeAll bool) *plexAccountDir {
	dir := &plexAccountDir{
		s:          s,
		includeAll: includeAll,
		byLocal:    make(map[int64]plexAccountEntry, len(users)+1),
	}
	add := func(u frontend.User, canonical int64) {
		if _, exists := dir.byLocal[u.ID]; exists {
			return
		}
		e := plexAccountEntry{user: u, canonical: canonical}
		dir.byLocal[u.ID] = e
		dir.entries = append(dir.entries, e)
	}
	for _, u := range users {
		add(u, plexAccountID(u))
	}
	if caller != nil {
		add(*caller, s.plexAccountIDFor(ctx, caller))
	}
	return dir
}

func matchAccountEntry(entries []plexAccountEntry, rawID int64) (plexAccountEntry, bool) {
	for _, e := range entries {
		if e.canonical == rawID {
			return e, true
		}
	}
	for _, e := range entries {
		if (e.user.PlexID > 0 && e.user.PlexID == rawID) || (e.user.ID > 0 && e.user.ID == rawID) {
			return e, true
		}
	}
	return plexAccountEntry{}, false
}

// resolve maps a client-supplied accountID (canonical or legacy/alternate) to
// its account entry. Canonical matches always take precedence over alternates.
// On owner-scoped lookups (includeAll), deactivated accounts are also resolved
// via Identity's lookup interfaces so filters and /accounts/{id} work for every
// account ID emitted in history and statistics.
func (d *plexAccountDir) resolve(ctx context.Context, rawID int64) (plexAccountEntry, bool) {
	if d == nil || rawID <= 0 {
		return plexAccountEntry{}, false
	}
	if e, ok := matchAccountEntry(d.entries, rawID); ok {
		return e, true
	}
	if !d.includeAll || d.s == nil {
		return plexAccountEntry{}, false
	}
	if !d.knownLoaded {
		d.knownLoaded = true
		if users, ok := d.s.listKnownUsers(ctx); ok {
			for _, u := range users {
				if _, exists := d.byLocal[u.ID]; exists {
					continue
				}
				d.known = append(d.known, plexAccountEntry{user: u, canonical: plexAccountID(u)})
			}
		}
	}
	if e, ok := matchAccountEntry(d.known, rawID); ok {
		return e, true
	}
	if u, ok := d.s.lookupUser(ctx, rawID); ok {
		return plexAccountEntry{user: u, canonical: plexAccountID(u)}, true
	}
	return plexAccountEntry{}, false
}

// user resolves an internal local viewer ID to its account entry, falling back
// to Identity's user lookup for deactivated accounts on owner-scoped reads.
func (d *plexAccountDir) user(ctx context.Context, localID int64) (plexAccountEntry, bool) {
	if d == nil || localID <= 0 {
		return plexAccountEntry{}, false
	}
	if e, ok := d.byLocal[localID]; ok {
		return e, true
	}
	if !d.includeAll || d.s == nil || d.missed[localID] {
		return plexAccountEntry{}, false
	}
	if u, ok := d.s.lookupUser(ctx, localID); ok {
		e := plexAccountEntry{user: u, canonical: plexAccountID(u)}
		d.byLocal[localID] = e
		return e, true
	}
	if d.missed == nil {
		d.missed = map[int64]bool{}
	}
	d.missed[localID] = true
	return plexAccountEntry{}, false
}

// wireID translates an internal local viewer ID into its canonical Plex wire
// account ID.
func (d *plexAccountDir) wireID(ctx context.Context, localID int64) int64 {
	if localID <= 0 {
		return 0
	}
	if e, ok := d.user(ctx, localID); ok {
		return e.canonical
	}
	return localID
}
