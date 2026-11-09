package music

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/store"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

const addonRegistryURL = "https://eclipsemusic.app/addonstore/registry.json"

type AddonListing struct {
	CommunityAddon
	Listed  bool `json:"listed"`
	Changed bool `json:"changed"`
}
type AddonSnapshot struct {
	Addons    []AddonListing `json:"addons"`
	UpdatedAt time.Time      `json:"updatedAt"`
	CheckedAt time.Time      `json:"checkedAt"`
	Error     string         `json:"error,omitempty"`
}

// AddonRegistry only updates public setup links. It never reads or writes
// personal manifests. Its last successful snapshot survives transient errors.
type AddonRegistry struct {
	db     *sql.DB
	mu     sync.Mutex
	state  AddonSnapshot
	busy   bool
	client *http.Client
}

func init() {
	store.Register("pse_music_addon_registry", 1, `CREATE TABLE pse_music_addon_registry (id INTEGER PRIMARY KEY CHECK(id=1), snapshot TEXT NOT NULL);`)
}

// Load is called once during runtime initialization, before serving requests.
func (r *AddonRegistry) Load(ctx context.Context, db *sql.DB) error {
	r.db = db
	if db == nil {
		return nil
	}
	var raw string
	err := db.QueryRowContext(ctx, "SELECT snapshot FROM pse_music_addon_registry WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var saved AddonSnapshot
	if err = json.Unmarshal([]byte(raw), &saved); err != nil {
		return err
	}
	// Reconcile persisted entries with the current built-in allowlist.
	var addons []AddonListing
	for _, a := range CommunityAddons() {
		item := AddonListing{CommunityAddon: a}
		for _, old := range saved.Addons {
			if old.ID == a.ID && publicSetupURL(old.SetupURL) {
				item.SetupURL = old.SetupURL
				item.Listed = old.Listed
				item.Changed = old.SetupURL != a.SetupURL
			}
		}
		addons = append(addons, item)
	}
	saved.Addons = addons
	r.state = saved
	return nil
}

func (r *AddonRegistry) Snapshot() AddonSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.state
	s.Addons = append([]AddonListing(nil), s.Addons...)
	if len(s.Addons) == 0 {
		for _, a := range CommunityAddons() {
			s.Addons = append(s.Addons, AddonListing{CommunityAddon: a})
		}
	}
	return s
}

func publicSetupURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || !strings.Contains(h, ".") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".localhost") {
		return false
	}
	if ip := net.ParseIP(h); ip != nil {
		return false
	}
	return u.Port() == "" || u.Port() == "443"
}

func parseAddonRegistry(body io.Reader) ([]AddonListing, error) {
	var registry struct {
		Addons []struct {
			ID       string `json:"id"`
			SetupURL string `json:"setupUrl"`
		} `json:"addons"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&registry); err != nil {
		return nil, err
	}
	if len(registry.Addons) == 0 {
		return nil, errors.New("empty registry")
	}
	seen := map[string]string{}
	for _, a := range registry.Addons {
		if _, ok := seen[a.ID]; ok {
			return nil, errors.New("duplicate ID")
		}
		seen[a.ID] = a.SetupURL
	}
	var out []AddonListing
	for _, a := range CommunityAddons() {
		item := AddonListing{CommunityAddon: a}
		if raw, ok := seen[a.ID]; ok {
			if !publicSetupURL(raw) {
				return nil, errors.New("invalid setup link")
			}
			item.Listed = true
			item.Changed = raw != a.SetupURL
			item.SetupURL = raw
		}
		out = append(out, item)
	}
	return out, nil
}

// Refresh limits manual retries too; concurrent calls cannot start extra requests.
func (r *AddonRegistry) Refresh(ctx context.Context) {
	r.mu.Lock()
	if r.busy || time.Since(r.state.CheckedAt) < time.Minute {
		r.mu.Unlock()
		return
	}
	r.busy = true
	r.state.CheckedAt = time.Now().UTC()
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, addonRegistryURL, nil)
	req.Header.Set("User-Agent", "Ether-Remix/1.0 (+https://github.com/Aerya/Ether-Remix)")
	req.Header.Set("Accept", "application/json")
	hc := r.client
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := hc.Do(req)
	var addons []AddonListing
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			err = errors.New("registry HTTP failure")
		} else {
			addons, err = parseAddonRegistry(resp.Body)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.busy = false
	if err != nil {
		r.state.Error = "Registre indisponible : derniers liens connus conservés."
		return
	}
	for i := range addons {
		if !addons[i].Listed {
			for _, old := range r.state.Addons {
				if old.ID == addons[i].ID {
					addons[i].SetupURL = old.SetupURL
					addons[i].Changed = old.Changed
				}
			}
		}
	}
	r.state.Addons = addons
	r.state.UpdatedAt = time.Now().UTC()
	r.state.Error = ""
	if r.db != nil {
		raw, _ := json.Marshal(r.state)
		if _, err := r.db.ExecContext(ctx, "INSERT INTO pse_music_addon_registry(id,snapshot) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET snapshot=excluded.snapshot", string(raw)); err != nil {
			r.state.Error = "Liens actualisés, mais sauvegarde du registre impossible."
		}
	}
}

func (r *AddonRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if req.Method == http.MethodPost {
		r.Refresh(req.Context())
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(r.Snapshot())
}

func (r *AddonRegistry) Run(ctx context.Context, enabled func() bool) {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		// Check frequently for an activation, fetch at most every six hours.
		if enabled() && (pseenv.Get("PSE_MUSIC_ADDON_REGISTRY") == "" || on(pseenv.Get("PSE_MUSIC_ADDON_REGISTRY"))) && time.Since(r.Snapshot().CheckedAt) >= 6*time.Hour {
			r.Refresh(ctx)
		}
		timer.Reset(time.Minute)
	}
}
