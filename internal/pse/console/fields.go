package console

import "github.com/braedonsaunders/ether/internal/pse/music"

// Text est un libellé bilingue ; la page choisit selon la langue de la console.
type Text struct {
	FR string `json:"fr"`
	EN string `json:"en"`
}

func tx(fr, en string) Text { return Text{FR: fr, EN: en} }

// Types de réglage.
const (
	KindToggle    = "toggle"
	KindNumber    = "number"
	KindDuration  = "duration"
	KindSelect    = "select"
	KindMulti     = "multi"
	KindCountries = "countries"
	KindFilter    = "filter"
	KindURL       = "url"
	KindKey       = "key" // clé d'API, masquée à l'affichage
)

// Field décrit un réglage, nommé comme sa variable d'environnement.
type Field struct {
	Key         string   `json:"key"`
	Kind        string   `json:"kind"`
	Label       Text     `json:"label"`
	Help        Text     `json:"help"`
	Default     string   `json:"default"`
	Placeholder string   `json:"placeholder,omitempty"`
	Min         *int     `json:"min,omitempty"`
	Max         *int     `json:"max,omitempty"`
	Options     []Option `json:"-"`
	// List : plusieurs liens séparés par des espaces (KindURL).
	List bool `json:"list,omitempty"`
	// Secret : le chemin du lien contient un jeton, masqué à l'affichage.
	Secret bool `json:"secret,omitempty"`
	// Advanced : rangé sous « Options avancées », replié par défaut.
	Advanced  bool `json:"advanced,omitempty"`
	Community bool `json:"community,omitempty"`
}

// Feature regroupe les réglages d'un ajout.
type Feature struct {
	ID          string
	Title       Text
	Description Text
	Fields      []Field
}

func ptr(n int) *int { return &n }

var (
	chartOptions = []Option{{"FR", tx("France", "France")}, {"WW", tx("Monde", "Worldwide")}, {"US", tx("États-Unis", "United States")},
		{"UK", tx("Royaume-Uni", "United Kingdom")}, {"BE", tx("Belgique", "Belgium")}, {"CA", tx("Canada", "Canada")},
		{"DE", tx("Allemagne", "Germany")}, {"ES", tx("Espagne", "Spain")}, {"IT", tx("Italie", "Italy")},
		{"BR", tx("Brésil", "Brazil")}, {"NL", tx("Pays-Bas", "Netherlands")}, {"NG", tx("Nigeria", "Nigeria")}}
	typeOptions = []Option{{"movie", tx("Films", "Movies")}, {"show", tx("Séries", "Shows")}}
	langOptions = []Option{
		{"french", tx("Français (toute VF : FRENCH, TRUEFRENCH, VFF, VFQ, VFi, VF, VOF)", "French (any dub: FRENCH, TRUEFRENCH, VFF, VFQ, VFi, VF, VOF)")},
		{"vff", tx("VFF / TRUEFRENCH (France)", "VFF / TRUEFRENCH (France)")},
		{"vfq", tx("VFQ (Québec)", "VFQ (Québec)")},
		{"vfi", tx("VFi (internationale)", "VFi (international)")},
		{"multi", tx("MULTi", "MULTi")},
	}
)

// Features est la liste des ajouts réglables, dans l'ordre de la page.
var Features = communityFields([]Feature{
	{
		ID:          "notify",
		Title:       tx("Notifications en français", "French notifications"),
		Description: tx("Langue des notifications envoyées au propriétaire (Discord, ntfy, e-mail…).", "Language of the notifications sent to the owner (Discord, ntfy, e-mail…)."),
		Fields: []Field{
			{Key: "PSE_NOTIFY_LANGUAGE", Kind: KindSelect, Default: "fr", Label: tx("Langue", "Language"),
				Help:    tx("« Anglais » conserve les textes d'origine d'Ether.", "“English” keeps Ether's original text."),
				Options: []Option{{"fr", tx("Français", "French")}, {"en", tx("Anglais", "English")}}},
		},
	},
	{
		ID:          "french",
		Title:       tx("Catalogue en français", "French catalogue"),
		Description: tx("N'affiche que les films et séries disponibles en français : œuvre française, sortie numérique ou TV dans les pays choisis, ou release VF/MULTi indexée.", "Only lists titles available in French: French original, digital or TV release in the chosen countries, or an indexed French/MULTi release."),
		Fields: []Field{
			{Key: "PSE_FRENCH", Kind: KindToggle, Default: "off", Label: tx("Activer", "Enable"),
				Help: tx("S'applique aux listes du catalogue (accueil, bibliothèques, recommandations). La liste de suivi, les collections et la recherche restent complètes.", "Applies to catalogue lists (home, libraries, recommendations). Watchlist, collections and search stay complete.")},
			{Key: "PSE_FRENCH_COUNTRIES", Advanced: true, Kind: KindCountries, Default: "FR", Placeholder: "FR,CA", Label: tx("Pays de sortie", "Release countries"),
				Help: tx("Codes ISO séparés par des virgules. Une sortie numérique, physique ou TV dans l'un de ces pays compte. CA ajoute le Canada, pas toujours doublé en français.", "Comma-separated ISO codes. A digital, physical or TV release in one of them counts. CA adds Canada, not always dubbed in French.")},
			{Key: "PSE_FRENCH_RELEASES", Kind: KindMulti, Default: "french,multi", Options: langOptions, Label: tx("Langues de release", "Release languages"),
				Help: tx("Langues d'une release indexée qui suffisent à garder un titre (nécessite l'indexation).", "Indexed release languages that keep a title (needs indexing).")},
			{Key: "PSE_FRENCH_TYPES", Advanced: true, Kind: KindMulti, Default: "movie,show", Options: typeOptions, Label: tx("Types filtrés", "Filtered types"),
				Help: tx("Les types non cochés restent intacts.", "Unchecked types are left untouched.")},
		},
	},
	{
		ID:          "indexer",
		Title:       tx("Indexation en arrière-plan", "Background indexing"),
		Description: tx("Parcourt le catalogue par popularité et interroge vos addons Stremio (et, si vous les choisissez, Prowlarr/Jackett/NZBHydra2) pour enregistrer les releases disponibles. Aucun appel à l'API AllDebrid, rien n'est ajouté au compte.", "Walks the catalogue by popularity and queries your Stremio addons (and, if chosen, Prowlarr/Jackett/NZBHydra2) to record available releases. No AllDebrid API call, nothing added to the account."),
		Fields: []Field{
			{Key: "PSE_INDEXER", Kind: KindToggle, Default: "off", Label: tx("Activer", "Enable"),
				Help: tx("Mis en pause pendant les lectures. Changer un réglage redémarre l'indexation.", "Paused during playback. Changing a setting restarts indexing.")},
			{Key: "PSE_INDEXER_SOURCES", Kind: KindMulti, Default: "", Label: tx("Sources importées", "Imported sources"),
				Help: tx("Aucune sélection : tous les addons Stremio activés. Un indexeur Newznab/Torznab n'est interrogé que s'il est coché.", "No selection: every enabled Stremio addon. A Newznab/Torznab indexer is only queried when checked.")},
			{Key: "PSE_INDEXER_DAILY", Advanced: true, Kind: KindNumber, Default: "1000", Min: ptr(0), Max: ptr(100000), Label: tx("Requêtes par jour et par source", "Requests per day per source"),
				Help: tx("Plafonné à 5 % de la limite déclarée d'un indexeur Newznab/Torznab.", "Capped at 5% of a Newznab/Torznab indexer's declared limit.")},
			{Key: "PSE_INDEXER_PACE", Advanced: true, Kind: KindDuration, Default: "10s", Placeholder: "10s", Label: tx("Délai entre deux titres", "Delay between titles"),
				Help: tx("Minimum 2s.", "At least 2s.")},
			{Key: "PSE_INDEXER_TYPES", Advanced: true, Kind: KindMulti, Default: "movie,show", Options: typeOptions, Label: tx("Types indexés", "Indexed types"),
				Help: tx("Une série est cherchée via son premier épisode.", "A show is searched through its first episode.")},
		},
	},
	{
		ID:          "playable",
		Title:       tx("Catalogue « lisible uniquement »", "“Playable only” catalogue"),
		Description: tx("Ne liste que les titres ayant assez de releases indexées correspondant à vos critères. Nécessite l'indexation.", "Only lists titles with enough indexed releases matching your criteria. Needs indexing."),
		Fields: []Field{
			{Key: "PSE_PLAYABLE", Kind: KindToggle, Default: "off", Label: tx("Activer", "Enable"),
				Help: tx("La liste de suivi, les collections, « Reprendre » et la recherche restent complètes.", "Watchlist, collections, continue watching and search stay complete.")},
			{Key: "PSE_PLAYABLE_MIN", Kind: KindNumber, Default: "2", Min: ptr(1), Max: ptr(50), Label: tx("Releases minimum", "Minimum releases"),
				Help: tx("Releases distinctes (même nom = une seule) nécessaires pour lister un titre.", "Distinct releases (same name counts once) needed to list a title.")},
			{Key: "PSE_PLAYABLE_FILTER", Kind: KindFilter, Default: "", Placeholder: "lang=fr,multi;min=1080", Label: tx("Critères des releases", "Release criteria"),
				Help: tx("Langues, résolution, codecs, cache debrid. Un changement recalcule le catalogue sans réindexer.", "Languages, resolution, codecs, debrid cache. A change recomputes the catalogue without reindexing.")},
			{Key: "PSE_PLAYABLE_PENDING", Advanced: true, Kind: KindSelect, Default: "hide", Label: tx("Titres pas encore indexés", "Titles not yet indexed"),
				Help:    tx("« Afficher » est pratique pendant la première indexation.", "“Show” is handy during the first indexing run."),
				Options: []Option{{"hide", tx("Masquer", "Hide")}, {"show", tx("Afficher", "Show")}}},
		},
	},
	{
		ID:          "scrob",
		Title:       tx("Scrob", "Scrob"),
		Description: tx("Suivi du visionnage dans Scrob : Ether-Remix envoie chaque lecture (début, pause, fin, marqué vu ou non vu) au webhook Jellyfin de Scrob, en temps réel, avec le lien de chaque spectateur réglé ci-dessous. Les bibliothèques « Vus et notés » permettent en plus à Scrob d'importer l'historique existant.", "Watch tracking in Scrob: Ether-Remix sends every playback (start, pause, stop, marked watched or unwatched) to Scrob's Jellyfin webhook in real time, using each viewer's link set below. The “Vus et notés” libraries also let Scrob import existing history."),
		Fields: []Field{
			{Key: "PSE_SCROB_PUSH", Kind: KindToggle, Default: "off", Label: tx("Envoyer les lectures à Scrob", "Send playback to Scrob"),
				Help: tx("Lien webhook par spectateur : Scrob → Connexions → votre connexion Jellyfin → URL du webhook.", "One webhook link per viewer: Scrob → Connections → your Jellyfin connection → webhook URL.")},
			{Key: "PSE_SCROB_VIEWS", Kind: KindToggle, Default: "off", Label: tx("Bibliothèques « Vus et notés »", "“Vus et notés” libraries"),
				Help: tx("Visibles aussi dans les autres applications Jellyfin/Emby.", "Also visible in other Jellyfin/Emby apps.")},
		},
	},
	{
		ID:          "music",
		Title:       tx("Musique", "Music"),
		Description: tx("Catalogue Deezer public, lu par vos comptes AllDebrid, Real-Debrid, TorBox ou un addon Eclipse ; rien n'est stocké sur disque. Visible dans Plex, Jellyfin et les clients Subsonic (Feishin, Symfonium…, adresse de la console, authentification « legacy »).", "Public Deezer catalogue, played through your AllDebrid, Real-Debrid, TorBox accounts or an Eclipse addon; nothing is stored on disk. Shown in Plex, Jellyfin and Subsonic clients (Feishin, Symfonium…, console address, “legacy” authentication)."),
		Fields: []Field{
			{Key: "PSE_MUSIC_ALLDEBRID", Kind: KindToggle, Default: "off", Label: tx("Lire depuis mon compte AllDebrid", "Play from my AllDebrid account"),
				Help: tx("Rien à installer : Ether-Remix reprend la clé AllDebrid et les indexeurs torrent d'Ether (ou Prowlarr). Au lancement d'un titre, l'album est cherché, ouvert sur AllDebrid s'il y est déjà en cache, puis lu en flux. Avec un addon Eclipse, celui-ci est essayé d'abord.", "Nothing to install: Ether-Remix reuses Ether's AllDebrid key and torrent indexers (or Prowlarr). When a track starts, the album is searched, opened on AllDebrid if already cached, then streamed. With an Eclipse addon, that is tried first.")},
			{Key: "PSE_MUSIC_DMM", Kind: KindToggle, Default: "off", Label: tx("DMM · Real-Debrid", "DMM · Real-Debrid"),
				Help: tx("Cherche les albums dans le catalogue public Debrid Media Manager, sans Prowlarr. Utilise vos comptes Real-Debrid actifs dans Sources › Debrid, dans leur ordre de priorité. La clé reste entre Ether-Remix et Real-Debrid. Essayé avant la recherche Real-Debrid par indexeurs ; les autres sources prennent le relais si nécessaire.", "Searches Debrid Media Manager’s public album catalogue without Prowlarr. Uses your active Real-Debrid accounts from Sources › Debrid in priority order. The key stays between Ether-Remix and Real-Debrid. Tried before Real-Debrid indexer searches; other sources take over when needed.")},
			{Key: "PSE_MUSIC_REALDEBRID", Kind: KindToggle, Default: "off", Label: tx("Lire depuis mon compte Real-Debrid", "Play from my Real-Debrid account"),
				Help: tx("Réutilise le compte et les indexeurs d’Ether-Remix. Sélectionne les pistes audio de l’album ; seuls les fichiers prêts sont lus. Un ajout non prêt est retiré, mais Real-Debrid peut brièvement démarrer son téléchargement côté serveur. Les comptes sont essayés selon leur priorité dans Sources › Debrid, après Eclipse.", "Reuses Ether-Remix’s account and indexers. Selects the album’s audio tracks; only ready files are played. An unready addition is removed, but Real-Debrid may briefly start downloading it server-side. Accounts follow their priority in Sources › Debrid, after Eclipse.")},
			{Key: "PSE_MUSIC_TORBOX", Kind: KindToggle, Default: "off", Label: tx("Lire depuis mon compte TorBox", "Play from my TorBox account"),
				Help: tx("Réutilise le compte et les indexeurs d’Ether-Remix. Ajoute uniquement les albums déjà en cache et lit chaque piste directement, sans stockage local. Priorité réglée dans Sources › Debrid. FLAC et Hi-Res selon les fichiers disponibles.", "Reuses Ether-Remix’s account and indexers. Adds cached albums only and streams each track directly, without local storage. Priority is set in Sources › Debrid. FLAC and Hi-Res depend on the available files.")},

			{Key: "PSE_MUSIC_AUDIUS", Kind: KindSelect, Default: "off", Label: tx("Audius (MP3 public)", "Audius (public MP3)"),
				Help:    tx("Sans compte ni clé. Uniquement les titres publics correspondant au catalogue Deezer. Choisissez le repli pour privilégier vos sources lossless. Le test des sources ci-dessous vérifie la lecture.", "No account or key. Only public tracks matching the Deezer catalogue. Choose fallback to prefer your lossless sources. The source test below checks playback."),
				Options: []Option{{"off", tx("Désactivé", "Disabled")}, {"first", tx("Avant les addons Eclipse", "Before Eclipse addons")}, {"last", tx("En repli après les autres sources", "Fallback after other sources")}}},
			{Key: "PSE_MUSIC_ECLIPSE_URL", Kind: KindURL, List: true, Secret: true, Default: "", Placeholder: "https://…/manifest.json", Label: tx("Lien manifest de l'addon Eclipse", "Eclipse addon manifest link"),
				Help: tx("Le lien manifest.json généré sur le site de l'addon, comme dans Eclipse ou Remux. Plusieurs liens séparés par des espaces : essayés dans l'ordre. Facultatif avec AllDebrid.", "The manifest.json link generated on the addon's site, as in Eclipse or Remux. Several links separated by spaces are tried in order. Optional with AllDebrid.")},
			{Key: "PSE_LASTFM_API_KEY", Kind: KindKey, Default: "", Placeholder: "32 caractères", Label: tx("Clé API Last.fm", "Last.fm API key"),
				Help: tx("Pour importer les titres aimés et les tops Last.fm des spectateurs. Gratuite : last.fm/api/account/create (seuls un nom et une description sont demandés). Les comptes Deezer et Last.fm se lient plus bas, par spectateur.", "To import viewers' Last.fm loved tracks and top tracks. Free: last.fm/api/account/create (only a name and a description are asked). Deezer and Last.fm accounts are linked below, per viewer.")},
			{Key: "PSE_LASTFM_SECRET", Kind: KindKey, Default: "", Placeholder: "32 caractères", Label: tx("Secret partagé Last.fm", "Last.fm shared secret"),
				Help: tx("Pour envoyer les écoutes à Last.fm (scrobbling). Il s'affiche à côté de la clé API sur last.fm/api/accounts. Chaque spectateur relie ensuite son compte plus bas.", "To send plays to Last.fm (scrobbling). It is shown next to the API key on last.fm/api/accounts. Each viewer then links their account below.")},
			{Key: "PSE_MUSIC_PLAYLISTS_HOURS", Advanced: true, Kind: KindNumber, Default: "6", Min: ptr(1), Max: ptr(168), Label: tx("Mise à jour des playlists (heures)", "Playlist refresh (hours)"),
				Help: tx("Les playlists Deezer et les listes Last.fm sont réimportées toutes les N heures, de 1 à 168.", "Deezer playlists and Last.fm lists are re-imported every N hours, from 1 to 168.")},
			{Key: "PSE_MUSIC_CHARTS", Kind: KindMulti, Default: "FR,WW", Options: chartOptions, Label: tx("Tops par pays", "Charts by country"),
				Help: tx("Playlists « Top France », « Top Royaume-Uni »… (100 titres, classements Deezer), ajoutées pour chaque spectateur et mises à jour avec les playlists.", "“Top France”, “Top UK”… playlists (100 tracks, Deezer charts), added for every viewer and refreshed with the playlists.")},
			{Key: "PSE_MUSIC_DISCOVER", Kind: KindToggle, Default: "on", Label: tx("Playlist « Découvertes »", "“Discoveries” playlist"),
				Help: tx("Pour chaque spectateur, une cinquantaine de titres d'artistes proches de ceux qu'il écoute et de ses playlists, renouvelée avec les playlists.", "For every viewer, about fifty tracks by artists close to what they listen to and to their playlists, renewed with the playlists.")},
			{Key: "PSE_MUSIC_HIFI_URL", Advanced: true, Kind: KindURL, Default: "", Placeholder: "https://hifi.example.org", Label: tx("HiFi API", "HiFi API"),
				Help: tx("Utilisée seulement quand aucune autre source n'est active (ni addon Eclipse, ni Audius, ni compte debrid) ; jamais ajoutée en repli. Des identifiants HTTP Basic peuvent figurer dans l'adresse.", "Only used when no other source is active (no Eclipse addon, Audius or debrid account); never added as a fallback. HTTP Basic credentials may be embedded in the address.")},
			{Key: "PSE_MUSIC_QUALITY", Advanced: true, Kind: KindSelect, Default: "LOSSLESS", Label: tx("Qualité (HiFi API)", "Quality (HiFi API)"),
				Help: tx("Un flux DASH (souvent en hi-res) bascule sur la qualité inférieure.", "A DASH stream (often hi-res) falls back to the next quality."),
				Options: []Option{{"HI_RES_LOSSLESS", tx("Hi-res lossless", "Hi-res lossless")}, {"LOSSLESS", tx("Lossless (FLAC 16 bits)", "Lossless (16-bit FLAC)")},
					{"HIGH", tx("Haute (AAC 320)", "High (AAC 320)")}, {"LOW", tx("Basse", "Low")}}},
			{Key: "PSE_MUSIC_COVERS_URL", Advanced: true, Kind: KindURL, Default: "https://resources.tidal.com/images", Label: tx("Origine des pochettes (HiFi API)", "Artwork origin (HiFi API)"),
				Help: tx("Rarement à modifier. Avec un addon Eclipse, les pochettes viennent de Deezer.", "Rarely needs changing. With an Eclipse addon, artwork comes from Deezer.")},
		},
	},
})

func communityFields(features []Feature) []Feature {
	for i := range features {
		if features[i].ID != "music" {
			continue
		}
		for _, a := range music.CommunityAddons() {
			features[i].Fields = append(features[i].Fields, Field{Key: a.Key, Kind: KindURL, Secret: true, Community: true, Label: tx(a.Name, a.Name), Placeholder: "https://…/manifest.json", Help: tx("Collez le manifest généré sur le site du fournisseur. Vide : désactivé.", "Paste the manifest generated on the provider’s website. Empty: disabled.")})
		}
		features[i].Fields = append(features[i].Fields, Field{Key: "PSE_MUSIC_ADDON_REGISTRY", Kind: KindToggle, Default: "on", Advanced: true, Label: tx("Actualiser les liens des fournisseurs", "Refresh provider setup links"), Help: tx("Consulte le registre public Eclipse toutes les 6 heures lorsque la musique est active. Ne modifie jamais vos manifests personnels.", "Checks the public Eclipse registry every 6 hours while music is active. Never changes your personal manifests.")})
	}
	return features
}

var fieldByKey = func() map[string]Field {
	m := map[string]Field{}
	for _, f := range Features {
		for _, fd := range f.Fields {
			m[fd.Key] = fd
		}
	}
	return m
}()
