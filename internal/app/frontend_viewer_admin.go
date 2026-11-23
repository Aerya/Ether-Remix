package app

import (
	"context"
	"errors"
	"strings"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/identity"
)

func (a *App) apiKeyStore() (*identity.APIKeys, error) {
	a.apiKeysMu.Lock()
	defer a.apiKeysMu.Unlock()
	if a.apiKeys != nil {
		return a.apiKeys, nil
	}
	if a.db == nil || a.users == nil {
		return nil, errors.New("app: api keys unavailable")
	}
	k, err := identity.NewAPIKeys(a.db, a.users, nil)
	if err != nil {
		return nil, err
	}
	a.apiKeys = k
	return k, nil
}

func (a *App) mustAPIKeyStore() *identity.APIKeys {
	k, _ := a.apiKeyStore()
	return k
}

func (a *App) viewerAdmin(local *identity.Local, grants *identity.Grants) frontend.ViewerAdmin {
	if a == nil || a.users == nil || local == nil {
		return nil
	}
	return &surfaceViewerAdmin{app: a, local: local, grants: grants}
}

type surfaceViewerAdmin struct {
	app    *App
	local  *identity.Local
	grants *identity.Grants
}

var _ frontend.ViewerAdmin = (*surfaceViewerAdmin)(nil)

func (v *surfaceViewerAdmin) ListViewers(ctx context.Context) ([]frontend.ManagedViewer, error) {
	all, err := v.app.users.AllUsers(ctx)
	if err != nil {
		return nil, err
	}
	pwSet, err := v.local.LocalPasswordViewerIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]frontend.ManagedViewer, 0, len(all))
	for _, u := range all {
		if u.Origin == "dlna_guest" {
			continue
		}
		out = append(out, frontend.ManagedViewer{
			User:             u.User,
			Active:           u.Active,
			OwnerEnabled:     u.OwnerEnabled,
			HasPlex:          u.HasPlex,
			Origin:           u.Origin,
			HasLocalPassword: pwSet[u.ID],
			LastSeen:         u.LastSeen,
		})
	}
	return out, nil
}

func (v *surfaceViewerAdmin) GetViewer(ctx context.Context, id int64) (*frontend.ManagedViewer, error) {
	if id <= 0 {
		return nil, core.ErrNotFound
	}
	all, err := v.app.users.AllUsers(ctx)
	if err != nil {
		return nil, err
	}
	pwSet, err := v.local.LocalPasswordViewerIDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range all {
		if u.ID != id {
			continue
		}
		if u.Origin == "dlna_guest" {
			return nil, core.ErrNotFound
		}
		mv := frontend.ManagedViewer{
			User:             u.User,
			Active:           u.Active,
			OwnerEnabled:     u.OwnerEnabled,
			HasPlex:          u.HasPlex,
			Origin:           u.Origin,
			HasLocalPassword: pwSet[u.ID],
			LastSeen:         u.LastSeen,
		}
		return &mv, nil
	}
	return nil, core.ErrNotFound
}

func (v *surfaceViewerAdmin) CreateLocalViewer(ctx context.Context, username, password string) (*frontend.ManagedViewer, error) {
	username = strings.TrimSpace(username)
	var (
		id  int64
		err error
	)
	if password == "" {
		id, err = v.local.CreateViewerPasswordless(ctx, username)
	} else {
		id, err = v.local.CreateViewer(ctx, username, password)
	}
	if err != nil {
		return nil, mapViewerAdminError(err)
	}
	if err := v.app.users.RefreshViewers(ctx); err != nil {
		return nil, err
	}
	return v.GetViewer(ctx, id)
}

func (v *surfaceViewerAdmin) UpdateViewerPolicy(ctx context.Context, id int64, update frontend.ViewerPolicyUpdate) error {
	mv, err := v.GetViewer(ctx, id)
	if err != nil {
		return err
	}
	if update.Paused != nil {
		if mv.User.IsOwner {
			if *update.Paused {
				return frontend.ErrViewerProtected
			}
		} else if *update.Paused {
			if err := v.app.users.PauseViewer(ctx, id); err != nil {
				return mapViewerAdminError(err)
			}
			if v.grants != nil {
				_, _ = v.grants.SignOutViewer(ctx, id)
			}
			_, _ = v.app.users.RevokeViewerSessions(ctx, id)
		} else {
			if err := v.app.users.ResumeViewer(ctx, id); err != nil {
				return mapViewerAdminError(err)
			}
		}
	}
	if update.AllowedLibraries != nil {
		u, err := v.app.users.User(ctx, id)
		if err != nil {
			return err
		}
		u.AllowedLibraries = append([]core.LibraryID(nil), (*update.AllowedLibraries)...)
		if err := v.app.users.UpdateUser(ctx, *u); err != nil {
			return err
		}
	}
	if update.Hidden != nil {
		if err := v.app.users.SetViewerHidden(ctx, id, *update.Hidden); err != nil {
			return err
		}
	}
	return nil
}

func (v *surfaceViewerAdmin) SetViewerPassword(ctx context.Context, id int64, password string, reset bool) error {
	mv, err := v.GetViewer(ctx, id)
	if err != nil {
		return err
	}
	if reset && password == "" {
		if mv.User.IsOwner {
			return frontend.ErrViewerProtected
		}
		if err := v.local.ClearPassword(ctx, id); err != nil {
			return err
		}
		if v.grants != nil {
			_, _ = v.grants.SignOutViewer(ctx, id)
		}
		_, _ = v.app.users.RevokeViewerSessions(ctx, id)
		return nil
	}
	subject, err := v.local.LocalUsername(ctx, id)
	if err != nil {
		return err
	}
	if subject == "" {
		subject = strings.TrimSpace(mv.User.Username)
		if subject == "" {
			subject = strings.TrimSpace(mv.User.Title)
		}
	}
	if err := v.local.UpsertPassword(ctx, id, subject, password); err != nil {
		return mapViewerAdminError(err)
	}
	if mv.User.IsOwner {
		v.app.syncOwnerRecovery(ctx)
	} else if reset {
		if v.grants != nil {
			_, _ = v.grants.SignOutViewer(ctx, id)
		}
		_, _ = v.app.users.RevokeViewerSessions(ctx, id)
	}
	return nil
}

func (v *surfaceViewerAdmin) VerifyViewerPassword(ctx context.Context, id int64, password string) error {
	mv, err := v.GetViewer(ctx, id)
	if err != nil {
		return err
	}
	subject, err := v.local.LocalUsername(ctx, id)
	if err != nil {
		return err
	}
	if subject == "" {
		subject = strings.TrimSpace(mv.User.Username)
	}
	gotID, _, err := v.local.Authenticate(ctx, subject, password)
	if err != nil || gotID != id {
		return core.ErrUnauthorized
	}
	return nil
}

func (v *surfaceViewerAdmin) RemoveViewer(ctx context.Context, id int64) error {
	if _, err := v.GetViewer(ctx, id); err != nil {
		return err
	}
	if err := v.app.users.RemoveViewer(ctx, id); err != nil {
		return mapViewerAdminError(err)
	}
	if v.app.catalog != nil {
		_ = v.app.catalog.RefreshViewerRules(ctx)
	}
	return nil
}

func mapViewerAdminError(err error) error {
	switch {
	case errors.Is(err, identity.ErrPasswordTooShort):
		return frontend.ErrPasswordTooShort
	case errors.Is(err, identity.ErrUsernameExists):
		return frontend.ErrViewerExists
	case errors.Is(err, auth.ErrViewerIsOwner):
		return frontend.ErrViewerProtected
	case errors.Is(err, auth.ErrViewerPlexManaged):
		return frontend.ErrViewerPlexManaged
	default:
		return err
	}
}
