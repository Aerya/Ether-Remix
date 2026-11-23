package frontend

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// APIKeyInfo describes one active MediaBrowser API key without exposing its secret.
type APIKeyInfo struct {
	ID        int64     `json:"id"`
	Handle    string    `json:"handle"`
	AppName   string    `json:"appName"`
	CreatedAt time.Time `json:"createdAt"`
	LastUsed  time.Time `json:"lastUsed,omitempty"`
}

// APIKeyIdentity is optionally implemented by SurfaceIdentity for surfaces
// that support long-lived owner API keys (Jellyfin and Emby).
type APIKeyIdentity interface {
	ResolveAPIKey(ctx context.Context, key string) (*core.Viewer, APIKeyInfo, error)
	ListAPIKeys(ctx context.Context) ([]APIKeyInfo, error)
	CreateAPIKey(ctx context.Context, appName string) (APIKeyInfo, string, error)
	RevokeAPIKey(ctx context.Context, keyOrHandle string) (int64, error)
}

// ManagedViewer is the admin view of one Ether viewer (including paused viewers).
type ManagedViewer struct {
	User             core.User
	Active           bool
	OwnerEnabled     bool
	HasPlex          bool
	Origin           string
	HasLocalPassword bool
	LastSeen         time.Time
}

// ViewerPolicyUpdate describes the policy fields a frontend admin API can map
// onto Ether's viewer model. Nil pointers leave the field unchanged.
type ViewerPolicyUpdate struct {
	AllowedLibraries *[]core.LibraryID // non-nil empty slice grants all libraries
	Paused           *bool
	Hidden           *bool
}

var (
	ErrViewerExists      = errors.New("viewer already exists")
	ErrPasswordTooShort  = errors.New("password must be at least 12 characters")
	ErrViewerProtected   = errors.New("viewer cannot be modified this way")
	ErrViewerPlexManaged = errors.New("viewer is managed on plex.tv")
)

// ViewerAdmin exposes owner-authorized viewer administration mapped onto
// Ether's viewer store (no parallel user store).
type ViewerAdmin interface {
	ListViewers(ctx context.Context) ([]ManagedViewer, error)
	GetViewer(ctx context.Context, id int64) (*ManagedViewer, error)
	CreateLocalViewer(ctx context.Context, username, password string) (*ManagedViewer, error)
	UpdateViewerPolicy(ctx context.Context, id int64, update ViewerPolicyUpdate) error
	SetViewerPassword(ctx context.Context, id int64, password string, reset bool) error
	VerifyViewerPassword(ctx context.Context, id int64, password string) error
	RemoveViewer(ctx context.Context, id int64) error
}
