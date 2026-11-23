package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/frontends/vela"
	"github.com/braedonsaunders/ether/internal/httpclient"
	"github.com/braedonsaunders/ether/internal/safefetch"
)

func (a *App) velaSkinsStore() *vela.SkinStore {
	a.velaSkinsOnce.Do(func() {
		a.velaSkins = vela.NewSkinStore(a.db, filepath.Join(a.opts.DataDir, "vela-skins"))
	})
	return a.velaSkins
}

func (a *App) velaSkinsAdmin() admin.VelaSkinAdmin {
	return velaSkinsService{app: a}
}

func (a *App) velaSkinFetchClient() *http.Client {
	if a.velaSkinHTTP != nil {
		return a.velaSkinHTTP
	}
	return (&safefetch.Guard{}).Client(egress.Client(egress.CapPlugins, 30*time.Second))
}

type velaSkinsService struct {
	app *App
}

func toAdminVelaSkin(s vela.SkinSummary) admin.VelaSkin {
	swatches := s.Swatches
	if swatches == nil {
		swatches = []string{}
	}
	return admin.VelaSkin{
		ID:          s.ID,
		Name:        s.Name,
		Version:     s.Version,
		Kind:        s.Kind,
		Builtin:     s.Builtin,
		Preview:     s.Preview,
		Swatches:    swatches,
		InstalledAt: s.InstalledAt,
	}
}

func mapVelaSkinErr(err error) error {
	switch {
	case errors.Is(err, vela.ErrBuiltinSkin):
		msg := strings.TrimPrefix(err.Error(), vela.ErrBuiltinSkin.Error()+": ")
		return &admin.VelaSkinValidationError{
			Code:    "builtin_skin",
			Message: msg,
			Status:  http.StatusBadRequest,
		}
	case errors.Is(err, vela.ErrSkinNotFound):
		return &admin.VelaSkinValidationError{
			Code:    "not_found",
			Message: "Skin not found.",
			Status:  http.StatusNotFound,
		}
	case errors.Is(err, vela.ErrSkinInvalid):
		msg := strings.TrimPrefix(err.Error(), vela.ErrSkinInvalid.Error()+": ")
		return &admin.VelaSkinValidationError{
			Code:    "invalid_skin",
			Message: msg,
			Status:  http.StatusBadRequest,
		}
	default:
		return err
	}
}

func (s velaSkinsService) bumpAppearanceRevision(removedID string) error {
	if s.app.cfg == nil {
		return nil
	}
	return s.app.cfg.Update(func(c *config.Config) error {
		if c.Frontends == nil {
			c.Frontends = map[string]config.Frontend{}
			c.Frontends["plex"] = config.Frontend{Enabled: true}
		}
		v := c.Frontends["vela"]
		if removedID != "" && v.Settings != nil {
			if cur, _ := v.Settings["defaultSkinId"].(string); cur == removedID {
				cp := make(map[string]any, len(v.Settings))
				for k, val := range v.Settings {
					cp[k] = val
				}
				cp["defaultSkinId"] = "cinematic"
				v.Settings = cp
			}
		}
		if v.AppearanceRevision >= 9007199254740991 {
			return errors.New("appearance revision exhausted")
		}
		v.AppearanceRevision++
		c.Frontends["vela"] = v
		return nil
	})
}

func (s velaSkinsService) ListSkins(ctx context.Context) ([]admin.VelaSkin, error) {
	raw := s.app.velaSkinsStore().List(ctx, filepath.Join(s.app.frontendClientsDir(), "vela"))
	out := make([]admin.VelaSkin, len(raw))
	for i, item := range raw {
		out[i] = toAdminVelaSkin(item)
	}
	return out, nil
}

func (s velaSkinsService) InstallSkinArchive(ctx context.Context, pkg []byte) (admin.VelaSkin, error) {
	installed, err := s.app.velaSkinsStore().InstallArchive(ctx, pkg)
	if err != nil {
		return admin.VelaSkin{}, mapVelaSkinErr(err)
	}
	if err := s.bumpAppearanceRevision(""); err != nil {
		return admin.VelaSkin{}, err
	}
	return toAdminVelaSkin(installed), nil
}

func (s velaSkinsService) InstallSkinURL(ctx context.Context, rawURL string, lan bool) (admin.VelaSkin, error) {
	trimmed := strings.TrimSpace(rawURL)
	u, err := url.Parse(trimmed)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return admin.VelaSkin{}, &admin.VelaSkinValidationError{
			Code:    "invalid_url",
			Message: "Enter a valid http:// or https:// .vskin URL.",
			Status:  http.StatusBadRequest,
		}
	}
	if ip, ipErr := netip.ParseAddr(strings.Trim(u.Hostname(), "[]")); ipErr == nil {
		ip = ip.Unmap()
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return admin.VelaSkin{}, &admin.VelaSkinValidationError{
				Code:    "ssrf_blocked",
				Message: "Link-local and metadata addresses are not allowed.",
				Status:  http.StatusBadRequest,
			}
		}
	}

	fetchCtx := ctx
	if lan {
		fetchCtx = safefetch.WithTrustedOrigin(fetchCtx, trimmed)
	}
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, trimmed, nil)
	if err != nil {
		return admin.VelaSkin{}, &admin.VelaSkinValidationError{
			Code:    "invalid_url",
			Message: "Enter a valid http:// or https:// .vskin URL.",
			Status:  http.StatusBadRequest,
		}
	}
	req.Header.Set("User-Agent", httpclient.FormatUserAgent(s.app.opts.Version))

	client := *s.app.velaSkinFetchClient()
	priorRedirect := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		next.Header.Del("Referer")
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if priorRedirect != nil {
			return priorRedirect(next, via)
		}
		return nil
	}

	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, safefetch.ErrBlocked) {
			return admin.VelaSkin{}, &admin.VelaSkinValidationError{
				Code:    "ssrf_blocked",
				Message: "Private-network URLs are blocked unless local network access is allowed.",
				Status:  http.StatusBadRequest,
			}
		}
		return admin.VelaSkin{}, &admin.VelaSkinValidationError{
			Code:    "fetch_failed",
			Message: "Failed to download .vskin from the provided URL.",
			Status:  http.StatusBadRequest,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return admin.VelaSkin{}, &admin.VelaSkinValidationError{
			Code:    "fetch_failed",
			Message: fmt.Sprintf("Skin download returned HTTP %d.", resp.StatusCode),
			Status:  http.StatusBadRequest,
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, vela.MaxSkinArchiveBytes+1))
	if err != nil {
		return admin.VelaSkin{}, &admin.VelaSkinValidationError{
			Code:    "fetch_failed",
			Message: "Failed to read downloaded .vskin archive.",
			Status:  http.StatusBadRequest,
		}
	}
	if len(body) > vela.MaxSkinArchiveBytes {
		return admin.VelaSkin{}, &admin.VelaSkinValidationError{
			Code:    "too_large",
			Message: "Skin archive exceeds the 8 MB limit.",
			Status:  http.StatusBadRequest,
		}
	}

	return s.InstallSkinArchive(ctx, body)
}

func (s velaSkinsService) RemoveSkin(ctx context.Context, id string) error {
	if err := s.app.velaSkinsStore().Remove(ctx, id); err != nil {
		return mapVelaSkinErr(err)
	}
	return s.bumpAppearanceRevision(id)
}
