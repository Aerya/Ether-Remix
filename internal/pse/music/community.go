package music

import (
	"context"
	"net/http"
	"strings"
)

// CommunityAddon identifies a provider independently of its current host.
// Setup URLs are public landing pages, never personal manifests or credentials.
type CommunityAddon struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	Name     string `json:"name"`
	SetupURL string `json:"setupUrl"`
	Account  bool   `json:"account"`
}

// Order favours potentially lossless sources before public SoundCloud audio.
func CommunityAddons() []CommunityAddon {
	return []CommunityAddon{
		{"com.eclipse.community.tido", "PSE_MUSIC_TIDO_URL", "Tido", "https://tido.tido.tido.rickyaddons.dpdns.org/", false},
		{"com.eclipse.community.internetarchive", "PSE_MUSIC_INTERNETARCHIVE_URL", "Internet Archive", "https://internetarchive.rickyaddons.dpdns.org/", false},
		{"com.eclipse.community.ultramax", "PSE_MUSIC_ULTRAMAX_URL", "UltraMax", "https://ultramax.vip/eclipse-info.html", true},
		{"accent.community.deezer.ricky", "PSE_MUSIC_DEEZER_RICKY_URL", "Deezer · Ricky", "https://deezer.rickyaddons.dpdns.org/", true},
		{"com.eclipse.community.soundcloud.eclipse", "PSE_MUSIC_SOUNDCLOUD_URL", "SoundCloud · Eclipse", "https://sc.eclipsemusic.app/", false},
		{"com.eclipse.community.soundcloud", "PSE_MUSIC_SOUNDCLOUD_RICKY_URL", "SoundCloud · Ricky", "https://eclipse3.cyrusna29.workers.dev/", false},
	}
}

func IsCommunityKey(key string) bool {
	for _, a := range CommunityAddons() {
		if a.Key == key {
			return true
		}
	}
	return false
}

func (s Settings) hasCommunity() bool {
	for _, u := range s.CommunityURLs {
		if strings.TrimSpace(u) != "" {
			return true
		}
	}
	return false
}

// eclipseSources is shared by playback and diagnostics so their order agrees.
func (s Settings) eclipseSources(hc *http.Client) []namedResolver {
	var out []namedResolver
	seen := map[string]bool{}
	add := func(name, kind, raw string) {
		for _, u := range ParseEclipseURLs(raw) {
			if seen[u] {
				continue
			}
			seen[u] = true
			e := NewEclipse([]string{u}, hc)
			out = append(out, namedResolver{name, kind, namedAddon{name: name, resolver: e}})
		}
	}
	for _, u := range ParseEclipseURLs(s.EclipseURLs) {
		add("Addon Eclipse · "+addonName(u), "eclipse", u)
	}
	for i, a := range CommunityAddons() {
		add(a.Name, "eclipse", s.CommunityURLs[i])
	}
	return out
}

type namedAddon struct {
	name     string
	resolver Resolver
}

func (a namedAddon) Resolve(ctx context.Context, t Track) (Stream, error) {
	s, err := a.resolver.Resolve(ctx, t)
	if err == nil {
		s.Provider = a.name
	}
	return s, err
}
