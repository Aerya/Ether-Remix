package console

import (
	"github.com/braedonsaunders/ether/internal/pse/continuewatch"
	"github.com/braedonsaunders/ether/internal/pse/music"
)

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
	KindFeeds     = "feeds"  // liste de flux : JSON [{name, url, key}], clés masquées à l'affichage
	KindKey       = "key"    // clé d'API, masquée à l'affichage
	KindSecret    = "secret" // mot de passe : jamais renvoyé, même en partie
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
		Description: tx("N'affiche que les films et séries disponibles en français, y compris dans les rangées de l'accueil et les collections : œuvre française, sortie numérique ou TV dans les pays choisis, ou release VF/MULTi indexée.", "Only lists titles available in French, including Home rows and collections: French original, digital or TV release in the chosen countries, or an indexed French/MULTi release."),
		Fields: []Field{
			{Key: "PSE_FRENCH", Kind: KindToggle, Default: "off", Label: tx("Activer", "Enable"),
				Help: tx("S'applique à l'accueil, aux bibliothèques, aux recommandations et aux collections. La liste de suivi, la recherche et les collections de sorties à venir restent complètes.", "Applies to home, libraries, recommendations and collections. Watchlist, search and upcoming collections stay complete.")},
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
		Title:       tx("Indexation des releases", "Release indexing"),
		Description: tx("Les recherches et imports conservent dans un index commun les liens conformes aux critères Remix, réutilisés à la lecture. La recherche automatique prépare les contenus du catalogue local ; elle ne télécharge ni ne débride les fichiers. Les addons distants peuvent effectuer leurs propres appels. Pour les séries, le passage initial couvre S01E01 ; les autres épisodes sont conservés au fil des recherches.", "Searches and imports retain links meeting Remix criteria in one shared index, reused during playback. Automatic search prepares titles from the local catalogue; it does not download or unlock files. Remote addons may make their own calls. For shows, the initial pass covers S01E01; other episodes are retained as they are searched."),
		Fields: []Field{
			{Key: "PSE_INDEXER", Kind: KindToggle, Default: "off", Label: tx("Rechercher automatiquement des releases", "Automatically search for releases"),
				Help: tx("En pause pendant les lectures. Désactiver arrête les recherches de fond sans effacer l’index ni empêcher sa réutilisation ou les recherches à la lecture.", "Paused during playback. Disabling stops background searches without deleting the index or preventing reuse and playback searches.")},
			{Key: "PSE_INDEXER_SOURCES", Kind: KindMulti, Default: "", Label: tx("Sources utilisées pour l’indexation", "Sources used for indexing"),
				Help: tx("Mode automatique : addons Stremio activés. Mode personnalisé : seules les sources cochées sont interrogées, y compris Prowlarr et Jackett. Ces choix ne changent pas les sources disponibles à la lecture.", "Automatic mode: enabled Stremio addons. Custom mode: only checked sources are queried, including Prowlarr and Jackett. These choices do not change sources available for playback.")},
			{Key: "PSE_INDEXER_DAILY", Advanced: true, Kind: KindNumber, Default: "1000", Min: ptr(0), Max: ptr(100000), Label: tx("Requêtes par jour et par source", "Requests per day per source"),
				Help: tx("Plafonné à 5 % de la limite déclarée d'un indexeur Newznab/Torznab.", "Capped at 5% of a Newznab/Torznab indexer's declared limit.")},
			{Key: "PSE_INDEXER_PACE", Advanced: true, Kind: KindDuration, Default: "10s", Placeholder: "10s", Label: tx("Délai entre deux titres", "Delay between titles"),
				Help: tx("Minimum 2s.", "At least 2s.")},
			{Key: "PSE_INDEXER_TYPES", Advanced: true, Kind: KindMulti, Default: "movie,show", Options: typeOptions, Label: tx("Types indexés", "Indexed types"),
				Help: tx("Une série est cherchée via son premier épisode.", "A show is searched through its first episode.")},
		},
	},
	{
		ID:          "sfr",
		Title:       tx("Base StreamFusion Reborn auto-hébergée", "Self-hosted StreamFusion Reborn database"),
		Description: tx("Importe le catalogue et recherche les films ou épisodes dans votre base en lecture seule. La disponibilité debrid est vérifiée avant lecture ; vos autres sources restent utilisables.", "Imports the catalogue and searches movies or episodes in your read-only database. Debrid availability is checked before playback; other sources remain available."),
		Fields: []Field{
			{Key: "PSE_SFR", Kind: KindToggle, Default: "off", Label: tx("Activer", "Enable"),
				Help: tx("Désactivé par défaut. Le premier import lit toute la base, par lots.", "Off by default. The first import reads the whole database in batches.")},
			{Key: "PSE_SFR_URL", Kind: KindURL, Secret: true, Default: "", Placeholder: "postgres://remix_lecture:mot-de-passe@sfr-stremio-postgres:5432/streamfusion", Label: tx("Adresse PostgreSQL", "PostgreSQL address"),
				Help: tx("Compte en lecture seule, voir les étapes ci-dessous. Le mot de passe n'est jamais réaffiché.", "Read-only account, see the steps below. The password is never shown again.")},
			{Key: "PSE_SFR_PRIVATE", Kind: KindToggle, Default: "off", Label: tx("Inclure les trackers privés", "Include private trackers"),
				Help: tx("Désactivé par défaut. Un changement refait un import complet.", "Off by default. Changing this runs a full import again.")},
			{Key: "PSE_SFR_INTERVAL", Advanced: true, Kind: KindDuration, Default: "6h", Placeholder: "6h", Label: tx("Intervalle", "Interval"),
				Help: tx("Minimum 30m.", "At least 30m.")},
		},
	},
	{
		ID:    "predb",
		Title: tx("Pre (bêta) : annonces de releases FR / MULTi / VOSTFR", "Pre (beta): FR / MULTi / VOSTFR release announcements"),
		Description: tx("Fonction en bêta : les flux publics et les formats d'API personnels n'ont pas encore été éprouvés en conditions réelles. Lit les annonces de releases (les « pre ») et garde les françaises. Remix part du principe qu'une release annoncée est disponible sous une heure sur vos sources (debrid, BitTorrent, Usenet) : elle compte comme release indexée, et son film ou sa série est ajouté au catalogue sans attendre qu'il y arrive par TMDB. Une pre ne contient aucun lien : la lecture passe toujours par vos sources.",
			"Beta feature: the public feeds and personal API formats have not yet been proven in real conditions. Reads release announcements (“pre”) and keeps the French ones. Remix assumes an announced release is available within an hour on your sources (debrid, BitTorrent, Usenet): it counts as an indexed release, and its movie or show is added to the catalogue without waiting for it to arrive through TMDB. A pre holds no link: playback always goes through your sources."),
		Fields: []Field{
			{Key: "PSE_PREDB", Kind: KindToggle, Default: "off", Label: tx("Activer", "Enable"),
				Help: tx("Désactivé par défaut. Active les flux publics ci-dessous, plus vos flux personnels si vous en renseignez.", "Off by default. Enables the public feeds, plus your personal feeds if you provide any.")},
			{Key: "PSE_PREDB_PUBLIC", Kind: KindToggle, Default: "on", Label: tx("Flux publics", "Public feeds"),
				Help: tx("predb.me et la source publique de PreDB. Décochez pour n'utiliser que vos flux personnels.", "predb.me and PreDB's public source. Untick to use only your personal feeds.")},
			{Key: "PSE_PREDB_LANGS", Kind: KindMulti, Default: "fr,multi,vostfr", Options: []Option{{"fr", tx("FRENCH / VF (VFF, VFQ, VFI…)", "FRENCH / VF (VFF, VFQ, VFI…)")}, {"multi", tx("MULTi", "MULTi")}, {"vostfr", tx("VOSTFR / SUBFRENCH", "VOSTFR / SUBFRENCH")}}, Label: tx("Langues gardées", "Languages kept"),
				Help: tx("Les releases « autre langue + MULTi » et les corrections (FIX) sont toujours écartées.", "“Other language + MULTi” releases and fixes are always dropped.")},
			{Key: "PSE_PREDB_FEEDS", Kind: KindFeeds, Default: "", Label: tx("Flux privés (RSS, API...)", "Private feeds (RSS, API...)"),
				Help: tx("Ajoutez autant de sites que vous voulez : un nom (facultatif), l'adresse d'un flux RSS ou d'une API JSON et, si le site en demande une, sa clé d'API. La clé est envoyée en en-tête X-API-Key à ce site seulement, jamais dans l'adresse, et n'est jamais réaffichée. Ces sites passent par le proxy ci-dessous, s'il y en a un.", "Add as many sites as you like: an optional name, the address of an RSS feed or JSON API and, if the site needs one, its API key. The key is sent as an X-API-Key header to that site only, never in the address, and is never shown again. These sites go through the proxy below, if you set one.")},
			{Key: "PSE_PREDB_PROXY_TYPE", Kind: KindSelect, Default: "", Label: tx("Proxy pour les appels PRE", "Proxy for PRE calls"),
				Help:    tx("predb.me refuse les adresses de serveurs d'hébergeurs (erreur 403). Choisissez un proxy HTTP, HTTPS ou SOCKS5 (par exemple Gluetun), ou un relais MediaFlow, pour lire les flux publics et privés par une autre sortie.", "predb.me refuses hosting-provider addresses (403 error). Pick an HTTP, HTTPS or SOCKS5 proxy (for example Gluetun), or a MediaFlow relay, to read the public and private feeds through another exit."),
				Options: []Option{{"", tx("Aucun (lecture directe)", "None (direct)")}, {"proxy", tx("Proxy HTTP, HTTPS ou SOCKS5", "HTTP, HTTPS or SOCKS5 proxy")}, {"mediaflow", tx("Relais MediaFlow", "MediaFlow relay")}}},
			{Key: "PSE_PREDB_PROXY_URL", Kind: KindURL, Default: "", Placeholder: "http://gluetun:8888 · socks5://hôte:1080 · https://mediaflow.exemple.net", Label: tx("Adresse du proxy ou du relais", "Proxy or relay address"),
				Help: tx("Sans identifiants dans l'adresse : ils se saisissent dans le champ suivant. Adresses http://, https:// ou socks5:// pour un proxy ; http(s):// pour un relais MediaFlow.", "No credentials in the address: enter them in the next field. http://, https:// or socks5:// addresses for a proxy; http(s):// for a MediaFlow relay.")},
			{Key: "PSE_PREDB_PROXY_AUTH", Kind: KindSecret, Default: "", Label: tx("Identifiants du proxy (utilisateur:mot de passe) ou mot de passe API MediaFlow", "Proxy credentials (user:password) or MediaFlow API password"),
				Help: tx("Facultatif pour un proxy sans authentification. Pour MediaFlow, saisissez le mot de passe API du relais. Jamais réaffiché.", "Optional for a proxy without authentication. For MediaFlow, enter the relay's API password. Never shown again.")},
			{Key: "PSE_PREDB_DELAY", Advanced: true, Kind: KindDuration, Default: "1h", Placeholder: "1h", Label: tx("Délai avant prise en compte", "Delay before counting"),
				Help: tx("Une pre n'est comptée que ce temps après son annonce, le temps que la release arrive sur les sources. 0s : tout de suite.", "A pre only counts this long after its announcement, so the release has time to reach the sources. 0s: immediately.")},
			{Key: "PSE_PREDB_INTERVAL", Advanced: true, Kind: KindDuration, Default: "1m", Placeholder: "1m", Label: tx("Intervalle de lecture", "Read interval"),
				Help: tx("Minimum 30s. Les flux publics ne gardent que les 20 dernières annonces : une lecture par minute évite d'en perdre.", "At least 30s. Public feeds only keep the 20 latest announcements: reading once a minute avoids missing some.")},
		},
	},
	{
		ID:    "torrent",
		Title: tx("Clients BitTorrent (qBittorrent, rTorrent / ruTorrent)", "BitTorrent clients (qBittorrent, rTorrent / ruTorrent)"),
		Description: tx("Envoie les releases torrent de vos indexeurs (Prowlarr, Jackett) à votre client, puis les lit une fois téléchargées : films, séries, animés et musique. Les fichiers restent sur le disque du client, qui les partage selon vos limites ; Remix ne supprime jamais rien. Vos comptes debrid restent en « cache uniquement ». Le client peut être distant, mais Remix doit voir son dossier de téléchargement (même machine, ou partage NFS/SMB monté) : sinon l'ajout marche, la lecture non.",
			"Sends torrent releases from your indexers (Prowlarr, Jackett) to your client, then plays them once downloaded: movies, shows, anime and music. Files stay on the client's disk, which seeds them within your limits; Remix never deletes anything. Your debrid accounts stay cached-only. The client can be remote, but Remix must see its download folder (same machine, or a mounted NFS/SMB share): otherwise adding works, playback does not."),
		Fields: torrentMainFields(),
	},
	torrentExtraClient(2),
	torrentExtraClient(3),
	xdccFeature(),
	ftpFeature(),
	{
		ID:          "playable",
		Title:       tx("Catalogue « lisible uniquement »", "“Playable only” catalogue"),
		Description: tx("Ne liste que les titres ayant assez de releases indexées correspondant à vos critères. Nécessite l'indexation.", "Only lists titles with enough indexed releases matching your criteria. Needs indexing."),
		Fields: []Field{
			{Key: "PSE_PLAYABLE", Kind: KindToggle, Default: "off", Label: tx("Activer", "Enable"),
				Help: tx("S'applique aussi à l'accueil et aux collections. La liste de suivi, « Reprendre », la recherche et les collections de sorties à venir restent complètes.", "Also applies to home and collections. Watchlist, continue watching, search and upcoming collections stay complete.")},
			{Key: "PSE_PLAYABLE_MIN", Kind: KindNumber, Default: "2", Min: ptr(1), Max: ptr(50), Label: tx("Releases minimum", "Minimum releases"),
				Help: tx("Releases distinctes (même nom = une seule) nécessaires pour lister un titre.", "Distinct releases (same name counts once) needed to list a title.")},
			{Key: "PSE_PLAYABLE_SINGLE", Kind: KindToggle, Default: "off", Label: tx("Afficher aussi les titres à risque", "Also show risky titles"),
				Help: tx("Garde les titres qui ont moins de releases que le minimum, au moins une, en prévenant les spectateurs que la lecture est incertaine.", "Keeps titles with fewer releases than the minimum, at least one, and warns viewers that playback is uncertain.")},
			{Key: "PSE_PLAYABLE_SINGLE_MARK", Kind: KindSelect, Default: "summary", Label: tx("Signalement dans Plex et Jellyfin", "Warning in Plex and Jellyfin"),
				Help:    tx("Le résumé commence par « ⚠ 1 source ». Le badge « 1 SOURCE » n’apparaît que si les affiches d’Ether sont activées ; avec un autre outil d’affiches (PosterPlus…), choisissez le résumé.", "The summary starts with “⚠ 1 source”. The “1 SOURCE” badge only shows when Ether posters are on; with another poster tool (PosterPlus…), pick the summary."),
				Options: []Option{{"summary", tx("Résumé", "Summary")}, {"poster", tx("Badge d’affiche", "Poster badge")}, {"both", tx("Les deux", "Both")}}},
			{Key: "PSE_PLAYABLE_FILTER", Kind: KindFilter, Default: "", Placeholder: "lang=fr,multi;min=1080", Label: tx("Critères des releases", "Release criteria"),
				Help: tx("Langues, résolution, codecs, cache debrid. Un changement recalcule le catalogue sans réindexer.", "Languages, resolution, codecs, debrid cache. A change recomputes the catalogue without reindexing.")},
			{Key: "PSE_PLAYABLE_PENDING", Advanced: true, Kind: KindSelect, Default: "hide", Label: tx("Titres pas encore indexés", "Titles not yet indexed"),
				Help:    tx("« Afficher » est pratique pendant la première indexation.", "“Show” is handy during the first indexing run."),
				Options: []Option{{"hide", tx("Masquer", "Hide")}, {"show", tx("Afficher", "Show")}}},
		},
	},
	{
		ID:          "librarysort",
		Title:       tx("Tri des bibliothèques", "Library sorting"),
		Description: tx("Ordre des bibliothèques Films et Séries à leur ouverture dans Plex, Jellyfin et Emby. Un tri choisi dans l'application reste prioritaire.", "Order of the Movies and Shows libraries when opened in Plex, Jellyfin and Emby. A sort chosen in the app still wins."),
		Fields: []Field{
			{Key: "PSE_LIBRARY_SORT", Kind: KindSelect, Default: "release", Label: tx("Tri par défaut", "Default sort"),
				Help:    tx("Plex applique ce tri tant que vous n'en avez pas choisi un autre. Jellyfin Web et Infuse gardent leur propre tri : changez-le une fois dans l'application.", "Plex uses this sort until you pick another. Jellyfin Web and Infuse keep their own: change it once in the app."),
				Options: []Option{{"release", tx("Date de sortie, du plus récent au plus ancien", "Release date, newest first")}, {"title", tx("Titre (A → Z)", "Title (A → Z)")}}},
		},
	},
	{
		ID:          "lightlists",
		Title:       tx("Listes légères", "Light lists"),
		Description: tx("Une bibliothèque, une rangée ou une collection n'affiche que ce qui est déjà en base : aucune fiche TMDB complétée, aucune écriture du catalogue, aucun appel debrid. Les détails ne partent qu'à l'ouverture d'une fiche ou à la lecture.", "A library, row or collection only shows what is already stored: no TMDB detail fetched, no catalogue write, no debrid call. Details load only when a title is opened or played."),
		Fields: []Field{
			{Key: "PSE_LIGHT_LISTS", Kind: KindToggle, Default: "on", Label: tx("Listes sans chargement des fiches", "Lists without loading titles"),
				Help: tx("Désactivé, les listes se comportent comme dans Ether : fiches complétées à l'affichage et préchauffage de l'accueil.", "Off, lists behave like Ether: details completed while listing and Home warm-up.")},
		},
	},
	{
		ID:          "continuewatch",
		Title:       tx("Continuer à regarder", "Continue watching"),
		Description: tx("Rangée « Continuer à regarder » de Plex, Jellyfin, Emby et Stremio, pour les bibliothèques Ether comme pour les bibliothèques Remix. Une ancienne série dont un nouvel épisode vient de sortir remonte en tête, classée à la date de sortie de l'épisode. Droits, contrôle parental, favoris et épisodes futurs ne changent pas.", "Continue Watching row of Plex, Jellyfin, Emby and Stremio, for Ether libraries and Remix libraries alike. An old show with a newly released episode comes back on top, ranked by the episode's release date. Access rights, parental controls, favorites and future episodes are unchanged."),
		Fields: []Field{
			{Key: continuewatch.KeyScanLimit, Kind: KindNumber, Default: "200", Min: ptr(continuewatch.MinScanLimit), Max: ptr(continuewatch.MaxScanLimit), Label: tx("Séries examinées", "Shows examined"),
				Help: tx("Nombre de séries de l'historique dont l'épisode suivant est cherché (de 20 à 1000). Plus il est grand, plus on retrouve d'anciennes séries, mais plus la rangée est longue à construire. Sur Plex, au plus 100 séries sont examinées.", "Number of history shows whose next episode is looked up (20 to 1000). A higher value finds older shows but makes the row slower to build. On Plex, at most 100 shows are examined.")},
			{Key: continuewatch.KeyWatchDays, Kind: KindNumber, Default: "0", Min: ptr(0), Max: ptr(continuewatch.MaxWatchDays), Label: tx("Période du dernier visionnage (jours)", "Last-watched period (days)"),
				Help: tx("Ignore les titres en cours et les séries terminées regardés il y a plus longtemps. 0 = illimitée. Une série plus ancienne reste affichée si son épisode suivant est un nouvel épisode.", "Skips in-progress titles and finished shows last watched longer ago. 0 = unlimited. An older show is still shown when its next episode is a new episode.")},
			{Key: continuewatch.KeyNewDays, Kind: KindNumber, Default: "60", Min: ptr(0), Max: ptr(continuewatch.MaxNewDays), Label: tx("Fenêtre des nouveaux épisodes (jours)", "New episodes window (days)"),
				Help: tx("Un épisode suivant non vu, sorti depuis moins de ce nombre de jours, fait remonter la série même si son dernier visionnage est ancien, et la classe à la date de sortie. 0 = désactivé.", "An unwatched next episode released within this many days brings the show back even if it was last watched long ago, ranked by its release date. 0 = off.")},
			{Key: continuewatch.KeyMaxMovies, Kind: KindNumber, Default: "0", Min: ptr(0), Max: ptr(continuewatch.MaxQuantity), Label: tx("Films au maximum", "Maximum movies"),
				Help: tx("Nombre maximal de films dans la rangée, sans dépasser la limite de l'application. 0 = limite de l'application.", "Maximum number of movies in the row, never above the app's limit. 0 = the app's limit.")},
			{Key: continuewatch.KeyMaxShows, Kind: KindNumber, Default: "0", Min: ptr(0), Max: ptr(continuewatch.MaxQuantity), Label: tx("Séries au maximum", "Maximum shows"),
				Help: tx("Nombre maximal de séries (un épisode par série) dans la rangée, sans dépasser la limite de l'application. 0 = limite de l'application.", "Maximum number of shows (one episode each) in the row, never above the app's limit. 0 = the app's limit.")},
		},
	},
	{
		ID:          "posters",
		Title:       tx("Affiches dans les applications", "Posters in apps"),
		Description: tx("Avec un fournisseur d'affiches (PostersPlus, XRDB, RPDB…), les applications gardent chaque affiche au lieu de la redemander à chaque affichage.", "With a poster provider (PostersPlus, XRDB, RPDB…), apps keep each poster instead of asking for it again on every screen."),
		Fields: []Field{
			{Key: "PSE_POSTER_REFRESH_HOURS", Kind: KindNumber, Default: "24", Min: ptr(1), Max: ptr(168), Label: tx("Renouvellement des affiches (heures)", "Poster refresh (hours)"),
				Help: tx("Délai au bout duquel les applications reprennent les affiches du fournisseur, pour suivre ses changements (notes, style). Ether utilisait 6 heures.", "Delay after which apps fetch provider posters again, to follow its changes (ratings, style). Ether used 6 hours.")},
		},
	},
	{
		ID:          "scrob",
		Title:       tx("Scrob", "Scrob"),
		Description: tx("Connectez Ether-Remix dans Scrob comme un serveur Jellyfin. Choisissez les sens de synchronisation et leur fréquence dans Scrob : Remix → Scrob pour importer l’historique ; Scrob → Remix pour les statuts vus et les notes.", "Connect Ether-Remix in Scrob as a Jellyfin server. Choose sync directions and schedules in Scrob: Remix → Scrob imports history; Scrob → Remix sends watched status and ratings."),
		Fields: []Field{
			{Key: "PSE_SCROB_PUSH", Advanced: true, Kind: KindToggle, Default: "off", Label: tx("Envoi immédiat des événements de lecture (webhook)", "Send playback events immediately (webhook)"),
				Help: tx("Complément facultatif à la synchronisation programmée : début, pause et fin de lecture. Nécessite le webhook de la même connexion Jellyfin, à renseigner ci-dessus. Ce bouton ne contrôle pas le retour Scrob → Remix.", "Optional addition to scheduled sync: playback start, pause and stop. Requires the same Jellyfin connection’s webhook, entered above. This switch does not control Scrob → Remix sync.")},
			{Key: "PSE_SCROB_VIEWS", Advanced: true, Kind: KindToggle, Default: "off", Label: tx("Proposer des bibliothèques limitées à l’historique", "Offer history-only libraries"),
				Help: tx("Ajoute « Vus et notés » dans les bibliothèques que Scrob peut importer : utile pour éviter de parcourir tout le catalogue. Facultatif ; les bibliothèques Films et Séries fonctionnent aussi. Ne contrôle aucun sens de synchronisation.", "Adds “Vus et notés” libraries for Scrob to import, avoiding a full-catalogue scan. Optional; Movies and Shows libraries also work. Does not control either sync direction.")},
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
			{Key: "PSE_MUSIC_DMM_URL", Kind: KindURL, Default: "https://debridmediamanager.com", Label: tx("Adresse de l’instance DMM", "DMM instance address"),
				Help: tx("Instance publique par défaut. Pour une instance auto-hébergée, indiquez son adresse accessible depuis Remix, sans /albums ni /api/music. Elle doit fournir un catalogue musical et les API /api/music/search et /api/music/library. Une installation vide ne reprend pas automatiquement le catalogue public.", "Public instance by default. For a self-hosted instance, enter its address reachable from Remix, without /albums or /api/music. It must provide a music catalogue and the /api/music/search and /api/music/library APIs. An empty installation does not automatically inherit the public catalogue.")},
			{Key: "PSE_MUSIC_REALDEBRID", Kind: KindToggle, Default: "off", Label: tx("Lire depuis mon compte Real-Debrid", "Play from my Real-Debrid account"),
				Help: tx("Réutilise le compte et les indexeurs d’Ether-Remix. Sélectionne les pistes audio de l’album ; seuls les fichiers prêts sont lus. Un ajout non prêt est retiré, mais Real-Debrid peut brièvement démarrer son téléchargement côté serveur. Les comptes sont essayés selon leur priorité dans Sources › Debrid, après Eclipse.", "Reuses Ether-Remix’s account and indexers. Selects the album’s audio tracks; only ready files are played. An unready addition is removed, but Real-Debrid may briefly start downloading it server-side. Accounts follow their priority in Sources › Debrid, after Eclipse.")},
			{Key: "PSE_MUSIC_TORBOX", Kind: KindToggle, Default: "off", Label: tx("Lire depuis mon compte TorBox", "Play from my TorBox account"),
				Help: tx("Réutilise le compte et les indexeurs d’Ether-Remix. Ajoute uniquement les albums déjà en cache et lit chaque piste directement, sans stockage local. Priorité réglée dans Sources › Debrid. FLAC et Hi-Res selon les fichiers disponibles.", "Reuses Ether-Remix’s account and indexers. Adds cached albums only and streams each track directly, without local storage. Priority is set in Sources › Debrid. FLAC and Hi-Res depend on the available files.")},

			{Key: "PSE_MUSIC_AUDIUS", Kind: KindSelect, Default: "off", Label: tx("Audius (MP3 public)", "Audius (public MP3)"),
				Help:    tx("Sans compte ni clé. Uniquement les titres publics correspondant au catalogue Deezer. Choisissez le repli pour privilégier vos sources lossless. Le test des sources ci-dessous vérifie la lecture.", "No account or key. Only public tracks matching the Deezer catalogue. Choose fallback to prefer your lossless sources. The source test below checks playback."),
				Options: []Option{{"off", tx("Désactivé", "Disabled")}, {"first", tx("Avant les addons Eclipse", "Before Eclipse addons")}, {"last", tx("En repli après les autres sources", "Fallback after other sources")}}},
			{Key: "PSE_MUSIC_OPENDEEZER", Kind: KindToggle, Default: "off", Label: tx("OpenDeezer", "OpenDeezer"),
				Help: tx("Lit directement les titres Deezer complets accessibles avec votre propre compte Deezer, sans téléchargement ni stockage. Compte gratuit : MP3 128 kb/s, jamais de FLAC. Si Deezer ne fournit qu’un aperçu de 30 s ou refuse le flux, la source est ignorée et la suivante prend le relais. Les publicités du compte gratuit ne sont pas lues par Remix ; chaque écoute est signalée à Deezer comme sur son lecteur web. Utilisez le test des sources ci-dessous pour vérifier la connexion.", "Streams full Deezer tracks available to your own Deezer account directly, with no download or storage. Free account: MP3 128 kbps, never FLAC. If Deezer only provides a 30 s preview or refuses the stream, the source is skipped and the next one takes over. Free-account ads are not played by Remix; each play is reported to Deezer as in its web player. Use the source test below to check the connection.")},
			{Key: "PSE_MUSIC_OPENDEEZER_ARL", Kind: KindSecret, Default: "", Placeholder: "arl", Label: tx("Session Deezer (ARL) pour OpenDeezer", "Deezer session (ARL) for OpenDeezer"),
				Help: tx("Cookie « arl » de votre session deezer.com (outils du navigateur › Stockage › Cookies). Il donne accès à votre compte : il n’est jamais réaffiché ni journalisé. Videz le champ pour le supprimer.", "The “arl” cookie of your deezer.com session (browser tools › Storage › Cookies). It gives access to your account: it is never shown again or logged. Clear the field to remove it.")},
			{Key: "PSE_MUSIC_OPENDEEZER_ORDER", Kind: KindSelect, Default: "last", Label: tx("Rang d’OpenDeezer", "OpenDeezer rank"),
				Help:    tx("En secours après vos autres sources (conseillé), ou avant elles.", "As a fallback after your other sources (recommended), or before them."),
				Options: []Option{{"last", tx("En secours après les autres sources", "Fallback after other sources")}, {"first", tx("Avant les autres sources", "Before other sources")}}},
			{Key: "PSE_MUSIC_ECLIPSE_URL", Kind: KindURL, List: true, Secret: true, Default: "", Placeholder: "https://…/manifest.json", Label: tx("Lien manifest de l'addon Eclipse", "Eclipse addon manifest link"),
				Help: tx("Le lien manifest.json généré sur le site de l'addon, comme dans Eclipse ou Remux. Plusieurs liens séparés par des espaces : essayés dans l'ordre. Facultatif avec AllDebrid.", "The manifest.json link generated on the addon's site, as in Eclipse or Remux. Several links separated by spaces are tried in order. Optional with AllDebrid.")},
			{Key: "PSE_LASTFM_API_KEY", Kind: KindKey, Default: "", Placeholder: "32 caractères", Label: tx("Clé API Last.fm", "Last.fm API key"),
				Help: tx("Pour importer les titres aimés et les tops Last.fm des spectateurs. Gratuite : last.fm/api/account/create (seuls un nom et une description sont demandés). Les comptes Deezer et Last.fm se lient dans Contenus et bibliothèques → Musique, par spectateur.", "To import viewers' Last.fm loved tracks and top tracks. Free: last.fm/api/account/create (only a name and a description are asked). Deezer and Last.fm accounts are linked under Content & libraries → Music, per viewer.")},
			{Key: "PSE_LASTFM_SECRET", Kind: KindKey, Default: "", Placeholder: "32 caractères", Label: tx("Secret partagé Last.fm", "Last.fm shared secret"),
				Help: tx("Pour envoyer les écoutes à Last.fm (scrobbling). Il s'affiche à côté de la clé API sur last.fm/api/accounts. Chaque spectateur relie ensuite son compte dans Contenus et bibliothèques → Musique.", "To send plays to Last.fm (scrobbling). It is shown next to the API key on last.fm/api/accounts. Each viewer then links their account under Content & libraries → Music.")},
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
	{
		ID: "submaker", Title: tx("SubMaker : sous-titres externes", "SubMaker: external subtitles"),
		Description: tx("Cherche des sous-titres compatibles puis traduit si nécessaire. Instance publique ou auto-hébergée ; désactivé par défaut.", "Find matching subtitles, then translate if needed. Public or self-hosted instance; disabled by default."),
		Fields: []Field{
			{Key: "PSE_SUBMAKER", Kind: KindToggle, Default: "off", Label: tx("Activer SubMaker", "Enable SubMaker")},
			{Key: "PSE_SUBMAKER_MANIFEST", Kind: KindURL, Secret: true, Label: tx("Manifeste SubMaker personnel", "Personal SubMaker manifest"), Placeholder: "https://submaker.example/addon/…/manifest.json", Help: tx("Configurez les langues et votre moteur de traduction sur l’instance choisie, puis collez le manifeste généré. Une URL /configure ne suffit pas.", "Configure languages and your translation engine on the chosen instance, then paste its generated manifest. A /configure URL is not sufficient.")},
			{Key: "PSE_SUBMAKER_SOURCE", Kind: KindKey, Default: "en", Label: tx("Langue source (code ISO)", "Source language (ISO code)")},
			{Key: "PSE_SUBMAKER_TARGET", Kind: KindKey, Default: "fr", Label: tx("Langue des sous-titres (code ISO)", "Subtitle language (ISO code)")},
			{Key: "PSE_SUBMAKER_DAILY", Kind: KindNumber, Default: "10", Min: ptr(1), Max: ptr(100), Label: tx("Maximum de recherches / traductions par jour", "Maximum searches / translations per day")},
		},
	},

	{
		ID:          "seerrcatalog",
		Title:       tx("Disponibilités vers Remix", "Availability to Remix"),
		Description: tx("Importer les disponibilités et sources déjà vérifiées par SeerrCatalog, sans relancer leur recherche.", "Import availability and sources already checked by SeerrCatalog without searching again."),
		Fields: []Field{
			{Key: "PSE_SEERRCATALOG", Kind: KindToggle, Default: "off", Label: tx("Importer les disponibilités", "Import availability"), Help: tx("Utilise les critères de SeerrCatalog. Les règles de lecture du spectateur restent appliquées.", "Uses SeerrCatalog criteria. Viewer playback policies still apply.")},
			{Key: "PSE_SEERRCATALOG_URL", Kind: KindURL, Label: tx("Adresse de SeerrCatalog", "SeerrCatalog address"), Placeholder: "http://seerrcatalog:7000", Help: tx("Adresse de base, sans manifest ni chemin API. Synchronisation chaque minute, sans requête aux addons.", "Base address, without manifest or API path. Synchronizes every minute without addon requests.")},
			{Key: "PSE_SEERRCATALOG_TOKEN", Kind: KindKey, Secret: true, Label: tx("Clé de partage des disponibilités", "Availability sharing token"), Help: tx("Copiez la clé du bloc Disponibilités vers Remix dans SeerrCatalog.", "Copy the token from Availability to Remix in SeerrCatalog.")},
		},
	},
	{
		ID:          "seerrcatalog_search",
		Title:       tx("Recherche via Remix", "Search via Remix"),
		Description: tx("Permet à SeerrCatalog de compléter ses recherches avec les sources de Remix, dont Prowlarr.", "Let SeerrCatalog complement its searches using Remix sources, including Prowlarr."),
		Fields: []Field{
			{Key: "PSE_SEERRCATALOG_SEARCH", Kind: KindToggle, Default: "off", Label: tx("Autoriser les recherches SeerrCatalog", "Allow SeerrCatalog searches")},
			{Key: "PSE_SEERRCATALOG_SEARCH_TOKEN", Kind: KindKey, Secret: true, Label: tx("Clé dédiée aux recherches", "Dedicated search token"), Help: tx("Copiez la clé du bloc Recherche via Remix dans SeerrCatalog (32 caractères minimum). Enregistrez ici, puis testez depuis SeerrCatalog.", "Copy the token from Search via Remix in SeerrCatalog (at least 32 characters). Save here, then test from SeerrCatalog.")},
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
