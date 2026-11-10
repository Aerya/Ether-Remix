package music

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Diagnostic des sources audio pour la console : chaque source configurée
// est essayée séparément sur un même morceau, puis le lien obtenu est
// ouvert (premiers octets seulement) pour connaître le format réellement
// servi. Rien n'est gardé, et les caches de lecture ne sont pas utilisés :
// chaque test repart de zéro. La lecture complète n'est jamais testée.

// SourceReport est le résultat d'une source.
type SourceReport struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // eclipse, dmm, alldebrid, realdebrid, torbox, audius, hifi
	Priority int    `json:"priority"`
	OK       bool   `json:"ok"`
	// Quality : qualité annoncée par la source ; Format : format constaté
	// à l'ouverture du lien (FLAC, MP3, AAC, Opus…).
	Quality    string `json:"quality,omitempty"`
	Format     string `json:"format,omitempty"`
	Lossless   bool   `json:"lossless"`
	Error      string `json:"error,omitempty"`
	SampleRate int    `json:"sampleRate,omitempty"`
	BitDepth   int    `json:"bitDepth,omitempty"`
	HiRes      bool   `json:"hiRes"`
	Millis     int64  `json:"ms"`
	// Stage : dernière étape franchie. "source" (la source a donné un
	// résultat), "link" (lien audio obtenu et joignable), "bytes" (format
	// audio vérifié dans les premiers octets). Vide : rien obtenu.
	Stage string `json:"stage,omitempty"`
}

// PlanEntry décrit une source de la chaîne de lecture, active ou non.
type PlanEntry struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Active bool   `json:"active"`
	Order  int    `json:"order,omitempty"` // rang dans la chaîne (actives)
	Reason string `json:"reason,omitempty"`
}

// Diagnosis est le résultat d'un test.
type Diagnosis struct {
	Track string `json:"track"`
	// Catalog : le titre a été trouvé dans le catalogue Deezer.
	Catalog bool           `json:"catalog"`
	Plan    []PlanEntry    `json:"plan"`
	Sources []SourceReport `json:"sources"`
}

type namedResolver struct {
	name, kind string
	r          Resolver
}

// addonName ne garde que l'hôte d'un lien d'addon (le chemin et la requête
// peuvent contenir un jeton).
func addonName(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return "addon"
}

// formatOf traduit un type MIME en format lisible.
func formatOf(mime string) (string, bool) {
	m := strings.ToLower(mime)
	switch {
	case strings.Contains(m, "flac"):
		return "FLAC", true
	case strings.Contains(m, "alac"):
		return "ALAC", true
	case strings.Contains(m, "wav"):
		return "WAV", true
	case strings.Contains(m, "mpeg"), strings.Contains(m, "mp3"):
		return "MP3", false
	case strings.Contains(m, "opus"), strings.Contains(m, "ogg"):
		return "Opus/Vorbis", false
	case strings.Contains(m, "mp4"), strings.Contains(m, "aac"), strings.Contains(m, "m4a"):
		return "AAC/M4A", false
	case strings.Contains(m, "mpegurl"):
		return "HLS (non pris en charge)", false
	case strings.Contains(m, "dash"):
		return "DASH (non pris en charge)", false
	}
	return "", false
}

// Diagnose teste chaque source sur le premier titre trouvé pour query.
func Diagnose(ctx context.Context, cfg Settings, debrid DebridSources, query string, hc *http.Client) (Diagnosis, error) {
	return DiagnoseSource(ctx, cfg, debrid, query, "", hc)
}

// chain renvoie les sources dans l'ordre réel de lecture (celui de
// Settings.NewSource), et le plan complet avec les sources inactives.
func chain(cfg Settings, debrid DebridSources, hc *http.Client) ([]namedResolver, []PlanEntry) {
	var rs []namedResolver
	var plan []PlanEntry
	add := func(n namedResolver) {
		rs = append(rs, n)
		plan = append(plan, PlanEntry{Name: n.name, Kind: n.kind, Active: true, Order: len(rs)})
	}
	off := func(name, kind, reason string) {
		plan = append(plan, PlanEntry{Name: name, Kind: kind, Reason: reason})
	}
	if cfg.Audius == "first" {
		add(namedResolver{"Audius", "audius", NewAudius(hc)})
	}
	for _, n := range cfg.eclipseSources(hc) {
		add(n)
	}
	if len(ParseEclipseURLs(cfg.EclipseURLs)) == 0 {
		off("Addon Eclipse", "eclipse", "Aucun lien manifest réglé")
	}
	for i, a := range CommunityAddons() {
		if cfg.CommunityURLs[i] == "" {
			off(a.Name, "eclipse", "Manifest personnel non configuré")
		} else {
			active := false
			for _, n := range rs {
				if n.name == a.Name {
					active = true
					break
				}
			}
			if !active {
				off(a.Name, "eclipse", "Manifest déjà utilisé par une source précédente")
			}
		}
	}
	for _, d := range debrid {
		switch {
		case !cfg.DebridEnabled(d.Kind):
			off(d.Name, d.Kind, "Lecture depuis ce service non activée dans les réglages Musique")
		case d.NoAccount || d.Resolver == nil:
			off(d.Name, d.Kind, "Aucun compte actif avec clé dans Sources › Debrid")
		default:
			if many, ok := d.Resolver.(Resolvers); ok && len(many) > 1 {
				// Plusieurs comptes : chacun est une étape de la chaîne.
				for i, r := range many {
					name := fmt.Sprintf("%s #%d", d.Name, i+1)
					if dr, ok := r.(*DebridResolver); ok && dr.Provider != "" {
						name = dr.Provider
					}
					add(namedResolver{name, d.Kind, r})
				}
				continue
			}
			add(namedResolver{d.Name, d.Kind, d.Resolver})
		}
	}
	if cfg.Audius == "last" {
		add(namedResolver{"Audius", "audius", NewAudius(hc)})
	}
	if !cfg.audiusEnabled() {
		off("Audius", "audius", "Désactivé")
	}
	switch {
	case cfg.HiFiURL == "":
		off("HiFi API", "hifi", "Non réglée")
	case cfg.Mode() != "hifi":
		// Jamais ajoutée en repli : HiFi API ne sert que seule.
		off("HiFi API · "+addonName(cfg.HiFiURL), "hifi", "Utilisée seulement quand aucune autre source (Eclipse, Audius, debrid) n'est active")
	default:
		plan = append(plan, PlanEntry{Name: "HiFi API · " + addonName(cfg.HiFiURL), Kind: "hifi", Active: true, Order: len(rs) + 1})
	}
	return rs, plan
}

// DiagnoseSource teste seulement la source nommée only (nom affiché dans
// le rapport), ou toutes si only est vide.
func DiagnoseSource(ctx context.Context, cfg Settings, debrid DebridSources, query, only string, hc *http.Client) (Diagnosis, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	query = strings.TrimSpace(query)
	if query == "" {
		query = "Stromae Papaoutai"
	}
	all, plan := chain(cfg, debrid, hc)
	var rs []namedResolver
	for _, n := range all {
		if only == "" || n.name == only {
			rs = append(rs, n)
		}
	}
	d := Diagnosis{Plan: plan}
	var track Track
	if len(rs) > 0 {
		tracks, err := NewDeezerSource(nil, nil).SearchTracks(ctx, query, 1, 0)
		if err != nil {
			return d, errors.New("catalogue Deezer injoignable")
		}
		if len(tracks) == 0 {
			return d, errors.New("aucun titre trouvé pour cette recherche")
		}
		track = tracks[0]
		d.Track = track.Artist.Name + " — " + track.Title
		d.Catalog = true
	}
	order := map[string]int{}
	for _, p := range plan {
		order[p.Name] = p.Order
	}
	for _, n := range rs {
		start := time.Now()
		rep := SourceReport{Name: n.name, Kind: n.kind, Priority: order[n.name]}
		st, err := n.r.Resolve(ctx, track)
		if err == nil {
			rep.Quality, rep.Stage = st.Quality, "source"
			err = probe(ctx, hc, st, &rep)
		}
		rep.Millis = time.Since(start).Milliseconds()
		if err != nil {
			rep.Error = explain(err)
		} else {
			rep.OK = true
		}
		d.Sources = append(d.Sources, rep)
	}
	if hifi := "HiFi API · " + addonName(cfg.HiFiURL); cfg.HiFiURL != "" && cfg.Mode() == "hifi" && (only == "" || only == hifi) {
		start := time.Now()
		rep := SourceReport{Name: hifi, Kind: "hifi", Priority: 1}
		c := NewClient(cfg.HiFiURL, nil)
		tracks, err := c.SearchTracks(ctx, query, 1, 0)
		if err == nil && len(tracks) == 0 {
			err = ErrNotFound
		}
		if err == nil {
			d.Track = tracks[0].Artist.Name + " — " + tracks[0].Title
			d.Catalog = true
			var st Stream
			if st, err = c.Stream(ctx, tracks[0].ID, cfg.Quality); err == nil {
				rep.Quality, rep.Stage = st.Quality, "source"
				err = probe(ctx, hc, st, &rep)
			}
		}
		rep.Millis = time.Since(start).Milliseconds()
		if err != nil {
			rep.Error = explain(err)
		} else {
			rep.OK = true
		}
		d.Sources = append(d.Sources, rep)
	}
	return d, nil
}

// probe ouvre les premiers octets du lien pour constater le format.
func probe(ctx context.Context, hc *http.Client, st Stream, rep *SourceReport) error {
	if st.File != "" {
		f, err := os.Open(st.File)
		if err != nil {
			return errors.New("fichier du client torrent introuvable")
		}
		defer f.Close()
		rep.Stage = "link"
		b, err := io.ReadAll(io.LimitReader(f, 64<<10))
		if err != nil {
			return errors.New("lecture audio interrompue")
		}
		if !inspectAudio(b, rep) {
			return errors.New("format audio non vérifié dans les premiers octets")
		}
		rep.Stage = "bytes"
		return nil
	}
	if err := checkAudioHost(ctx, st.URL); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, st.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", "bytes=0-65535")
	checked := *hc
	previous := hc.CheckRedirect
	checked.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("trop de redirections audio")
		}
		if err := checkAudioHost(r.Context(), r.URL.String()); err != nil {
			return err
		}
		if previous != nil {
			return previous(r, via)
		}
		return nil
	}
	res, err := checked.Do(req)
	if err != nil {
		return errors.New("lien audio injoignable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusPartialContent {
		return errors.New("lien audio refusé (" + http.StatusText(res.StatusCode) + ")")
	}
	rep.Stage = "link"
	b, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return errors.New("lecture audio interrompue")
	}
	if !inspectAudio(b, rep) {
		return errors.New("format audio non vérifié dans les premiers octets")
	}
	rep.Stage = "bytes"
	return nil
}

func explain(err error) string {
	switch {
	case errors.Is(err, ErrNotFound):
		return "Titre absent de cette source, ou aucune version identique (live, remix… refusés)"
	case errors.Is(err, context.DeadlineExceeded):
		return "La source n'a pas répondu à temps"
	case errors.Is(err, ErrNotCached):
		return "Album absent du cache de ce compte"
	}
	return redactErr(err).Error()
}
