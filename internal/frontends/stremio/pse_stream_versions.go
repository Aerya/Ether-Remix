package stremio

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// Liste de flux Stremio : le flux Auto, puis les releases classées par le
// résolveur (celles que l'Auto essaie, dans son ordre, rejetées exclues), au
// format et selon les quotas par qualité du lien (voir pse_link_options.go).
// Chaque flux lit exactement sa release (version exacte). Sans option, le
// comportement d'origine (un seul flux) reste en place. Voir
// docs/pse/FORK.fr.md.

// pseVersionListBudget borne la recherche et la construction de la liste ;
// au-delà, seul le flux Auto est proposé.
const pseVersionListBudget = 4 * time.Second

// pseVersionStreams répond la liste de flux et renvoie true quand le lien
// demande un affichage autre que « direct ». Sinon (ou si la fiche est
// introuvable) il renvoie false et stream() garde son flux unique.
func (i *instance) pseVersionStreams(ctx context.Context, w http.ResponseWriter, r *http.Request, credential string, viewer *frontend.Viewer, device frontend.Device, plan frontend.PlanRequest, base *url.URL, autoLazy string) bool {
	opts := pseLinkOptionsFrom(r.Context())
	if opts.Format == pseFormatDirect {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, pseVersionListBudget)
	defer cancel()
	detail, err := i.host.Experience.Item(ctx, frontend.ItemRequest{Viewer: viewer, Device: device, Key: plan.Item})
	if err != nil || len(detail.Versions) == 0 {
		return false
	}
	ranked := i.pseRanked(ctx, viewer, plan, detail.Title)
	runtime := detail.Title.Runtime
	var streams []any
	if len(ranked) > 0 {
		streams = append(streams, pseStreamEntry(opts.Format, pseFactsOf(ranked[0], runtime), pseLazyURL(base, autoLazy), true))
	} else {
		auto := map[string]any{"name": "Ether", "url": pseLazyURL(base, autoLazy), "description": detail.Versions[0].Name}
		writeJSON(w, map[string]any{"streams": []any{auto}})
		return true
	}
	scope := frontend.VersionScope{Viewer: viewer, Device: device, Protocol: "stremio", Caps: plan.Caps}
	for _, v := range pseSelect(opts, ranked) {
		if ctx.Err() != nil || i.host.VersionHandles == nil {
			break
		}
		handle, err := i.host.VersionHandles.Issue(ctx, scope, plan.Item, v.ID)
		if err != nil || handle == "" {
			continue
		}
		exact := plan
		exact.Version = handle
		exact.Intent = frontend.PlayIntent{Estimate: true, ExactVersion: true, Options: frontend.DecideOptions{DirectPlay: true}}
		lazy, err := i.host.LazyStreams.Issue(ctx, credential, exact)
		if err != nil || !strings.HasPrefix(lazy, "/sessions/lazy/") {
			continue
		}
		streams = append(streams, pseStreamEntry(opts.Format, pseFactsOf(v, runtime), pseLazyURL(base, lazy), false))
	}
	writeJSON(w, map[string]any{"streams": streams})
	return true
}

// pseRanked lit les releases classées ; si la recherche n'est pas en cache,
// la lance et l'attend dans le budget restant (aucune mise en file de
// téléchargement : ce n'est qu'une recherche).
func (i *instance) pseRanked(ctx context.Context, viewer *frontend.Viewer, plan frontend.PlanRequest, title frontend.Title) []frontend.ReleaseVariant {
	src, ok := i.host.Editions.(frontend.RankedReleases)
	if !ok {
		return nil
	}
	req := frontend.ResolveRequest{Title: &title, User: viewer, Client: plan.Caps}
	ranked, _ := src.RankedReleases(ctx, req)
	if len(ranked) > 0 || title.Key.IsPluginKey() || (title.Type != "movie" && title.Type != "episode") {
		return ranked
	}
	if !title.Released.IsZero() && title.Released.After(time.Now()) {
		return nil
	}
	t := title
	done := i.host.Editions.RefillSearchCache(&t)
	if done == nil {
		return nil
	}
	select {
	case <-done:
	case <-ctx.Done():
		return nil
	}
	ranked, _ = src.RankedReleases(ctx, req)
	return ranked
}

// pseHeightBucket range une release dans 2160, 1080 ou 720 ; 0 sinon.
func pseHeightBucket(height int) int {
	switch {
	case height >= 2000:
		return 2160
	case height >= 1000:
		return 1080
	case height >= 700:
		return 720
	}
	return 0
}

// pseSelect applique les quotas (ou le nombre total des anciens liens) aux
// releases classées, en gardant leur ordre.
func pseSelect(opts pseLinkOptions, ranked []frontend.ReleaseVariant) []frontend.ReleaseVariant {
	var out []frontend.ReleaseVariant
	seen := map[string]bool{}
	uniq := ranked[:0:0]
	for _, v := range ranked {
		k := strings.ToLower(v.Release.FileName)
		if k == "" {
			k = strings.ToLower(v.Release.Name)
		}
		if k == "" || !seen[k] {
			seen[k] = true
			uniq = append(uniq, v)
		}
	}
	ranked = uniq
	if opts.Quotas == nil {
		for _, v := range ranked {
			if len(out) >= opts.Legacy-1 {
				break
			}
			out = append(out, v)
		}
		return out
	}
	left := map[int]int{}
	for _, q := range opts.Quotas {
		left[q.Height] = q.Count
	}
	for _, v := range ranked {
		b := pseHeightBucket(v.Release.Info.Height)
		if left[b] > 0 {
			left[b]--
			out = append(out, v)
		}
	}
	return out
}

func pseLazyURL(base *url.URL, lazy string) string {
	u := *base
	u.Path = strings.TrimSuffix(u.Path, "/image/") + lazy
	u.RawPath = ""
	return u.String()
}

func pseStreamEntry(format string, f pseFacts, streamURL string, auto bool) map[string]any {
	entry := map[string]any{"url": streamURL}
	hints := map[string]any{"bingeGroup": "ether"}
	if f.res != "" {
		hints["bingeGroup"] = "ether-" + f.res
	}
	if f.size > 0 {
		hints["videoSize"] = f.size
	}
	entry["behaviorHints"] = hints
	if format == pseFormatDetailed {
		entry["name"] = f.serviceName()
		entry["description"] = f.detailed(auto)
	} else {
		entry["name"] = strings.TrimSpace(f.serviceName() + " " + f.res)
		entry["description"] = f.compact(auto)
	}
	return entry
}

func pseSizeFR(bytes int64) string {
	if bytes <= 0 {
		return ""
	}
	gb := float64(bytes) / (1 << 30)
	if gb >= 1 {
		return strings.Replace(fmt.Sprintf("%.1f", gb), ".", ",", 1) + " Go"
	}
	return fmt.Sprintf("%d Mo", (bytes+(1<<20)/2)>>20)
}
