package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// Only fixed public catalogue endpoints are fetched. Neither search strings nor
// page markup may choose an arbitrary host for server-side requests.
var publicGalleryCache = struct {
	sync.Mutex
	entries map[string]publicGalleryEntry
}{entries: make(map[string]publicGalleryEntry)}

type publicGalleryEntry struct {
	images []galleryImage
	at     time.Time
}

func galleryDocument(ctx context.Context, address string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Ether-Remix artwork browser")
	client := *galleryClient
	client.Timeout = 30 * time.Second
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("source HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if len(data) > 4<<20 {
		return nil, fmt.Errorf("réponse trop volumineuse")
	}
	return data, err
}
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func walk(n *html.Node, f func(*html.Node)) {
	f(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, f)
	}
}
func nodeText(n *html.Node) string {
	var b strings.Builder
	walk(n, func(c *html.Node) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
			b.WriteByte(' ')
		}
	})
	return strings.TrimSpace(b.String())
}

func publicImages(data []byte, provider, name, page string) []galleryImage {
	root, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return nil
	}
	if provider == "MediUX" {
		return mediuxGenreImages(root, page)
	}
	out := []galleryImage{}
	seen := map[string]bool{}
	walk(root, func(n *html.Node) {
		var address, title string
		if provider == "ACdb" && n.Data == "img" && strings.Contains(attr(n, "class"), "primary-img") {
			address, title = attr(n, "src"), attr(n, "alt")
			if strings.HasPrefix(address, "/static/images/") {
				address = "https://acdb.tv" + address
			} else {
				return
			}
		}
		if provider == "ThePosterDB" && attr(n, "data-poster-id") != "" {
			id := attr(n, "data-poster-id")
			for _, r := range id {
				if r < '0' || r > '9' {
					return
				}
			}
			address, title = "https://theposterdb.com/api/assets/"+id, name
		}
		if address == "" || seen[address] {
			return
		}
		seen[address] = true
		out = append(out, galleryImage{Name: title, URL: address, Source: provider, Page: page})
	})
	return out
}
func loadPublicGallery(ctx context.Context, provider, q, group string, genreIDs ...string) ([]galleryImage, error) {
	genreID := ""
	if len(genreIDs) > 0 {
		genreID = genreIDs[0]
	}
	if genreID != "" {
		for _, r := range genreID {
			if r < '0' || r > '9' {
				return nil, fmt.Errorf("genre invalide")
			}
		}
		if len(genreID) > 9 {
			return nil, fmt.Errorf("genre invalide")
		}
	}
	key := provider + ":" + group + ":" + q + ":" + genreID
	publicGalleryCache.Lock()
	cached, ok := publicGalleryCache.entries[key]
	publicGalleryCache.Unlock()
	if ok && time.Since(cached.at) < time.Hour {
		return cached.images, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out := []galleryImage{}
	switch provider {
	case "MediUX":
		if group != "genres" {
			return nil, fmt.Errorf("MediUX : la galerie publique connectée couvre les genres, pas les studios ni les services")
		}
		if genreID != "" {
			page := "https://mediux.pro/genres/" + genreID
			data, err := galleryDocument(ctx, page)
			if err != nil {
				return nil, err
			}
			out = publicImages(data, provider, q, page)
			break
		}
		data, err := galleryDocument(ctx, "https://mediux.pro/genres")
		if err != nil {
			return nil, err
		}
		root, err := html.Parse(strings.NewReader(string(data)))
		if err != nil {
			return nil, err
		}
		pages := []string{}
		seen := map[string]bool{}
		walk(root, func(n *html.Node) {
			href := attr(n, "href")
			if n.Data != "a" || !strings.HasPrefix(href, "/genres/") || !strings.Contains(strings.ToLower(nodeText(n)), q) || seen[href] || len(pages) >= 3 {
				return
			}
			id := strings.TrimPrefix(href, "/genres/")
			if id == "" {
				return
			}
			for _, r := range id {
				if r < '0' || r > '9' {
					return
				}
			}
			seen[href] = true
			pages = append(pages, "https://mediux.pro"+href)
		})
		for _, page := range pages {
			data, err = galleryDocument(ctx, page)
			if err != nil {
				return nil, err
			}
			out = append(out, publicImages(data, provider, q, page)...)
		}
	case "ACdb":
		data, err := galleryDocument(ctx, "https://acdb.tv/posters/range?"+url.Values{"image_type": {"LIBRARY_POSTER"}, "offset": {"0"}, "limit": {"48"}, "search": {q}}.Encode())
		if err != nil {
			return nil, err
		}
		var result struct {
			HTML string `json:"html"`
		}
		if err = json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
		out = publicImages([]byte(result.HTML), provider, q, "https://acdb.tv/library-posters/?search="+url.QueryEscape(q))
	case "ThePosterDB":
		section := "companies"
		if group == "genres" {
			section = "collections"
		}
		data, err := galleryDocument(ctx, "https://theposterdb.com/search?"+url.Values{"term": {q}, "section": {section}}.Encode())
		if err != nil {
			return nil, err
		}
		root, err := html.Parse(strings.NewReader(string(data)))
		if err != nil {
			return nil, err
		}
		pages := []string{}
		seen := map[string]bool{}
		walk(root, func(n *html.Node) {
			href := attr(n, "href")
			prefix := "https://theposterdb.com/posters/"
			if n.Data != "a" || !strings.HasPrefix(href, prefix) || seen[href] || len(pages) >= 3 {
				return
			}
			id := strings.TrimPrefix(href, prefix)
			if id == "" {
				return
			}
			for _, r := range id {
				if r < '0' || r > '9' {
					return
				}
			}
			seen[href] = true
			pages = append(pages, href)
		})
		for _, page := range pages {
			data, err = galleryDocument(ctx, page)
			if err != nil {
				return nil, err
			}
			out = append(out, publicImages(data, provider, q, page)...)
		}
	default:
		return nil, fmt.Errorf("source non prise en charge")
	}
	publicGalleryCache.Lock()
	if len(publicGalleryCache.entries) >= 128 {
		publicGalleryCache.entries = make(map[string]publicGalleryEntry)
	}
	publicGalleryCache.entries[key] = publicGalleryEntry{out, time.Now()}
	publicGalleryCache.Unlock()
	return out, nil
}

// Next.js publishes the genre-specific poster list in its public page payload.
// Restrict extraction to that list: set thumbnails also contain unrelated genres.
func mediuxGenreImages(root *html.Node, page string) []galleryImage {
	out := []galleryImage{}
	seen := map[string]bool{}
	walk(root, func(n *html.Node) {
		if n.Data != "script" || n.FirstChild == nil {
			return
		}
		text := n.FirstChild.Data
		const prefix = "self.__next_f.push("
		start := strings.Index(text, prefix)
		if start < 0 {
			return
		}
		var args []json.RawMessage
		if json.NewDecoder(strings.NewReader(text[start+len(prefix):])).Decode(&args) != nil || len(args) < 2 {
			return
		}
		var payload string
		if json.Unmarshal(args[1], &payload) != nil {
			return
		}
		const key = `"genrePosters":`
		i := strings.Index(payload, key)
		if i < 0 {
			return
		}
		var posters []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		if json.NewDecoder(strings.NewReader(payload[i+len(key):])).Decode(&posters) != nil {
			return
		}
		for _, p := range posters {
			if len(p.ID) != 36 || seen[p.ID] {
				continue
			}
			valid := true
			for _, r := range p.ID {
				if !(r >= 'a' && r <= 'f' || r >= '0' && r <= '9' || r == '-') {
					valid = false
				}
			}
			if !valid {
				continue
			}
			seen[p.ID] = true
			out = append(out, galleryImage{Name: p.Title, URL: "https://api.mediux.pro/assets/" + p.ID, Source: "MediUX", Page: page})
		}
	})
	return out
}
