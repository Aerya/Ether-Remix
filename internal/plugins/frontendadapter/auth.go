package frontendadapter

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// Auth modes an adapter declaration may pick (spec §6.5).
const (
	AuthEtherToken = "ether_token"
	AuthBasic      = "basic"
	AuthNoneLAN    = "none_lan"
	// AuthQueryCredentials reads a per-viewer app login from the query
	// string on every request: u (username) plus either p (the password,
	// plain or "enc:" hex) or t and s (t = md5(password + s), lowercase hex).
	// Ether issues and owns the login (an app password like Xtream's); the
	// plugin never sees p, t or s.
	AuthQueryCredentials = "query_credentials"
)

// Query parameters that carry query credentials. They are removed before the
// request reaches the plugin.
const (
	queryUser     = "u"
	queryPassword = "p"
	queryToken    = "t"
	querySalt     = "s"
)

// errRateLimited means the client's address exceeded the shared identity
// limiter after failed credentials; the proxy answers 429.
var errRateLimited = errors.New("frontendadapter: rate limited")

// errNotVisible means the authenticated viewer cannot see the requested
// library. The proxy answers 404, never 403, so a hidden library's
// existence does not leak.
var errNotVisible = errors.New("frontendadapter: library not visible")

// errUnauthenticated means no valid viewer credential was presented.
var errUnauthenticated = errors.New("frontendadapter: unauthenticated")

// authenticated is the result of authenticating one inbound request.
type authenticated struct {
	viewer *core.Viewer
	// libraries is the intersection of the adapter's declared libraries
	// and the viewer's visible libraries.
	libraries []core.LibraryID
}

// authenticator resolves the viewer for one adapter from the host identity
// surface, then filters the adapter's libraries to those the viewer sees.
type authenticator struct {
	decl Declaration
	host frontend.Host
}

func newAuthenticator(decl Declaration, host frontend.Host) (*authenticator, error) {
	switch decl.Meta.Auth {
	case "", AuthEtherToken, AuthBasic, AuthNoneLAN, AuthQueryCredentials:
	default:
		return nil, fmt.Errorf("frontendadapter: adapter %q has unknown auth mode %q", decl.Meta.Key, decl.Meta.Auth)
	}
	return &authenticator{decl: decl, host: host}, nil
}

// authenticate resolves the viewer and their visible libraries. The
// returned error is errUnauthenticated or errNotVisible; the caller maps
// those to 401 and 404.
func (a *authenticator) authenticate(ctx context.Context, r *http.Request) (authenticated, error) {
	switch a.decl.Meta.Auth {
	case AuthNoneLAN:
		return a.authenticateLAN(ctx, r)
	case AuthBasic:
		return a.authenticateBasic(ctx, r)
	case AuthQueryCredentials:
		return a.authenticateQuery(ctx, r)
	default:
		return a.authenticateToken(ctx, r)
	}
}

// authenticateToken resolves an Ether credential (a bearer token, or the
// conventional Basic password an OPDS reader supplies) through the bound
// identity surface for this adapter's protocol.
func (a *authenticator) authenticateToken(ctx context.Context, r *http.Request) (authenticated, error) {
	credential := bearerCredential(r)
	if credential == "" {
		if user, pass, ok := r.BasicAuth(); ok {
			credential = pass
			if credential == "" {
				credential = user
			}
		}
	}
	if credential == "" {
		return authenticated{}, errUnauthenticated
	}
	return a.resolve(ctx, r, credential)
}

// authenticateBasic resolves the credential Ether issued for this adapter
// via HTTP Basic. Ether still owns the credential; the plugin does not.
func (a *authenticator) authenticateBasic(ctx context.Context, r *http.Request) (authenticated, error) {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return authenticated{}, errUnauthenticated
	}
	credential := pass
	if credential == "" {
		credential = user
	}
	return a.resolve(ctx, r, credential)
}

// authenticateQuery resolves an app login from the query credentials. Valid
// credentials never charge the identity limiter; failures do, per verified
// client address, exactly like Xtream logins.
func (a *authenticator) authenticateQuery(ctx context.Context, r *http.Request) (authenticated, error) {
	q := r.URL.Query()
	user := q.Get(queryUser)
	if user == "" || a.host.Identity == nil || a.host.ClientIP == nil {
		return authenticated{}, errUnauthenticated
	}
	ip, ok := a.host.ClientIP(r)
	if !ok {
		return authenticated{}, errUnauthenticated
	}
	ctx = frontend.WithQuietIdentity(frontend.WithClientIP(ctx, ip))
	var (
		viewer *core.Viewer
		err    error
	)
	if token, salt := q.Get(queryToken), q.Get(querySalt); token != "" && salt != "" {
		id, ok := a.host.Identity.(frontend.AppLoginTokenIdentity)
		if !ok {
			return authenticated{}, errUnauthenticated
		}
		viewer, err = id.AuthenticateAppToken(ctx, user, token, salt)
	} else if password, ok := queryPasswordValue(q.Get(queryPassword)); ok {
		id, ok := a.host.Identity.(frontend.AppPasswordIdentity)
		if !ok {
			return authenticated{}, errUnauthenticated
		}
		viewer, err = id.AuthenticateAppPassword(ctx, user, password)
	} else {
		return authenticated{}, errUnauthenticated
	}
	if errors.Is(err, frontend.ErrRateLimited) {
		return authenticated{}, errRateLimited
	}
	if err != nil || viewer == nil {
		return authenticated{}, errUnauthenticated
	}
	return a.visible(viewer)
}

// queryPasswordValue decodes a p parameter: plain text, or "enc:" followed by
// the password's bytes in hex.
func queryPasswordValue(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	if rest, ok := strings.CutPrefix(raw, "enc:"); ok {
		decoded, err := hex.DecodeString(rest)
		if err != nil || len(decoded) == 0 {
			return "", false
		}
		return string(decoded), true
	}
	return raw, true
}

// stripQueryCredentials removes the credential parameters from a query the
// plugin will see. The username stays: it is the login name the client shows,
// not a secret.
func stripQueryCredentials(values url.Values) url.Values {
	out := make(url.Values, len(values))
	for k, v := range values {
		switch k {
		case queryPassword, queryToken, querySalt:
			continue
		}
		out[k] = v
	}
	return out
}

// authenticateLAN follows the DLNA rule: only a request Ether has already
// classified as coming from a trusted LAN, with an owner-picked viewer,
// is accepted without a credential.
func (a *authenticator) authenticateLAN(ctx context.Context, r *http.Request) (authenticated, error) {
	if a.host.LANViewer == nil {
		return authenticated{}, errUnauthenticated
	}
	viewer, err := a.host.LANViewer(ctx, r)
	if err != nil || viewer == nil {
		return authenticated{}, errUnauthenticated
	}
	return a.visible(viewer)
}

// resolve resolves a credential for the adapter's protocol and filters the
// libraries.
func (a *authenticator) resolve(ctx context.Context, r *http.Request, credential string) (authenticated, error) {
	if a.host.Identity == nil {
		return authenticated{}, errUnauthenticated
	}
	// A quiet identity skips activity bookkeeping; proxy requests are
	// frequent and should not each write viewer-times rows.
	viewer, err := a.host.Identity.Resolve(frontend.WithQuietIdentity(ctx), credential)
	if err != nil || viewer == nil {
		return authenticated{}, errUnauthenticated
	}
	return a.visible(viewer)
}

// visible intersects the adapter's declared libraries with the viewer's
// visible libraries. A viewer who can see none of them is answered 404.
func (a *authenticator) visible(viewer *core.Viewer) (authenticated, error) {
	allowed := make([]core.LibraryID, 0, len(a.decl.Libraries))
	for _, id := range a.decl.Libraries {
		if viewer.AllowsLibrary(id) {
			allowed = append(allowed, id)
		}
	}
	if len(a.decl.Libraries) > 0 && len(allowed) == 0 {
		return authenticated{}, errNotVisible
	}
	return authenticated{viewer: viewer, libraries: allowed}, nil
}

// bearerCredential reads a bearer token from the Authorization header.
func bearerCredential(r *http.Request) string {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}
