package music

import (
	"context"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// Correspondance stricte entre un titre du catalogue et un résultat de
// fournisseur audio. Une version différente (live, remix, acoustique,
// reprise…) ou une durée trop éloignée est refusée : mieux vaut passer à
// la source suivante que lancer le mauvais enregistrement.

// versionWords désigne un autre enregistrement que l'original.
var versionWords = map[string]string{
	"live": "live", "concert": "live", "unplugged": "acoustic",
	"remix": "remix", "rmx": "remix", "rework": "remix", "bootleg": "remix", "mashup": "remix",
	"acoustic": "acoustic", "acoustique": "acoustic",
	"instrumental": "instrumental", "karaoke": "karaoke",
	"cover": "cover", "tribute": "cover",
	"demo": "demo", "maquette": "demo",
	"extended": "extended", "orchestral": "orchestral", "symphonic": "orchestral",
	"sped": "speed", "slowed": "speed", "nightcore": "speed", "reverb": "speed",
	"8d": "speed", "piano": "piano", "a cappella": "acapella", "acapella": "acapella",
}

var versionWordRe = regexp.MustCompile(`[a-z0-9]+`)

// versionOf renvoie les marqueurs de version d'un titre (« Song (Live) »
// donne {live}). Les mentions neutres (remastered, radio edit, deluxe…)
// ne comptent pas.
func versionOf(title string) map[string]bool {
	out := map[string]bool{}
	f := foldWords(title)
	if strings.Contains(f, "a cappella") {
		out["acapella"] = true
	}
	for _, w := range versionWordRe.FindAllString(f, -1) {
		if v, ok := versionWords[w]; ok {
			out[v] = true
		}
	}
	return out
}

func sameVersion(a, b string) bool {
	va, vb := versionOf(a), versionOf(b)
	if len(va) != len(vb) {
		return false
	}
	for k := range va {
		if !vb[k] {
			return false
		}
	}
	return true
}

// seconds ramène une durée en secondes (certains fournisseurs comptent en
// millisecondes).
func seconds(d int64) int64 {
	if d > 10000 {
		return d / 1000
	}
	return d
}

// candidate est un résultat de fournisseur à comparer.
type candidate struct {
	title, artist, isrc string
	duration            int64 // secondes ou millisecondes
}

// matchScore note un résultat ; ok est faux s'il doit être refusé.
func matchScore(c candidate, t Track) (score int, ok bool) {
	if c.isrc != "" && t.ISRC != "" {
		if strings.EqualFold(c.isrc, t.ISRC) {
			return 100, true
		}
		return 0, false
	}
	if !sameVersion(c.title, t.Title) {
		return 0, false
	}
	ct, tt := foldWords(stripTrackExtras(c.title)), foldWords(stripTrackExtras(t.Title))
	if ct == "" || tt == "" {
		return 0, false
	}
	switch {
	case foldWords(c.title) == foldWords(t.Title):
		score += 4
	case ct == tt:
		score += 3
	default:
		return 0, false
	}
	ca, ta := fold(c.artist), fold(t.Artist.Name)
	if ca != "" && ta != "" {
		if !strings.Contains(ca, ta) && !strings.Contains(ta, ca) {
			return 0, false
		}
		score += 2
	}
	if t.Duration > 0 && c.duration > 0 {
		d := seconds(c.duration) - int64(t.Duration)
		if d < 0 {
			d = -d
		}
		switch {
		case d <= 3:
			score += 2
		case d <= 10:
			score++
		case d > 20:
			return 0, false
		}
	}
	return score, true
}

// skipKey transporte les liens audio déjà refusés pendant une lecture.
type skipKey struct{}

// WithSkippedStreams marque des liens audio à ne plus proposer : la source
// suivante est essayée.
func WithSkippedStreams(ctx context.Context, urls ...string) context.Context {
	prev, _ := ctx.Value(skipKey{}).(map[string]bool)
	next := make(map[string]bool, len(prev)+len(urls))
	for k := range prev {
		next[k] = true
	}
	for _, u := range urls {
		if u != "" {
			next[u] = true
		}
	}
	return context.WithValue(ctx, skipKey{}, next)
}

// skipped indique si un lien audio a déjà été refusé.
func skipped(ctx context.Context, u string) bool {
	m, _ := ctx.Value(skipKey{}).(map[string]bool)
	return m[u]
}

// allowLocalAudio lève la protection (tests sur serveur local).
var allowLocalAudio = false

// checkAudioHost refuse un lien audio qui viserait la machine elle-même ou
// une adresse de service (boucle locale, lien local, métadonnées cloud) :
// les liens viennent d'addons tiers. Le réseau local reste permis (addons
// auto-hébergés, résolution DNS locale).
func checkAudioHost(ctx context.Context, link string) error {
	if allowLocalAudio {
		return nil
	}
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("music: lien audio invalide")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errors.New("music: lien audio vers la machine locale refusé")
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil // le proxy éventuel résoudra ; l'échec viendra de la requête
		}
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return errors.New("music: lien audio vers une adresse réservée refusé")
		}
	}
	return nil
}
