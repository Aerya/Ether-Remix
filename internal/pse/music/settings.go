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
	DMMURL     string
	DMM        bool // Catalogue DMM, compte Real-Debrid existant.
	TorBox     bool
	// Torrent : client BitTorrent de Remix (PSE_TORRENT et PSE_TORRENT_MUSIC).
	Torrent bool
	Audius  string // off, first or last; public MP3 fallback
	// OpenDeezer : lecture directe avec la session Deezer (ARL) du
	// compte de l'administrateur. Désactivé par défaut ; OpenDeezerOrder :
	// "last" (secours après les autres sources, défaut) ou "first".
	OpenDeezer      bool
	OpenDeezerARL   string
	OpenDeezerOrder string
}

// TorrentSource est la source « client torrent » de Remix.
const TorrentSource = "torrent"

// Enabled indique si l'API Subsonic doit être montée.
func (s Settings) Enabled() bool {
	return (s.EclipseURLs != "" || s.hasCommunity()) || s.HiFiURL != "" || (s.Debrid || s.RealDebrid || s.TorBox || s.DMM || s.Torrent) || s.audiusEnabled() || s.openDeezerEnabled()
}

// Mode renvoie la source utilisée : "eclipse", "alldebrid",
// "eclipse+alldebrid", "debrid", "audius" (éventuellement combiné), "hifi" ou "".
func (s Settings) Mode() string {
	switch {
	case s.RealDebrid || s.TorBox || s.DMM || s.Torrent:
		return "debrid"
	case s.audiusEnabled():
		return "audius"
	case s.openDeezerEnabled() && s.EclipseURLs == "" && !s.hasCommunity() && !s.Debrid:
		return "opendeezer"
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
		EclipseURLs:     strings.Join(ParseEclipseURLs(pseenv.Get("PSE_MUSIC_ECLIPSE_URL")), " "),
		HiFiURL:         strings.TrimSpace(pseenv.Get("PSE_MUSIC_HIFI_URL")),
		Quality:         strings.ToUpper(strings.TrimSpace(pseenv.Get("PSE_MUSIC_QUALITY"))),
		CoverBase:       strings.TrimSpace(pseenv.Get("PSE_MUSIC_COVERS_URL")),
		Audius:          strings.ToLower(strings.TrimSpace(pseenv.Get("PSE_MUSIC_AUDIUS"))),
		Debrid:          on(pseenv.Get("PSE_MUSIC_ALLDEBRID")),
		OpenDeezer:      on(pseenv.Get("PSE_MUSIC_OPENDEEZER")),
		OpenDeezerARL:   strings.TrimSpace(pseenv.Get("PSE_MUSIC_OPENDEEZER_ARL")),
		OpenDeezerOrder: strings.ToLower(strings.TrimSpace(pseenv.Get("PSE_MUSIC_OPENDEEZER_ORDER"))),
		RealDebrid:      on(pseenv.Get("PSE_MUSIC_REALDEBRID")),
		DMMURL:          pseenv.Get("PSE_MUSIC_DMM_URL"),
		DMM:             on(pseenv.Get("PSE_MUSIC_DMM")),
		TorBox:          on(pseenv.Get("PSE_MUSIC_TORBOX")),
		Torrent: on(pseenv.Get("PSE_TORRENT")) && strings.TrimSpace(pseenv.Get("PSE_TORRENT_URL")) != "" &&
			!off(pseenv.Get("PSE_TORRENT_MUSIC")),
	}
	for i, a := range CommunityAddons() {
		if urls := ParseEclipseURLs(pseenv.Get(a.Key)); len(urls) == 1 {
			s.CommunityURLs[i] = urls[0]
		}
	}
	if s.OpenDeezerOrder != "first" {
		s.OpenDeezerOrder = "last"
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
	case "debrid", "eclipse", "alldebrid", "eclipse+alldebrid", "audius", "opendeezer":
		var rs Resolvers
		if s.openDeezerEnabled() && s.OpenDeezerOrder == "first" {
			rs = append(rs, NewOpenDeezer(s.OpenDeezerARL, nil))
		}
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
		if s.openDeezerEnabled() && s.OpenDeezerOrder != "first" {
			rs = append(rs, NewOpenDeezer(s.OpenDeezerARL, nil))
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

func off(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "0", "false", "no", "non":
		return true
	}
	return false
}

// openDeezerEnabled : option activée ET session renseignée.
func (s Settings) openDeezerEnabled() bool { return s.OpenDeezer && s.OpenDeezerARL != "" }

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
	case TorrentSource:
		return s.Torrent
	}
	return false
}
