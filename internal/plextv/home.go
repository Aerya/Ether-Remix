package plextv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

var ErrHomePINRequired = errors.New("Plex Home PIN required")

// ErrHomePINIncorrect is a refused Home switch for a protected member
// when a PIN was supplied: plex.tv rejected that PIN.
var ErrHomePINIncorrect = errors.New("Plex Home PIN incorrect")

var ErrPlexRefused = errors.New("Plex refused Home switch")

// HomeUser is one member of a Plex Home (GET /api/home/users).
type HomeUser struct {
	ID         int64
	UUID       string
	Username   string
	Title      string
	Friendly   string
	Thumb      string
	Protected  bool
	Restricted bool
	HomeAdmin  bool
}

// DisplayName prefers the friendly name, then title, then username.
func (u HomeUser) DisplayName() string {
	for _, s := range []string{u.Friendly, u.Title, u.Username} {
		if s != "" {
			return s
		}
	}
	return ""
}

// HomeUsers lists the members of the Plex Home the token belongs to.
// The token is sent only as the X-Plex-Token header and never logged.
func (c *Client) HomeUsers(ctx context.Context, token string) ([]HomeUser, error) {
	if strings.TrimSpace(token) == "" {
		return nil, &APIError{Method: http.MethodGet, Path: "/api/home/users", Status: http.StatusUnauthorized, Message: "no token"}
	}
	res, err := c.do(ctx, request{method: http.MethodGet, path: "/api/home/users", token: token, accept: "application/xml"})
	if err != nil {
		return nil, err
	}
	return parseHomeUsers(res.body)
}

func parseHomeUsers(body []byte) ([]HomeUser, error) {
	trim := bytes.TrimSpace(body)
	var out []HomeUser
	if bytes.HasPrefix(trim, []byte("{")) || bytes.HasPrefix(trim, []byte("[")) {
		// Be liberal: accept a JSON array, or an object wrapping one.
		var raw []struct {
			ID           int64  `json:"id"`
			UUID         string `json:"uuid"`
			Username     string `json:"username"`
			Title        string `json:"title"`
			FriendlyName string `json:"friendlyName"`
			Thumb        string `json:"thumb"`
			Protected    bool   `json:"protected"`
			Restricted   bool   `json:"restricted"`
			HomeAdmin    bool   `json:"homeAdmin"`
		}
		if bytes.HasPrefix(trim, []byte("{")) {
			var wrap struct {
				Users json.RawMessage `json:"users"`
				User  json.RawMessage `json:"user"`
			}
			if err := json.Unmarshal(trim, &wrap); err != nil {
				return nil, fmt.Errorf("plextv: decode home users: %w", err)
			}
			inner := wrap.Users
			if len(inner) == 0 {
				inner = wrap.User
			}
			if len(inner) == 0 {
				return nil, errors.New("plextv: home users response without users")
			}
			if err := json.Unmarshal(inner, &raw); err != nil {
				// A single user object instead of an array.
				var one struct {
					ID           int64  `json:"id"`
					UUID         string `json:"uuid"`
					Username     string `json:"username"`
					Title        string `json:"title"`
					FriendlyName string `json:"friendlyName"`
					Thumb        string `json:"thumb"`
					Protected    bool   `json:"protected"`
					Restricted   bool   `json:"restricted"`
					HomeAdmin    bool   `json:"homeAdmin"`
				}
				if err2 := json.Unmarshal(inner, &one); err2 != nil {
					return nil, fmt.Errorf("plextv: decode home users: %w", err)
				}
				raw = append(raw, one)
			}
		} else if err := json.Unmarshal(trim, &raw); err != nil {
			return nil, fmt.Errorf("plextv: decode home users: %w", err)
		}
		for _, u := range raw {
			if u.ID <= 0 {
				continue
			}
			out = append(out, HomeUser{ID: u.ID, UUID: u.UUID, Username: u.Username, Title: u.Title, Friendly: u.FriendlyName, Thumb: u.Thumb, Protected: u.Protected, Restricted: u.Restricted, HomeAdmin: u.HomeAdmin})
		}
	} else {
		root, err := parseXMLNode(trim)
		if err != nil {
			return nil, fmt.Errorf("plextv: decode home users: %w", err)
		}
		for _, m := range root.findAll("User") {
			id := m.intAttr("id")
			if id <= 0 {
				continue
			}
			out = append(out, HomeUser{
				ID:         id,
				UUID:       m.attr("uuid"),
				Username:   m.attr("username"),
				Title:      m.attr("title"),
				Friendly:   m.attr("friendlyName"),
				Thumb:      m.attr("thumb"),
				Protected:  m.boolAttr("protected"),
				Restricted: m.boolAttr("restricted"),
				HomeAdmin:  m.boolAttr("homeAdmin", "admin"),
			})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("plextv: home users response without users")
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ErrAvatarUnavailable identifies a Home user avatar that cannot be
// fetched or is not an acceptable image source.
var ErrAvatarUnavailable = errors.New("plextv: home user avatar unavailable")

const maxHomeUserAvatarBytes = 2 << 20

// HomeUserAvatar fetches a Home member's avatar image bytes from its
// plex.tv thumb URL. The URL comes from plex.tv response data, so it is
// confined to plex.tv (and Gravatar, where Plex hosts some account
// avatars) over HTTPS — plus this client's own base host, which is how
// tests fake plex.tv — and the body must be a bounded image. The token,
// when given, is sent only to plex.tv hosts as the X-Plex-Token header;
// neither it nor the thumb URL is ever logged.
// avatarHostAllowed reports whether u is an acceptable avatar source and
// whether it may receive the Plex token header.
func (c *Client) avatarHostAllowed(u *url.URL) (tokenHost, ok bool) {
	if u == nil || u.Host == "" || u.User != nil {
		return false, false
	}
	host := strings.ToLower(u.Hostname())
	plexHost := host == "plex.tv" || strings.HasSuffix(host, ".plex.tv")
	gravatar := host == "gravatar.com" || strings.HasSuffix(host, ".gravatar.com")
	sameBase := host == strings.ToLower(c.base.Hostname()) && u.Port() == c.base.Port()
	switch {
	case sameBase && u.Scheme == c.base.Scheme:
		return true, true
	case u.Scheme == "https" && plexHost:
		return true, true
	case u.Scheme == "https" && gravatar:
		return false, true
	default:
		return false, false
	}
}

func (c *Client) HomeUserAvatar(ctx context.Context, token, thumb string) ([]byte, string, error) {
	u, err := url.Parse(strings.TrimSpace(thumb))
	tokenHost, ok := c.avatarHostAllowed(u)
	if err != nil || !ok {
		return nil, "", ErrAvatarUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", ErrAvatarUnavailable
	}
	sendToken := ""
	if tokenHost {
		sendToken = token
	}
	c.setHeaders(req.Header, sendToken)
	req.Header.Set("Accept", "image/*")
	hc := *c.hc
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return ErrAvatarUnavailable
		}
		hopToken, hopOK := c.avatarHostAllowed(req.URL)
		if !hopOK {
			return ErrAvatarUnavailable
		}
		if !hopToken {
			req.Header.Del("X-Plex-Token")
		}
		return nil
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrAvatarUnavailable, redactURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", ErrAvatarUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHomeUserAvatarBytes+1))
	if err != nil || len(body) == 0 || len(body) > maxHomeUserAvatarBytes {
		return nil, "", ErrAvatarUnavailable
	}
	contentType := resp.Header.Get("Content-Type")
	if i := strings.Index(contentType, ";"); i >= 0 {
		contentType = strings.TrimSpace(contentType[:i])
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		contentType = http.DetectContentType(body)
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return nil, "", ErrAvatarUnavailable
	}
	return body, contentType, nil
}

// HomeUserToken uses the owner's authorized Home switch operation. A PIN, when
// configured by Plex, is still required; this never resets or bypasses it.
// A refused switch with a PIN supplied yields ErrHomePINIncorrect; without
// one, a protected member yields ErrHomePINRequired.
func (c *Client) HomeUserToken(ctx context.Context, ownerToken string, id int64, pin string) (string, error) {
	if id <= 0 {
		return "", ErrPlexRefused
	}
	q := url.Values{}
	if pin != "" {
		q.Set("pin", pin)
	}
	res, err := c.do(ctx, request{method: http.MethodPost, path: "/api/home/users/" + strconv.FormatInt(id, 10) + "/switch", token: ownerToken, query: q, accept: "application/xml"})
	if err != nil {
		if pin != "" {
			var api *APIError
			if errors.As(err, &api) && (api.Status == http.StatusUnauthorized || api.Status == http.StatusForbidden || strings.Contains(strings.ToLower(api.Message), "pin")) {
				return "", fmt.Errorf("%w: %v", ErrHomePINIncorrect, err)
			}
			return "", homeSwitchError(err)
		}
		// Some Plex responses say only "Unauthorized" for a protected member.
		// Look up PIN protection only after a refused switch, never on success.
		var api *APIError
		if errors.As(err, &api) && (api.Status == http.StatusUnauthorized || api.Status == http.StatusForbidden) {
			if users, lookupErr := c.HomeUsers(ctx, ownerToken); lookupErr == nil {
				for _, member := range users {
					if member.ID == id && member.Protected {
						return "", fmt.Errorf("%w: %v", ErrHomePINRequired, err)
					}
				}
			}
		}
		return "", homeSwitchError(err)
	}
	root, err := parseXMLNode(res.body)
	if err != nil {
		return "", homeSwitchError(err)
	}
	token := root.attr("authenticationToken", "authToken")
	if token == "" {
		// Some responses nest the user element.
		if u := root.find("User"); u != nil {
			token = u.attr("authenticationToken", "authToken")
		}
	}
	if token == "" {
		return "", ErrPlexRefused
	}
	account, err := c.User(ctx, token)
	if err != nil {
		return "", homeSwitchError(err)
	}
	if account.ID != id {
		return "", ErrPlexRefused
	}
	return token, nil
}

// homeSwitchError preserves a typed refusal without confusing it with a shared friend.
func homeSwitchError(err error) error {
	var api *APIError
	if errors.As(err, &api) && strings.Contains(strings.ToLower(api.Message), "pin") {
		return fmt.Errorf("%w: %v", ErrHomePINRequired, err)
	}
	return fmt.Errorf("%w: %v", ErrPlexRefused, err)
}
