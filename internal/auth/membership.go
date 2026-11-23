package auth

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"time"
)

// SetOwnerEnabled changes the owner's membership decision. Plex sharing
// remains independently authoritative and can keep a viewer inactive.
func (s *Service) SetOwnerEnabled(ctx context.Context, viewerID int64, enabled bool) error {
	s.membershipMu.Lock()
	defer s.membershipMu.Unlock()
	state := 0
	if enabled {
		state = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE auth_users SET owner_enabled=?, active=CASE WHEN ?=1 AND (plex_id IS NULL OR plex_shared=1) THEN 1 ELSE 0 END,updated_at=? WHERE id=? AND is_owner=0`, state, state, time.Now().Unix(), viewerID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return core.ErrUnauthorized
	}
	if err = s.reload(ctx); err != nil {
		return err
	}
	s.notify("")
	var name string
	var streamLimit int
	var maxRating string
	var libs []core.LibraryID
	if u, e := s.User(ctx, viewerID); e == nil && u != nil {
		name = u.Username
		if name == "" {
			name = u.Title
		}
		streamLimit = u.StreamLimit
		maxRating = u.MaxContentRating
		libs = u.AllowedLibraries
	}
	if !enabled {
		s.emitViewerEvent(ViewerEvent{
			Kind:     "deactivated",
			ViewerID: viewerID,
			Username: name,
			Reason:   "paused_by_owner",
			Actor:    "owner",
		})
	} else {
		s.emitViewerEvent(ViewerEvent{
			Kind:             "access_changed",
			ViewerID:         viewerID,
			Username:         name,
			StreamLimit:      streamLimit,
			MaxContentRating: maxRating,
			AllowedLibraries: libs,
			Actor:            "owner",
		})
	}
	return nil
}

// SetViewerHidden sets whether a viewer is hidden from public sign-in pickers
// (Jellyfin/Emby Policy.IsHidden).
func (s *Service) SetViewerHidden(ctx context.Context, viewerID int64, hidden bool) error {
	s.membershipMu.Lock()
	defer s.membershipMu.Unlock()
	val := 0
	if hidden {
		val = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE auth_users SET hidden=?, updated_at=? WHERE id=? AND origin<>'dlna_guest'`, val, time.Now().Unix(), viewerID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return core.ErrNotFound
	}
	if err := s.reload(ctx); err != nil {
		return err
	}
	s.gen.Add(1)
	s.notify("")
	return nil
}
