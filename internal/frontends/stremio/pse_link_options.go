package stremio

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

// Options d'un lien Stremio : un segment « o/<format>-<n> » entre le jeton
// (et « fichiers » le cas échéant) et la ressource. Sans segment, le lien
// garde son comportement d'origine (un seul flux, « direct »). Aucun
// stockage : les options voyagent dans l'URL installée. Voir
// docs/pse/FORK.fr.md.

const (
	pseLinkOptionsSegment = "o"
	pseFormatDirect       = "direct"
	pseFormatCompact      = "compact"
	pseFormatDetailed     = "detaille"
	pseMaxStreams         = 15
	pseLegacyMaxStreams   = 10
)

// pseQuota est le nombre de flux voulus pour une qualité (2160, 1080, 720).
type pseQuota struct{ Height, Count int }

// pseLinkOptions : le format d'affichage et, soit des quotas par qualité
// (« 2160.2-1080.3-720.1 »), soit — ancien lien « detaille-7 » — un nombre
// total de flux Auto compris (Legacy).
type pseLinkOptions struct {
	Format string
	Legacy int
	Quotas []pseQuota
}

type pseLinkOptionsKey struct{}

// pseParseLinkOptions lit « <format>-<quotas> » (format direct, compact ou
// detaille ; quotas « 2160.2-1080.3-720.1 », au plus 15 flux en tout, ou un
// nombre de 1 à 10 pour les anciens liens).
func pseParseLinkOptions(seg string) (pseLinkOptions, bool) {
	format, rest, found := strings.Cut(seg, "-")
	if !found || (format != pseFormatDirect && format != pseFormatCompact && format != pseFormatDetailed) {
		return pseLinkOptions{}, false
	}
	if n, ok := pseSmallInt(rest, 2); ok {
		if n < 1 || n > pseLegacyMaxStreams || (len(rest) == 2 && rest[0] == '0') {
			return pseLinkOptions{}, false
		}
		return pseLinkOptions{Format: format, Legacy: n}, true
	}
	opts := pseLinkOptions{Format: format}
	seen, total := map[int]bool{}, 0
	for _, part := range strings.Split(rest, "-") {
		h, c, ok := strings.Cut(part, ".")
		height, okH := pseSmallInt(h, 4)
		count, okC := pseSmallInt(c, 2)
		if !ok || !okH || !okC || (height != 2160 && height != 1080 && height != 720) || seen[height] || count > pseMaxStreams {
			return pseLinkOptions{}, false
		}
		seen[height] = true
		total += count
		opts.Quotas = append(opts.Quotas, pseQuota{height, count})
	}
	if total > pseMaxStreams {
		return pseLinkOptions{}, false
	}
	return opts, true
}

// pseSmallInt lit un entier décimal d'au plus max chiffres.
func pseSmallInt(s string, max int) (int, bool) {
	if s == "" || len(s) > max || strings.TrimLeft(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func pseLinkOptionsFrom(ctx context.Context) pseLinkOptions {
	if o, ok := ctx.Value(pseLinkOptionsKey{}).(pseLinkOptions); ok {
		return o
	}
	return pseLinkOptions{Format: pseFormatDirect, Legacy: 1}
}

// pseWithOptions lit le segment {opts} ; un segment invalide répond 404.
func pseWithOptions(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts, ok := pseParseLinkOptions(r.PathValue("opts"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), pseLinkOptionsKey{}, opts)))
	}
}

func (i *instance) registerPSELinkOptionRoutes() error {
	base := "/{credential}/" + pseLinkOptionsSegment + "/{opts}"
	routes := []struct {
		pattern string
		handler http.HandlerFunc
	}{
		{base + "/manifest.json", i.manifest},
		{base + "/catalog/{type}/{id}", i.catalog},
		{base + "/catalog/{type}/{id}/{extra}", i.catalog},
		{base + "/meta/{type}/{id}", i.meta},
		{base + "/stream/{type}/{id}", i.stream},
		{"/{credential}/" + pseFilesSegment + "/" + pseLinkOptionsSegment + "/{opts}/manifest.json", i.pseFilesManifest},
		{"/{credential}/" + pseFilesSegment + "/" + pseLinkOptionsSegment + "/{opts}/stream/{type}/{id}", i.stream},
	}
	for _, rt := range routes {
		if err := i.router.Handle(http.MethodGet, rt.pattern, pseWithOptions(rt.handler)); err != nil {
			return err
		}
	}
	return nil
}
