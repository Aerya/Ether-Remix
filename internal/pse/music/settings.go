package music

import (
	"strings"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// Settings regroupe la configuration PSE_MUSIC_*.
type Settings struct {
	// EclipseURLs : PSE_MUSIC_ECLIPSE_URL, un ou plusieurs liens manifest
	// d'addons Eclipse (séparés par des espaces). Catalogue Deezer, sans
	// compte : le mode de Remux, prioritaire.
	EclipseURLs   string
	CommunityURLs [6]string // Personal manifests; blank disables the provider.
	HiFiURL       string    // PSE_MUSIC_HIFI_URL : HiFi API, en option
	Quality       string    // PSE_MUSIC_QUALITY : qualité HiFi API
	CoverBase     string    // PSE_MUSIC_COVERS_URL : pochettes HiFi API
	// Debrid : PSE_MUSIC_ALLDEBRID, lecture depuis le compte AllDebrid
	// d'Ether (albums déjà en cache, trouvés sur ses indexeurs torrent).
	Debrid     bool
	RealDebrid bool
	DMM        bool // Catalogue DMM, compte Real-Debrid existant.
	TorBox     bool
	Audius     string // off, first or last; public MP3 fallback
}

// Enabled indique si l'API Subsonic doit être montée.
func (s Settings) Enabled() bool {
	return (s.EclipseURLs != "" || s.hasCommunity()) || s.HiFiURL != "" || (s.Debrid || s.RealDebrid || s.TorBox || s.DMM) || s.audiusEnabled()
}

// Mode renvoie la source utilisée : "eclipse", "alldebrid",
// "eclipse+alldebrid", "debrid", "audius" (éventuellement combiné), "hifi" ou "".
func (s Settings) Mode() string {
	switch {
	case s.RealDebrid || s.TorBox || s.DMM:
		return "debrid"
	case s.audiusEnabled():
		return "audius"
	case (s.EclipseURLs != "" || s.hasCommunity()) && s.Debrid:
		return "eclipse+alldebrid"
	case s.EclipseURLs != "" || s.hasCommunity():
		return "eclipse"
	case s.Debrid:
		return "alldebrid"
	case s.HiFiURL != "":
		return "hifi"
	}
	return ""
}

// LoadSettings lit l'environnement.
func LoadSettings() Settings {
	s := Settings{
		EclipseURLs: strings.Join(ParseEclipseURLs(pseenv.Get("PSE_MUSIC_ECLIPSE_URL")), " "),
		HiFiURL:     strings.TrimSpace(pseenv.Get("PSE_MUSIC_HIFI_URL")),
		Quality:     strings.ToUpper(strings.TrimSpace(pseenv.Get("PSE_MUSIC_QUALITY"))),
		CoverBase:   strings.TrimSpace(pseenv.Get("PSE_MUSIC_COVERS_URL")),
		Audius:      strings.ToLower(strings.TrimSpace(pseenv.Get("PSE_MUSIC_AUDIUS"))),
		Debrid:      on(pseenv.Get("PSE_MUSIC_ALLDEBRID")),
		RealDebrid:  on(pseenv.Get("PSE_MUSIC_REALDEBRID")),
		DMM:         on(pseenv.Get("PSE_MUSIC_DMM")),
		TorBox:      on(pseenv.Get("PSE_MUSIC_TORBOX")),
	}
	for i, a := range CommunityAddons() {
		if urls := ParseEclipseURLs(pseenv.Get(a.Key)); len(urls) == 1 {
			s.CommunityURLs[i] = urls[0]
		}
	}
	switch s.Quality {
	case "HI_RES_LOSSLESS", "LOSSLESS", "HIGH", "LOW":
	default:
		s.Quality = "LOSSLESS"
	}
	if !strings.HasPrefix(s.HiFiURL, "http://") && !strings.HasPrefix(s.HiFiURL, "https://") {
		s.HiFiURL = ""
	}
	return s
}

// NewSource construit la source correspondant aux réglages ; debrid est un
// ensemble des résolveurs debrid, filtrés par leurs activations respectives.
func (s Settings) NewSource(debrid DebridSources) Source {
	switch s.Mode() {
	case "debrid", "eclipse", "alldebrid", "eclipse+alldebrid", "audius":
		var rs Resolvers
		if s.Audius == "first" {
			rs = append(rs, NewAudius(nil))
		}
		for _, n := range s.eclipseSources(nil) {
			rs = append(rs, n.r)
		}
		for _, d := range debrid {
			if s.DebridEnabled(d.Kind) && d.Resolver != nil {
				rs = append(rs, d.Resolver)
			}
		}
		if s.Audius == "last" {
			rs = append(rs, NewAudius(nil))
		}
		return NewDeezerSource(rs, nil)
	case "hifi":
		return NewHiFiSource(NewClient(s.HiFiURL, nil), s.CoverBase)
	}
	return nil
}

func on(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "on", "1", "true", "yes", "oui":
		return true
	}
	return false
}

func (s Settings) audiusEnabled() bool { return s.Audius == "first" || s.Audius == "last" }

// DebridSources follows the configured account priorities.
type DebridSources []DebridSource
type DebridSource struct {
	Kind, Name string
	Resolver   Resolver
	// NoAccount : aucun compte actif avec clé pour ce service ; il reste
	// dans la chaîne mais ne peut rien lire.
	NoAccount bool
}

func (s Settings) DebridEnabled(kind string) bool {
	switch kind {
	case DMMSource:
		return s.DMM
	case "alldebrid":
		return s.Debrid
	case "realdebrid":
		return s.RealDebrid
	case "torbox":
		return s.TorBox
	}
	return false
}
