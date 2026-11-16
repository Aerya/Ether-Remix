package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

type galleryImage struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Source string `json:"source"`
}

var galleryCache struct {
	sync.Mutex
	images []galleryImage
	at     time.Time
}
var galleryClient = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirect refused") }}

func loadGallery(ctx context.Context) ([]galleryImage, error) {
	galleryCache.Lock()
	defer galleryCache.Unlock()
	if time.Since(galleryCache.at) < 6*time.Hour && galleryCache.images != nil {
		return galleryCache.images, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/nichtlegacy/kometa-config-images/git/trees/main?recursive=1", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Ether-Remix")
	res, err := galleryClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("gallery HTTP %d", res.StatusCode)
	}
	var tree struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&tree); err != nil {
		return nil, err
	}
	if tree.Truncated {
		return nil, fmt.Errorf("gallery listing truncated")
	}
	out := []galleryImage{}
	for _, item := range tree.Tree {
		ext := strings.ToLower(path.Ext(item.Path))
		if item.Type != "blob" || (ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp") {
			continue
		}
		parts := strings.Split(item.Path, "/")
		for n, p := range parts {
			parts[n] = url.PathEscape(p)
		}
		out = append(out, galleryImage{Name: strings.TrimSuffix(path.Base(item.Path), path.Ext(item.Path)), URL: "https://raw.githubusercontent.com/nichtlegacy/kometa-config-images/main/" + strings.Join(parts, "/"), Source: item.Path})
	}
	galleryCache.images = out
	galleryCache.at = time.Now()
	return out, nil
}
func (s *Service) gallery(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if len(q) < 2 || len(q) > 180 {
		http.Error(w, "Saisissez au moins deux caractères", 400)
		return
	}
	images, err := loadGallery(r.Context())
	if err != nil {
		http.Error(w, "Galerie indisponible ; utilisez une URL ou un fichier", 502)
		return
	}
	results := []galleryImage{}
	for _, image := range images {
		if strings.Contains(strings.ToLower(image.Name), q) {
			results = append(results, image)
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"images": results})
}
