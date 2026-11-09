package music

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// Playlists « système » : tops Deezer par pays et « Découvertes » (titres
// d'artistes proches de ce qu'écoute le spectateur). Elles sont rangées
// dans pse_music_playlists comme les playlists importées, donc visibles
// dans Plex, Jellyfin et les clients Subsonic sans code dédié.

// Sources des playlists système.
const (
	SourceCharts   = "pse-charts"
	SourceDiscover = "pse-discover"
)

// SystemEvery : cadence de régénération des playlists système.
const SystemEvery = 12 * time.Hour

// ChartPlaylist est un top Deezer d'un pays.
type ChartPlaylist struct {
	Code string
	ID   int64
	Name string
}

// chartPlaylists : playlists « Top … » publiées par le compte Deezer Charts.
var chartPlaylists = []ChartPlaylist{
	{"FR", 1109890291, "Top France"},
	{"WW", 3155776842, "Top Monde"},
	{"US", 1313621735, "Top États-Unis"},
	{"UK", 1111142221, "Top Royaume-Uni"},
	{"BE", 1266968331, "Top Belgique"},
	{"CA", 1652248171, "Top Canada"},
	{"DE", 1111143121, "Top Allemagne"},
	{"ES", 1116190041, "Top Espagne"},
	{"IT", 1116187241, "Top Italie"},
	{"BR", 1111141961, "Top Brésil"},
	{"NL", 1266971851, "Top Pays-Bas"},
	{"NG", 1362516565, "Top Nigeria"},
}

// ParseCharts lit une liste de codes pays (PSE_MUSIC_CHARTS), dans l'ordre
// du réglage, sans doublon ni code inconnu.
func ParseCharts(v string) []ChartPlaylist {
	var out []ChartPlaylist
	seen := map[string]bool{}
	for _, c := range strings.FieldsFunc(strings.ToUpper(v), func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if c == "GB" {
			c = "UK"
		}
		for _, p := range chartPlaylists {
			if p.Code == c && !seen[c] {
				seen[c] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// ChartsSetting renvoie les tops choisis (France et Monde par défaut).
func ChartsSetting() []ChartPlaylist {
	v, ok := pseenv.Lookup("PSE_MUSIC_CHARTS")
	if !ok {
		v = "FR,WW"
	}
	return ParseCharts(v)
}

// DiscoverSetting indique si la playlist « Découvertes » est active
// (oui par défaut).
func DiscoverSetting() bool {
	v, ok := pseenv.Lookup("PSE_MUSIC_DISCOVER")
	return !ok || strings.TrimSpace(v) == "" || on(v)
}

// SystemOptions règle la génération des playlists système.
type SystemOptions struct {
	Charts   []ChartPlaylist
	Discover bool
}

// SyncSystemDue régénère les playlists système des spectateurs dont la
// dernière génération date de plus de every.
func (s *PlaylistSync) SyncSystemDue(ctx context.Context, viewers []int64, opt SystemOptions, every time.Duration) {
	charts := map[string][]Track{} // partagés entre spectateurs
	for _, v := range viewers {
		if ctx.Err() != nil {
			return
		}
		if !s.systemDue(ctx, v, opt, every) {
			continue
		}
		s.mark(v, 1)
		s.run.Lock()
		err := s.syncSystemLocked(ctx, v, opt, charts)
		s.run.Unlock()
		s.mark(v, -1)
		if err != nil && ctx.Err() == nil {
			s.logger().Info("playlists système incomplètes", "viewer", v, "err", err)
		}
	}
}

// systemDue compare l'heure de la dernière génération (en mémoire, ou en
// base au premier passage) et le réglage appliqué : un réglage modifié
// relance la génération sans attendre.
func (s *PlaylistSync) systemDue(ctx context.Context, viewer int64, opt SystemOptions, every time.Duration) bool {
	s.mu.Lock()
	last, ok := s.system[viewer]
	s.mu.Unlock()
	if !ok {
		var at int64
		_ = s.Store.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(synced_at),0) FROM pse_music_playlists WHERE viewer_id=? AND source IN (?,?)`,
			viewer, SourceCharts, SourceDiscover).Scan(&at)
		if at == 0 {
			return true
		}
		last = systemRun{at: time.Unix(at, 0), sig: systemSig(opt)}
		s.mu.Lock()
		if s.system == nil {
			s.system = map[int64]systemRun{}
		}
		s.system[viewer] = last
		s.mu.Unlock()
	}
	return last.sig != systemSig(opt) || s.Store.now().Sub(last.at) >= every
}

type systemRun struct {
	at  time.Time
	sig string
}

func systemSig(opt SystemOptions) string {
	codes := make([]string, 0, len(opt.Charts))
	for _, c := range opt.Charts {
		codes = append(codes, c.Code)
	}
	return strings.Join(codes, ",") + "|" + strconv.FormatBool(opt.Discover)
}

// SyncSystem régénère tout de suite les playlists système d'un spectateur.
func (s *PlaylistSync) SyncSystem(ctx context.Context, viewer int64, opt SystemOptions) error {
	s.mark(viewer, 1)
	defer s.mark(viewer, -1)
	s.run.Lock()
	defer s.run.Unlock()
	return s.syncSystemLocked(ctx, viewer, opt, map[string][]Track{})
}

func (s *PlaylistSync) syncSystemLocked(ctx context.Context, viewer int64, opt SystemOptions, charts map[string][]Track) error {
	var errs []error
	var lists []Playlist
	for _, c := range opt.Charts {
		tracks, ok := charts[c.Code]
		if !ok {
			var err error
			tracks, err = s.chartTracks(ctx, c)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			charts[c.Code] = tracks
		}
		if len(tracks) > 0 {
			lists = append(lists, Playlist{Ref: "top-" + strings.ToLower(c.Code), Name: c.Name + " · Deezer", Tracks: tracks})
		}
	}
	// Un top en échec garde son ancienne version : on ne remplace que si
	// tout a été lu, ou si le réglage a retiré des pays.
	if len(errs) == 0 || len(lists) > 0 {
		if err := s.replaceKeeping(ctx, viewer, SourceCharts, lists, len(errs) > 0); err != nil {
			errs = append(errs, err)
		}
	}
	var disc []Playlist
	if opt.Discover {
		tracks, err := s.discoverTracks(ctx, viewer)
		if err != nil {
			errs = append(errs, err)
		}
		if len(tracks) > 0 {
			disc = []Playlist{{Ref: "discover", Name: "Découvertes · Ether-Remix", Tracks: tracks}}
		}
		if err == nil || len(disc) > 0 {
			if rerr := s.Store.replace(ctx, viewer, SourceDiscover, disc); rerr != nil {
				errs = append(errs, rerr)
			}
		}
	} else if err := s.Store.replace(ctx, viewer, SourceDiscover, nil); err != nil {
		errs = append(errs, err)
	}
	if ctx.Err() == nil {
		s.mu.Lock()
		if s.system == nil {
			s.system = map[int64]systemRun{}
		}
		s.system[viewer] = systemRun{at: s.Store.now(), sig: systemSig(opt)}
		s.mu.Unlock()
	}
	return errors.Join(errs...)
}

// replaceKeeping remplace les playlists d'une source ; partial garde
// celles qui manquent (lecture en échec) au lieu de les supprimer.
func (s *PlaylistSync) replaceKeeping(ctx context.Context, viewer int64, source string, lists []Playlist, partial bool) error {
	if partial {
		old, err := s.Store.List(ctx, viewer)
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for _, l := range lists {
			have[l.Ref] = true
		}
		for _, o := range old {
			if o.Source == source && !have[o.Ref] {
				lists = append(lists, o)
			}
		}
	}
	return s.Store.replace(ctx, viewer, source, lists)
}

func (s *PlaylistSync) chartTracks(ctx context.Context, c ChartPlaylist) ([]Track, error) {
	var r struct{ Data []dzTrack }
	if err := s.dz(ctx, "/playlist/"+strconv.FormatInt(c.ID, 10)+"/tracks", map[string][]string{"limit": {"100"}}, &r); err != nil {
		return nil, errors.New(c.Name + " : " + deezerErr(err).Error())
	}
	return readable(r.Data), nil
}

// --- Découvertes ---

const (
	discoverSeeds   = 10 // artistes de départ
	discoverArtists = 25 // artistes proposés
	discoverPerArt  = 2  // titres par artiste proposé
)

type seed struct {
	id     int64
	name   string
	weight int
}

// discoverTracks propose des titres d'artistes proches des artistes les
// plus écoutés (90 derniers jours) et les plus présents dans les
// playlists du spectateur, en écartant les artistes qu'il connaît déjà.
func (s *PlaylistSync) discoverTracks(ctx context.Context, viewer int64) ([]Track, error) {
	known := map[string]bool{} // noms repliés
	knownID := map[int64]bool{}
	byName := map[string]int64{}
	weights := map[int64]*seed{}

	lists, err := s.Store.List(ctx, viewer)
	if err != nil {
		return nil, err
	}
	for _, l := range lists {
		if l.Source == SourceCharts || l.Source == SourceDiscover {
			continue
		}
		for _, t := range l.Tracks {
			if t.Artist.ID <= 0 {
				continue
			}
			f := foldWords(t.Artist.Name)
			known[f], knownID[t.Artist.ID] = true, true
			byName[f] = t.Artist.ID
			sd := weights[t.Artist.ID]
			if sd == nil {
				sd = &seed{id: t.Artist.ID, name: t.Artist.Name}
				weights[t.Artist.ID] = sd
			}
			sd.weight++
		}
	}

	since := s.Store.now().Add(-90 * 24 * time.Hour).Unix()
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT artist, MAX(track_id), COUNT(*) FROM pse_music_plays
WHERE viewer_id=? AND played_at>=? AND artist<>'' GROUP BY lower(artist) ORDER BY COUNT(*) DESC LIMIT 30`, viewer, since)
	if err != nil {
		return nil, err
	}
	type played struct {
		name  string
		track int64
		n     int
	}
	var plays []played
	for rows.Next() {
		var p played
		if rows.Scan(&p.name, &p.track, &p.n) == nil {
			plays = append(plays, p)
		}
	}
	rows.Close()
	for _, p := range plays {
		f := foldWords(p.name)
		known[f] = true
		id := byName[f]
		if id == 0 && p.track > 0 {
			var t dzTrack
			if err := s.dz(ctx, "/track/"+strconv.FormatInt(p.track, 10), nil, &t); err == nil && t.Artist != nil {
				id = t.Artist.ID
			} else if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		if id <= 0 {
			continue
		}
		knownID[id] = true
		sd := weights[id]
		if sd == nil {
			sd = &seed{id: id, name: p.name}
			weights[id] = sd
		}
		sd.weight += 5 * p.n // une écoute pèse plus qu'une présence en playlist
	}
	seeds := make([]*seed, 0, len(weights))
	for _, sd := range weights {
		seeds = append(seeds, sd)
	}
	sort.Slice(seeds, func(i, j int) bool {
		if seeds[i].weight != seeds[j].weight {
			return seeds[i].weight > seeds[j].weight
		}
		return seeds[i].id < seeds[j].id
	})
	if len(seeds) > discoverSeeds {
		seeds = seeds[:discoverSeeds]
	}
	if len(seeds) == 0 {
		return nil, nil
	}

	type cand struct {
		a     Artist
		score int
		first int
	}
	cands := map[int64]*cand{}
	order := 0
	for si, sd := range seeds {
		var r struct{ Data []dzArtist }
		if err := s.dz(ctx, "/artist/"+strconv.FormatInt(sd.id, 10)+"/related", map[string][]string{"limit": {"12"}}, &r); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		for rank, a := range r.Data {
			if a.ID <= 0 || knownID[a.ID] || known[foldWords(a.Name)] {
				continue
			}
			c := cands[a.ID]
			if c == nil {
				c = &cand{a: a.conv(), first: order}
				cands[a.ID] = c
			}
			order++
			// Proche de plusieurs artistes de départ, et des premiers : en tête.
			c.score += (12 - rank) + 2*(discoverSeeds-si)
		}
	}
	picked := make([]*cand, 0, len(cands))
	for _, c := range cands {
		picked = append(picked, c)
	}
	sort.Slice(picked, func(i, j int) bool {
		if picked[i].score != picked[j].score {
			return picked[i].score > picked[j].score
		}
		return picked[i].first < picked[j].first
	})
	if len(picked) > discoverArtists {
		picked = picked[:discoverArtists]
	}
	var rounds [discoverPerArt][]Track
	for _, c := range picked {
		var r struct{ Data []dzTrack }
		if err := s.dz(ctx, "/artist/"+strconv.FormatInt(c.a.ID, 10)+"/top", map[string][]string{"limit": {strconv.Itoa(discoverPerArt)}}, &r); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		for i, t := range readable(r.Data) {
			if i < discoverPerArt {
				rounds[i] = append(rounds[i], t)
			}
		}
	}
	// Premier titre de chaque artiste, puis les seconds : la liste alterne
	// les artistes.
	var out []Track
	seen := map[int64]bool{}
	for _, r := range rounds {
		for _, t := range r {
			if !seen[t.ID] {
				seen[t.ID] = true
				out = append(out, t)
			}
		}
	}
	return out, nil
}
