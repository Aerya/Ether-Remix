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
	pseMaxStreams         = 10
)

type pseLinkOptions struct {
	Format string
	Count  int
}

type pseLinkOptionsKey struct{}

// pseParseLinkOptions lit « <format>-<n> » (format direct, compact ou
// detaille ; n de 1 à 10).
func pseParseLinkOptions(seg string) (pseLinkOptions, bool) {
	cut := strings.LastIndexByte(seg, '-')
	if cut <= 0 {
		return pseLinkOptions{}, false
	}
	format := seg[:cut]
	if format != pseFormatDirect && format != pseFormatCompact && format != pseFormatDetailed {
		return pseLinkOptions{}, false
	}
	digits := seg[cut+1:]
	if len(digits) == 0 || len(digits) > 2 || strings.TrimLeft(digits, "0123456789") != "" {
		return pseLinkOptions{}, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 1 || n > pseMaxStreams {
		return pseLinkOptions{}, false
	}
	return pseLinkOptions{Format: format, Count: n}, true
}

func pseLinkOptionsFrom(ctx context.Context) pseLinkOptions {
	if o, ok := ctx.Value(pseLinkOptionsKey{}).(pseLinkOptions); ok {
		return o
	}
	return pseLinkOptions{Format: pseFormatDirect, Count: 1}
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
