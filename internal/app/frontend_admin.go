package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/vela"
	"github.com/braedonsaunders/ether/internal/remote"
)

type frontendStreamCountsKey struct{}
type frontendStreamCounts struct {
	counts map[frontend.Protocol]int
	known  bool
}

// frontendControl persists desired state and reconciles it on each node.
type frontendControl struct {
	registry     *frontend.Registry
	listeners    *frontendListeners
	config       *config.Store
	db           *sql.DB
	mu           sync.Mutex
	applied      map[frontend.Protocol]listenerSettings
	clientCount  func(context.Context, frontend.Protocol) (int, bool)
	streamCounts func(context.Context) (map[frontend.Protocol]int, bool)
	sharedStatus func(frontend.Protocol, *frontend.Status)
	portChanged  func()
}
type listenerSettings struct{ Port, Discovery, ServeOnMainPort any }

func newFrontendControl(r *frontend.Registry, c *config.Store) *frontendControl {
	return &frontendControl{registry: r, listeners: newFrontendListeners(r), config: c, applied: make(map[frontend.Protocol]listenerSettings)}
}

func (f *frontendControl) UploadVelaWordmark(ctx context.Context, r io.Reader) (admin.VelaWordmark, error) {
	wm, err := vela.UploadWordmark(ctx, f.db, r)
	if err != nil {
		var valErr *vela.WordmarkValidationError
		switch {
		case errors.Is(err, vela.ErrWordmarkTooLarge):
			return admin.VelaWordmark{}, admin.ErrVelaWordmarkTooLarge
		case errors.As(err, &valErr):
			return admin.VelaWordmark{}, &admin.VelaWordmarkValidationError{Message: valErr.Message}
		default:
			return admin.VelaWordmark{}, err
		}
	}
	return admin.VelaWordmark{
		URL:    wm.URL,
		Width:  wm.Width,
		Height: wm.Height,
		Digest: wm.Digest,
	}, nil
}

func (f *frontendControl) UploadVelaIcon(ctx context.Context, r io.Reader) (admin.VelaAppIcon, error) {
	icon, err := vela.UploadAppIcon(ctx, f.db, r)
	if err != nil {
		var valErr *vela.AppIconValidationError
		switch {
		case errors.Is(err, vela.ErrAppIconTooLarge):
			return admin.VelaAppIcon{}, admin.ErrVelaAppIconTooLarge
		case errors.As(err, &valErr):
			return admin.VelaAppIcon{}, &admin.VelaAppIconValidationError{Message: valErr.Message}
		default:
			return admin.VelaAppIcon{}, err
		}
	}
	return admin.VelaAppIcon{
		URL:    icon.URL,
		Width:  icon.Width,
		Height: icon.Height,
		Digest: icon.Digest,
	}, nil
}

func (f *frontendControl) descriptor(id frontend.Protocol) (frontend.Descriptor, bool) {
	for _, d := range f.registry.Descriptors() {
		if d.ID == id {
			return d, true
		}
	}
	return frontend.Descriptor{}, false
}
func (f *frontendControl) List(ctx context.Context) ([]admin.FrontendInfo, error) {
	cfg := f.config.Get()
	var counts map[frontend.Protocol]int
	var known bool
	if f.streamCounts != nil {
		counts, known = f.streamCounts(ctx)
		ctx = context.WithValue(ctx, frontendStreamCountsKey{}, frontendStreamCounts{counts: counts, known: known})
	}
	out := make([]admin.FrontendInfo, 0)
	for _, d := range f.registry.Descriptors() {
		enabled := frontendDesired(cfg, d.ID)
		status := f.listeners.status(ctx, d.ID)
		if f.sharedStatus != nil {
			f.sharedStatus(d.ID, &status)
		}
		if f.clientCount != nil {
			status.Clients24h, status.Clients24hKnown = f.clientCount(ctx, d.ID)
		}
		if known {
			status.LiveStreams, status.LiveStreamsKnown = counts[d.ID], true
		}
		if frontendHostPolicy(d.ID).MainListener {
			status.PublishedAddresses = validPlexCustomURLs(cfg.Plex.CustomURLs)
		} else if frontendHostPolicy(d.ID).PublicURL {
			status.PublishedAddresses = validPublishedAddresses(cfg.Frontends[string(d.ID)].EffectivePublishedAddresses())
		}
		out = append(out, admin.FrontendInfo{Descriptor: d, Enabled: enabled, Status: status})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return frontendRank(out[i].Descriptor.ID) < frontendRank(out[j].Descriptor.ID)
	})
	return out, nil
}

// Household order for the setup chooser and the Apps page; protocol names are UI identities.
func frontendRank(id frontend.Protocol) int {
	switch id {
	case "plex":
		return 0
	case "jellyfin":
		return 1
	case "emby":
		return 2
	case "stremio":
		return 3
	case "silo":
		return 4
	case "dlna":
		return 5
	case "webdav":
		return 6
	case "m3u":
		return 7
	default:
		return 8
	}
}
func (f *frontendControl) Settings(_ context.Context, id frontend.Protocol) (frontend.Settings, error) {
	if _, ok := f.descriptor(id); !ok {
		return nil, frontend.ErrNotRegistered
	}
	cfg := f.config.Read()
	if frontendHostPolicy(id).MainListener {
		return frontend.Settings{"port": float64(cfg.Server.Port)}, nil
	}
	settings := frontend.Settings{}
	for k, v := range cfg.Frontends[string(id)].Settings {
		settings[k] = v
	}
	if id == "vela" {
		settings = vela.NormalizeSettings(settings)
	}
	if frontendHostPolicy(id).PublicURL {
		if published := validPublishedAddresses(cfg.Frontends[string(id)].EffectivePublishedAddresses()); len(published) > 0 {
			settings["publishedAddresses"] = published
		}
	}
	if port := cfg.FrontendPorts[string(id)]; port > 0 {
		settings["port"] = float64(port)
	} else if id != "vela" {
		if port, ok := frontend.DefaultPort(id); ok {
			settings["port"] = float64(port)
		}
	}
	return settings, nil
}
func validateFrontendSettings(s frontend.Settings, protocolValidator bool) error {
	for k, v := range s {
		switch k {
		case "port":
			n, ok := v.(float64)
			if !ok || n < 1 || n > 65535 || math.Trunc(n) != n {
				return errors.New("invalid port")
			}
		case "discovery", "quickConnect", "publicViewerPicker", "serveOnMainPort":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("invalid %s", k)
			}
		default:
			if !protocolValidator {
				return fmt.Errorf("unknown setting %s", k)
			}
		}
	}
	return nil
}
func (f *frontendControl) PutSettings(ctx context.Context, id frontend.Protocol, s frontend.Settings) error {
	descriptor, ok := f.descriptor(id)
	if !ok {
		return frontend.ErrNotRegistered
	}
	s = withoutServerName(s)
	// publishedAddresses (and legacy publicUrl) is a host-owned setting stored
	// on config.Frontend, like the port; keep it out of the protocol's own
	// settings and validation. Plex does not take one (it keeps Plex.CustomURLs),
	// so leave it in place there and let the Plex branch below reject it.
	var publishedAddresses []string
	hasPublishedAddresses := false
	if frontendHostPolicy(id).PublicURL {
		var err error
		s, publishedAddresses, hasPublishedAddresses, err = extractPublishedAddresses(s)
		if err != nil {
			return err
		}
	}
	// The LAN viewer is host-owned, independent of a plugin's settings schema.
	protocolSettings := s
	if descriptor.Auth == "none_lan" {
		protocolSettings = make(frontend.Settings, len(s))
		for key, value := range s {
			if key == "viewerId" {
				n, ok := value.(float64)
				if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 1 || n > 9007199254740991 || math.Trunc(n) != n {
					return errors.New("invalid viewerId")
				}
				continue
			}
			protocolSettings[key] = value
		}
	}
	supported, err := f.registry.ValidateSettings(id, protocolSettings)
	if err != nil {
		return err
	}
	if err := validateFrontendSettings(protocolSettings, supported); err != nil {
		return err
	}
	if id == "vela" {
		s = vela.NormalizeSettings(s)
		if port, ok := s["port"].(float64); ok && int(port) == f.config.Get().Server.Port {
			return errors.New("dedicated port must be different from the main server port")
		}
	}
	if frontendHostPolicy(id).MainListener {
		for key := range s {
			if key != "port" {
				return fmt.Errorf("unsupported Plex setting %s", key)
			}
		}
		if os.Getenv("ETHER_PORT") != "" {
			return errors.New("ETHER_PORT controls the listening port")
		}
		oldPort := f.config.Get().Server.Port
		if port, ok := s["port"].(float64); ok && int(port) != oldPort {
			ln, err := net.Listen("tcp", net.JoinHostPort(f.config.Get().Server.BindAddress, strconv.Itoa(int(port))))
			if err != nil {
				return fmt.Errorf("listening port is unavailable: %w", err)
			}
			ln.Close()
		}
		err := f.config.Update(func(c *config.Config) error {
			if port, ok := s["port"].(float64); ok {
				c.Server.Port = int(port)
				c.Server.PortExplicit = true
			}
			return nil
		})
		if err == nil && oldPort != f.config.Get().Server.Port && f.portChanged != nil {
			f.portChanged()
		}
		return err
	}
	err = f.config.Update(func(c *config.Config) error {
		if c.Frontends == nil {
			c.Frontends = map[string]config.Frontend{}
			c.Frontends["plex"] = config.Frontend{Enabled: true} // Preserve the default main-listener registration.
		}
		v := c.Frontends[string(id)]
		if id == "vela" && vela.AppearanceChanged(v.Settings, s) {
			if v.AppearanceRevision >= 9007199254740991 {
				return errors.New("appearance revision exhausted")
			}
			v.AppearanceRevision++
		}
		if id == "vela" && vela.CapabilitiesChanged(v.Settings, s) {
			if v.CapabilitiesRevision >= 9007199254740991 {
				return errors.New("capabilities revision exhausted")
			}
			v.CapabilitiesRevision++
		}
		// Only a present publishedAddresses/publicUrl changes the stored list;
		// a settings save that never sent it must not clear the owner's list.
		// A present empty list clears it.
		if hasPublishedAddresses {
			v.PublishedAddresses = publishedAddresses
			v.PublicURL = ""
		}
		_, hasPort := s["port"].(float64)
		v.Settings = make(map[string]any, len(s))
		for k, val := range s {
			if k == "port" {
				continue
			}
			if id == "vela" && k == "serveOnMainPort" && !hasPort {
				continue
			}
			v.Settings[k] = val
		}
		if c.FrontendPorts == nil {
			c.FrontendPorts = make(map[string]int)
		}
		if port, ok := s["port"].(float64); ok {
			c.FrontendPorts[string(id)] = int(port)
		} else {
			delete(c.FrontendPorts, string(id))
		}
		c.Frontends[string(id)] = v
		return nil
	})
	if err == nil && id == "vela" && f.db != nil {
		vela.PruneWordmarks(ctx, f.db, s)
		vela.PruneAppIcons(ctx, f.db, s)
	}
	return err
}
func (f *frontendControl) Toggle(_ context.Context, id frontend.Protocol, enabled bool) error {
	if _, ok := f.descriptor(id); !ok {
		return frontend.ErrNotRegistered
	}
	return f.config.Update(func(c *config.Config) error {
		if c.Frontends == nil {
			c.Frontends = map[string]config.Frontend{}
			if !frontendHostPolicy(id).MainListener {
				c.Frontends["plex"] = config.Frontend{Enabled: true} // Preserve the default main-listener registration.
			}
		}
		v := c.Frontends[string(id)]
		v.Enabled = enabled
		c.Frontends[string(id)] = v
		return nil
	})
}

// Reconcile is called on startup and after a shared configuration update.
func (f *frontendControl) Reconcile(ctx context.Context, c config.Config) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.registry.Descriptors() {
		desired := frontendDesired(c, d.ID)
		settings := c.Frontends[string(d.ID)].Settings
		current := listenerSettings{
			Port:            c.FrontendPorts[string(d.ID)],
			Discovery:       settings["discovery"],
			ServeOnMainPort: settings["serveOnMainPort"],
		}
		_, mounted := f.registry.Instance(d.ID)
		if desired && mounted && !reflect.DeepEqual(f.applied[d.ID], current) {
			_ = f.listeners.stop(ctx, d.ID)
			_, mounted = f.registry.Instance(d.ID)
		}
		if desired && !mounted {
			if f.listeners.start(ctx, d.ID) == nil {
				f.applied[d.ID] = current
			}
		} else if !desired && mounted {
			if f.listeners.stop(ctx, d.ID) == nil {
				delete(f.applied, d.ID)
			}
		}
	}
}

const maxPublishedAddresses = 16

// extractPublishedAddresses removes publishedAddresses (or legacy publicUrl)
// from settings and returns the normalized, deduplicated list. Present reports
// whether either key was supplied.
func extractPublishedAddresses(s frontend.Settings) (out frontend.Settings, values []string, present bool, err error) {
	rawList, hasList := s["publishedAddresses"]
	rawLegacy, hasLegacy := s["publicUrl"]
	if !hasList && !hasLegacy {
		return s, nil, false, nil
	}
	if hasList {
		var rawItems []string
		switch v := rawList.(type) {
		case nil:
			// Clearing the list.
		case []string:
			rawItems = v
		case []any:
			rawItems = make([]string, 0, len(v))
			for _, item := range v {
				text, ok := item.(string)
				if !ok {
					return s, nil, true, errors.New("invalid published address")
				}
				rawItems = append(rawItems, text)
			}
		default:
			return s, nil, true, errors.New("invalid published addresses")
		}
		seen := make(map[string]struct{}, len(rawItems))
		for _, item := range rawItems {
			normalized, parseErr := remote.ParsePublishedURL(item)
			if parseErr != nil {
				return s, nil, true, errors.New("invalid published address")
			}
			if _, dup := seen[normalized]; dup {
				continue
			}
			seen[normalized] = struct{}{}
			values = append(values, normalized)
			if len(values) > maxPublishedAddresses {
				return s, nil, true, errors.New("too many published addresses")
			}
		}
	} else {
		text, ok := rawLegacy.(string)
		if !ok {
			return s, nil, true, errors.New("invalid published address")
		}
		text = strings.TrimSpace(text)
		if text != "" {
			normalized, parseErr := remote.ParsePublishedURL(text)
			if parseErr != nil {
				return s, nil, true, errors.New("invalid published address")
			}
			values = []string{normalized}
		}
	}
	out = make(frontend.Settings, len(s))
	for k, v := range s {
		if k != "publishedAddresses" && k != "publicUrl" {
			out[k] = v
		}
	}
	return out, values, true, nil
}

func validPublishedAddresses(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, entry := range raw {
		normalized, err := remote.ParsePublishedURL(entry)
		if err != nil {
			continue
		}
		if _, dup := seen[normalized]; dup {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validPlexCustomURLs(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, entry := range raw {
		c, err := remote.ParseCustomURL(strings.TrimSpace(entry))
		if err != nil {
			continue
		}
		uri := strings.TrimRight(c.URI, "/")
		key := strings.ToLower(uri)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, uri)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func withoutServerName(s frontend.Settings) frontend.Settings {
	if _, ok := s["serverName"]; !ok {
		return s
	}
	out := make(frontend.Settings, len(s))
	for k, v := range s {
		if k != "serverName" {
			out[k] = v
		}
	}
	return out
}

var _ admin.FrontendAdmin = (*frontendControl)(nil)
var _ admin.VelaWordmarkUploader = (*frontendControl)(nil)

// frontendDesired reports whether an app should be served. An explicit
// entry wins. Without one, main-listener apps are on for an install that
// never chose its apps, and Vela, Ether's own app, is on unless the owner
// turned it off.
func frontendDesired(c config.Config, id frontend.Protocol) bool {
	if fe, ok := c.Frontends[string(id)]; ok {
		return fe.Enabled
	}
	if id == "vela" {
		return true
	}
	return c.Frontends == nil && frontendHostPolicy(id).MainListener
}
