package stremio

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// Liste de flux Stremio : le flux Auto puis les versions de la fiche (les
// mêmes que dans Plex et Jellyfin), au format et dans le nombre choisis par
// le lien (voir pse_link_options.go). Sans option, le comportement d'origine
// (un seul flux) reste en place. Voir docs/pse/FORK.fr.md.

// pseVersionListBudget borne toute la construction de la liste ; au-delà,
// seul le flux Auto est proposé.
const pseVersionListBudget = 2 * time.Second

// pseVersionStreams répond la liste de flux et renvoie true quand le lien
// demande un affichage autre que « direct ». Sinon (ou en cas de doute sur
// la fiche) il renvoie false et stream() garde son flux unique.
func (i *instance) pseVersionStreams(ctx context.Context, w http.ResponseWriter, r *http.Request, credential string, viewer *frontend.Viewer, device frontend.Device, plan frontend.PlanRequest, base *url.URL, autoLazy string) bool {
	opts := pseLinkOptionsFrom(r.Context())
	if opts.Format == pseFormatDirect {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, pseVersionListBudget)
	defer cancel()
	detail, ok := i.pseDetailWithReleases(ctx, viewer, device, plan.Item, opts.Count)
	if !ok {
		return false
	}
	streams := []any{pseStreamEntry(opts.Format, detail.Versions[0], pseLazyURL(base, autoLazy), true)}
	scope := frontend.VersionScope{Viewer: viewer, Device: device, Protocol: "stremio", Caps: plan.Caps}
	for _, version := range detail.Versions[1:] {
		if len(streams) >= opts.Count || ctx.Err() != nil {
			break
		}
		if psePlaceholderVersion(version) || i.host.VersionHandles == nil {
			continue
		}
		handle, err := i.host.VersionHandles.Issue(ctx, scope, plan.Item, version.ID)
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
		streams = append(streams, pseStreamEntry(opts.Format, version, pseLazyURL(base, lazy), false))
	}
	writeJSON(w, map[string]any{"streams": streams})
	return true
}

func pseLazyURL(base *url.URL, lazy string) string {
	u := *base
	u.Path = strings.TrimSuffix(u.Path, "/image/") + lazy
	u.RawPath = ""
	return u.String()
}

// psePlaceholderVersion reconnaît les emplacements provisoires du résolveur
// (recherche de releases encore froide).
func psePlaceholderVersion(v frontend.Version) bool {
	return strings.HasPrefix(v.Name, "Same-quality version") || strings.HasPrefix(v.Name, "Other version")
}

// pseDetailWithReleases lit la fiche ; si des emplacements provisoires
// manquent pour atteindre `want` flux, relance la recherche de releases (même
// principe que pseRefillDetail côté Jellyfin) dans le budget restant.
func (i *instance) pseDetailWithReleases(ctx context.Context, viewer *frontend.Viewer, device frontend.Device, key frontend.RatingKey, want int) (frontend.ItemDetail, bool) {
	req := frontend.ItemRequest{Viewer: viewer, Device: device, Key: key}
	detail, err := i.host.Experience.Item(ctx, req)
	if err != nil || len(detail.Versions) == 0 {
		return detail, false
	}
	if i.host.Editions == nil || want <= 1 || detail.Title.Key.IsPluginKey() || (detail.Title.Type != "movie" && detail.Title.Type != "episode") {
		return detail, true
	}
	if !detail.Title.Released.IsZero() && detail.Title.Released.After(time.Now()) {
		return detail, true
	}
	real := 0
	for _, v := range detail.Versions[1:] {
		if !psePlaceholderVersion(v) {
			real++
		}
	}
	if real >= want-1 {
		return detail, true
	}
	t := detail.Title
	done := i.host.Editions.RefillSearchCache(&t)
	if done == nil {
		return detail, true
	}
	select {
	case <-done:
	case <-ctx.Done():
		return detail, true
	}
	if fresh, err := i.host.Experience.Item(ctx, req); err == nil && len(fresh.Versions) > 0 {
		return fresh, true
	}
	return detail, true
}

func pseStreamEntry(format string, v frontend.Version, streamURL string, auto bool) map[string]any {
	res := pseResolution(v)
	entry := map[string]any{"url": streamURL}
	hints := map[string]any{"bingeGroup": "ether"}
	if res != "" {
		hints["bingeGroup"] = "ether-" + res
	}
	if v.Media.Size > 0 {
		hints["videoSize"] = v.Media.Size
	}
	entry["behaviorHints"] = hints
	if format == pseFormatDetailed {
		entry["name"] = strings.TrimSpace("Ether ⚡ " + res)
		entry["description"] = pseDetailedDescription(v, auto)
	} else {
		entry["name"] = strings.TrimSpace("Ether " + res)
		if d := pseCompactDescription(v); d != "" {
			entry["description"] = d
		}
	}
	return entry
}

var pseResNameRE = regexp.MustCompile(`(?i)\b(\d{3,4})p\b|\b4K\b`)

// pseResolution est la définition d'une version (« 1080p »), tirée de la
// vidéo sondée puis, à défaut, de son nom.
func pseResolution(v frontend.Version) string {
	if vs := v.Media.Video(); vs != nil {
		switch {
		case vs.Width >= 3000 || vs.Height >= 2000:
			return "2160p"
		case vs.Width >= 1800 || vs.Height >= 1000:
			return "1080p"
		case vs.Width >= 1200 || vs.Height >= 700:
			return "720p"
		case vs.Height > 0:
			return fmt.Sprintf("%dp", vs.Height)
		}
	}
	if m := pseResNameRE.FindStringSubmatch(v.Name + " " + v.FileName); m != nil {
		if m[1] != "" {
			return m[1] + "p"
		}
		return "2160p"
	}
	return ""
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

// pseCompactDescription tient sur une ligne : le libellé de la version, puis
// la taille et le montage s'ils n'y figurent pas.
func pseCompactDescription(v frontend.Version) string {
	parts := []string{strings.TrimSpace(v.Name)}
	if size := pseSizeFR(v.Media.Size); size != "" && !strings.Contains(v.Name, " Go") && !strings.Contains(v.Name, " GB") {
		parts = append(parts, size)
	}
	if e := strings.TrimSpace(v.Edition); e != "" && !strings.Contains(strings.ToLower(v.Name), strings.ToLower(e)) {
		parts = append(parts, e)
	}
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

var pseGroupRE = regexp.MustCompile(`-([A-Za-z0-9]+)\.[A-Za-z0-9]{2,4}$`)

func pseDetailedDescription(v frontend.Version, auto bool) string {
	var lines []string
	add := func(sep string, parts ...string) {
		var kept []string
		for _, p := range parts {
			if p != "" {
				kept = append(kept, p)
			}
		}
		if len(kept) > 0 {
			lines = append(lines, strings.Join(kept, sep))
		}
	}
	const sep = " • "
	add(sep, pseResolution(v), pseSource(v), pseHDR(v))
	add(sep, prefixed("🌐 ", pseLanguages(v)), prefixed("💾 ", pseSizeFR(v.Media.Size)), prefixed("✂ ", strings.TrimSpace(v.Edition)))
	group := ""
	if m := pseGroupRE.FindStringSubmatch(v.FileName); m != nil {
		group = m[1]
	}
	add(sep, prefixed("🎞 ", pseVideoCodec(v)), prefixed("🔊 ", pseAudio(v)), prefixed("👥 ", group))
	if v.FileName != "" {
		lines = append(lines, "📁 "+v.FileName)
	}
	if len(lines) == 0 {
		lines = append(lines, strings.TrimSpace(v.Name))
	} else if auto {
		lines = append([]string{"Auto"}, lines...)
	}
	return strings.Join(lines, "\n")
}

func prefixed(prefix, s string) string {
	if s == "" {
		return ""
	}
	return prefix + s
}

func pseSource(v frontend.Version) string {
	s := strings.ToLower(v.Name + " " + v.FileName)
	switch {
	case strings.Contains(s, "remux"):
		return "BluRay REMUX"
	case strings.Contains(s, "blu-ray"), strings.Contains(s, "bluray"), strings.Contains(s, "bdrip"):
		return "BluRay"
	case strings.Contains(s, "web-dl"), strings.Contains(s, "webdl"):
		return "WEB-DL"
	case strings.Contains(s, "webrip"):
		return "WEBRip"
	case strings.Contains(s, "hdtv"):
		return "HDTV"
	case strings.Contains(s, "dvdrip"):
		return "DVDRip"
	}
	return ""
}

func pseHDR(v frontend.Version) string {
	vs := v.Media.Video()
	if vs == nil {
		return ""
	}
	var out []string
	if vs.DolbyVision {
		out = append(out, "Dolby Vision")
	}
	switch {
	case vs.HDR10Plus != nil && *vs.HDR10Plus:
		out = append(out, "HDR10+")
	case vs.ColorTransfer == "smpte2084":
		out = append(out, "HDR10")
	case vs.ColorTransfer == "arib-std-b67":
		out = append(out, "HLG")
	}
	return strings.Join(out, " ")
}

func pseVideoCodec(v frontend.Version) string {
	vs := v.Media.Video()
	if vs == nil {
		return ""
	}
	names := map[string]string{"h264": "H.264", "hevc": "HEVC", "av1": "AV1", "vp9": "VP9", "mpeg2video": "MPEG-2", "vc1": "VC-1"}
	if n, ok := names[strings.ToLower(vs.Codec)]; ok {
		return n
	}
	return strings.ToUpper(vs.Codec)
}

func pseAudio(v frontend.Version) string {
	var a *frontend.StreamInfo
	for n := range v.Media.Streams {
		if v.Media.Streams[n].Type == "audio" {
			a = &v.Media.Streams[n]
			break
		}
	}
	if a == nil {
		return ""
	}
	names := map[string]string{"eac3": "DD+", "ac3": "DD", "truehd": "TrueHD", "aac": "AAC", "flac": "FLAC", "opus": "Opus", "mp3": "MP3"}
	codec := strings.ToLower(a.Codec)
	name, ok := names[codec]
	if codec == "dts" {
		name, ok = "DTS", true
		switch p := strings.ToLower(a.Profile); {
		case strings.Contains(p, "ma"):
			name = "DTS-HD MA"
		case strings.Contains(p, "x"):
			name = "DTS:X"
		}
	}
	if !ok {
		name = strings.ToUpper(a.Codec)
	}
	layout := map[int]string{1: "1.0", 2: "2.0", 6: "5.1", 8: "7.1"}[a.Channels]
	return strings.TrimSpace(name + " " + layout)
}

var pseLangNames = map[string]string{"fre": "FR", "fra": "FR", "fr": "FR", "eng": "EN", "en": "EN", "spa": "ES", "es": "ES", "ger": "DE", "deu": "DE", "de": "DE", "ita": "IT", "it": "IT", "jpn": "JA", "ja": "JA", "kor": "KO", "ko": "KO", "por": "PT", "pt": "PT", "rus": "RU", "ru": "RU", "chi": "ZH", "zho": "ZH", "zh": "ZH"}

func pseLanguages(v frontend.Version) string {
	seen := map[string]bool{}
	var out []string
	for _, s := range v.Media.Streams {
		if s.Type != "audio" || s.Language == "" {
			continue
		}
		code := strings.ToLower(s.Language)
		name, ok := pseLangNames[code]
		if !ok {
			name = strings.ToUpper(code)
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return strings.Join(out, ", ")
}
