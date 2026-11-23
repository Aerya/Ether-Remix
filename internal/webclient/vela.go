package webclient

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend/kit/clientnet"
	"github.com/braedonsaunders/ether/internal/veladist"
)

var (
	htmlTitlePattern = regexp.MustCompile(`(?is)<title>.*?</title>`)
	htmlIconPattern  = regexp.MustCompile(`(?is)<link\b[^>]*\brel\s*=\s*["']icon["'][^>]*>`)
)

// VelaIconAsset is a pre-rendered owner PWA icon served in place of the
// release's built-in icon files.
type VelaIconAsset struct {
	ContentType string
	ETag        string
	Data        []byte
	IsSVG       bool
}

// VelaBrand carries the owner's optional Vela app name and icon resolver for
// dynamic index.html, manifest.webmanifest, and PWA icon serving.
type VelaBrand struct {
	AppName        string
	IconVersion    string
	IconIsSVG      bool
	IconAsset      func(name string) (VelaIconAsset, bool)
	RedirectPrefix string
}

var velaPWAIconPaths = map[string]string{
	"vela-icon.svg":              "image/svg+xml",
	"vela-icon-180.png":          "image/png",
	"vela-icon-192.png":          "image/png",
	"vela-icon-512.png":          "image/png",
	"vela-icon-maskable-192.png": "image/png",
	"vela-icon-maskable-512.png": "image/png",
	"favicon-16.png":             "image/png",
	"favicon-32.png":             "image/png",
	"favicon-48.png":             "image/png",
}

type velaShellKey struct {
	dir         string
	file        string
	modTime     int64
	size        int64
	name        string
	iconVersion string
	iconIsSVG   bool
	basePath    string
}

func velaBasePath(brand VelaBrand) string {
	return clientnet.RedirectPath(brand.RedirectPrefix, "", "/vela")
}

type velaShellDoc struct {
	contentType string
	etag        string
	body        []byte
	isSVG       bool
	modTime     time.Time
}

var (
	velaShellMu    sync.Mutex
	velaShellCache = map[velaShellKey]velaShellDoc{}
)

// VelaReady reports an owner-installed optional build. No download is attempted
// until a pinned Vela release artifact is published and verified by the host.
func VelaReady(dir string) bool {
	if dir == "" {
		return false
	}
	s, e := os.Stat(filepath.Join(dir, "index.html"))
	return e == nil && !s.IsDir()
}

// ServeVela serves the installed build with SPA deep-link fallback and default branding.
func ServeVela(w http.ResponseWriter, r *http.Request, dir string) {
	ServeVelaWithBrand(w, r, dir, VelaBrand{})
}

// ServeVelaBrand serves the installed build with SPA deep-link fallback, patching
// the root HTML <title> and web manifest name/short_name when appName is set.
func ServeVelaBrand(w http.ResponseWriter, r *http.Request, dir, appName string) {
	ServeVelaWithBrand(w, r, dir, VelaBrand{AppName: appName})
}

// ServeVelaWithBrand serves the installed Vela build with dynamic branding for
// index.html, manifest.webmanifest, and PWA/favicon icons.
func ServeVelaWithBrand(w http.ResponseWriter, r *http.Request, dir string, brand VelaBrand) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	basePath := velaBasePath(brand)
	if r.URL.Path == "/" || r.URL.Path == "/vela/" {
		w.Header().Set("Cache-Control", veladist.CacheControlRevalidate)
		target := url.URL{Path: basePath, RawQuery: r.URL.RawQuery, Fragment: r.URL.Fragment, RawFragment: r.URL.RawFragment}
		// Browsers retain an incoming fragment across a redirect whose Location
		// omits one; ordinary HTTP requests never transmit that fragment.
		http.Redirect(w, r, target.String(), http.StatusPermanentRedirect)
		return
	}
	if r.URL.Path != "/vela" && !strings.HasPrefix(r.URL.Path, "/vela/") {
		http.NotFound(w, r)
		return
	}
	if !VelaReady(dir) {
		http.Error(w, "Vela client is not installed", 503)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, "/vela/")
	if r.URL.Path == "/vela" {
		rel = ""
	}
	f, e := os.OpenRoot(dir)
	if e != nil {
		http.Error(w, "Vela client is unavailable", 503)
		return
	}
	defer f.Close()
	if rel == "" || rel == "index.html" {
		if info, err := f.Stat("index.html"); err == nil && info.Mode().IsRegular() {
			serveBrandedVelaDoc(w, r, f, dir, "index.html", info, brand)
			return
		}
		http.Error(w, "Vela client is unavailable", 503)
		return
	}
	s, e := f.Stat(rel)
	if rel == "sw.js" {
		// The worker's directory is /vela/, but the canonical home and worker
		// scope are /vela. Never serve the SPA as a missing worker script.
		if e != nil || !s.Mode().IsRegular() {
			w.Header().Set("Cache-Control", veladist.CacheControlRevalidate)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Service-Worker-Allowed", basePath)
		serveStaticVelaFile(w, r, f, rel, s)
		return
	}
	// Branded or release PWA icons: always served with no-cache and ETag.
	if fallbackCT, isIcon := velaPWAIconPaths[rel]; isIcon {
		if brand.IconVersion != "" && brand.IconAsset != nil {
			if asset, ok := brand.IconAsset(rel); ok {
				writeNoCacheDoc(w, r, velaShellDoc{
					contentType: asset.ContentType,
					etag:        asset.ETag,
					body:        asset.Data,
					isSVG:       asset.IsSVG,
				})
				return
			}
		}
		if e == nil && s.Mode().IsRegular() {
			serveReleaseIcon(w, r, f, dir, rel, s, fallbackCT)
			return
		}
		http.NotFound(w, r)
		return
	}
	// Native installer pages are ordinary nested index documents. FileServer
	// redirects their explicit index.html URL to a directory; a SPA fallback
	// there would serve the player instead. Serve existing nested indexes as
	// documents, without changing the canonical /vela home redirect.
	indexPath := ""
	if e == nil && s.IsDir() {
		indexPath = strings.TrimSuffix(rel, "/") + "/index.html"
	} else if e == nil && strings.HasSuffix(rel, "/index.html") {
		indexPath = rel
	}
	if indexPath != "" {
		if info, err := f.Stat(indexPath); err == nil && info.Mode().IsRegular() {
			serveStaticVelaFile(w, r, f, indexPath, info)
			return
		}
	}
	if rel == "manifest.webmanifest" || rel == "manifest.json" {
		if e == nil && s.Mode().IsRegular() {
			serveBrandedVelaDoc(w, r, f, dir, rel, s, brand)
			return
		}
		if rel == "manifest.webmanifest" {
			serveFallbackVelaManifest(w, r, brand)
			return
		}
	}
	if e == nil && s.Mode().IsRegular() {
		serveStaticVelaFile(w, r, f, rel, s)
		return
	}
	if info, err := f.Stat("index.html"); err == nil && info.Mode().IsRegular() {
		serveBrandedVelaDoc(w, r, f, dir, "index.html", info, brand)
		return
	}
	http.Error(w, "Vela client is unavailable", 503)
}

const maxVelaContentETagBytes = 2 << 20

func serveStaticVelaFile(w http.ResponseWriter, r *http.Request, root *os.Root, rel string, info os.FileInfo) {
	file, err := root.Open(rel)
	if err != nil {
		http.Error(w, "Vela client is unavailable", 503)
		return
	}
	defer file.Close()

	cc := veladist.StaticCacheControl(rel)
	w.Header().Set("Cache-Control", cc)
	if w.Header().Get("Content-Type") == "" {
		if ct := velaStaticContentType(rel); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
	}

	var content io.ReadSeeker = file
	if cc == veladist.CacheControlRevalidate && info.Size() <= maxVelaContentETagBytes {
		raw, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, "Vela client is unavailable", 503)
			return
		}
		sum := sha256.Sum256(raw)
		w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:16])+`"`)
		content = bytes.NewReader(raw)
	} else {
		w.Header().Set("ETag", fmt.Sprintf(`"%x-%x"`, info.Size(), info.ModTime().UnixNano()))
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), content)
}

func velaStaticContentType(rel string) string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".webmanifest":
		return "application/manifest+json; charset=utf-8"
	case ".js", ".mjs":
		// Windows maps .js to application/javascript in the registry.
		return "text/javascript; charset=utf-8"
	case ".wasm":
		return "application/wasm"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	}
	return ""
}

func serveReleaseIcon(w http.ResponseWriter, r *http.Request, root *os.Root, dir, file string, info os.FileInfo, contentType string) {
	key := velaShellKey{
		dir:     dir,
		file:    file,
		modTime: info.ModTime().UnixNano(),
		size:    info.Size(),
	}
	velaShellMu.Lock()
	doc, ok := velaShellCache[key]
	velaShellMu.Unlock()

	if !ok {
		fh, err := root.Open(file)
		if err != nil {
			http.Error(w, "Vela client is unavailable", 503)
			return
		}
		raw, err := io.ReadAll(fh)
		_ = fh.Close()
		if err != nil {
			http.Error(w, "Vela client is unavailable", 503)
			return
		}
		sum := sha256.Sum256(raw)
		doc = velaShellDoc{
			contentType: contentType,
			etag:        `"` + hex.EncodeToString(sum[:16]) + `"`,
			body:        raw,
			isSVG:       contentType == "image/svg+xml",
			modTime:     info.ModTime(),
		}
		velaShellMu.Lock()
		if len(velaShellCache) >= 64 {
			clear(velaShellCache)
		}
		velaShellCache[key] = doc
		velaShellMu.Unlock()
	}

	writeNoCacheDoc(w, r, doc)
}

func serveFallbackVelaManifest(w http.ResponseWriter, r *http.Request, brand VelaBrand) {
	basePath := velaBasePath(brand)
	base := []byte(`{"id":"/vela","name":"Vela","short_name":"Vela","start_url":"/vela","scope":"/vela","display":"standalone","theme_color":"#090B0E","background_color":"#090B0E"}`)
	patched := patchVelaManifest(base, strings.TrimSpace(brand.AppName), brand.IconVersion != "", basePath)
	sum := sha256.Sum256(append(append([]byte(nil), patched...), ("\x00" + brand.IconVersion)...))
	writeNoCacheDoc(w, r, velaShellDoc{
		contentType: "application/manifest+json; charset=utf-8",
		etag:        `"` + hex.EncodeToString(sum[:16]) + `"`,
		body:        patched,
	})
}

func serveBrandedVelaDoc(w http.ResponseWriter, r *http.Request, root *os.Root, dir, file string, info os.FileInfo, brand VelaBrand) {
	appName := strings.TrimSpace(brand.AppName)
	basePath := velaBasePath(brand)
	key := velaShellKey{
		dir:         dir,
		file:        file,
		modTime:     info.ModTime().UnixNano(),
		size:        info.Size(),
		name:        appName,
		iconVersion: brand.IconVersion,
		iconIsSVG:   brand.IconIsSVG,
		basePath:    basePath,
	}
	velaShellMu.Lock()
	doc, ok := velaShellCache[key]
	velaShellMu.Unlock()

	if !ok {
		fh, err := root.Open(file)
		if err != nil {
			http.Error(w, "Vela client is unavailable", 503)
			return
		}
		raw, err := io.ReadAll(fh)
		_ = fh.Close()
		if err != nil {
			http.Error(w, "Vela client is unavailable", 503)
			return
		}
		var patched []byte
		var ct string
		if file == "index.html" {
			ct = "text/html; charset=utf-8"
			patched = patchVelaHTML(raw, appName, brand.IconVersion != "", brand.IconIsSVG, basePath)
		} else {
			ct = "application/manifest+json; charset=utf-8"
			patched = patchVelaManifest(raw, appName, brand.IconVersion != "", basePath)
		}
		hashInput := make([]byte, 0, len(patched)+1+len(brand.IconVersion))
		hashInput = append(hashInput, patched...)
		hashInput = append(hashInput, 0)
		hashInput = append(hashInput, brand.IconVersion...)
		sum := sha256.Sum256(hashInput)
		doc = velaShellDoc{
			contentType: ct,
			etag:        `"` + hex.EncodeToString(sum[:16]) + `"`,
			body:        patched,
			modTime:     info.ModTime(),
		}
		velaShellMu.Lock()
		if len(velaShellCache) >= 64 {
			clear(velaShellCache)
		}
		velaShellCache[key] = doc
		velaShellMu.Unlock()
	}

	writeNoCacheDoc(w, r, doc)
}

func writeNoCacheDoc(w http.ResponseWriter, r *http.Request, doc velaShellDoc) {
	w.Header().Set("Content-Type", doc.contentType)
	w.Header().Set("Cache-Control", veladist.CacheControlRevalidate)
	w.Header().Set("ETag", doc.etag)
	if !doc.modTime.IsZero() {
		w.Header().Set("Last-Modified", doc.modTime.UTC().Format(http.TimeFormat))
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if doc.isSVG {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	}
	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == doc.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(doc.body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(doc.body)
	}
}

func patchVelaHTML(raw []byte, appName string, hasCustomIcon, iconIsSVG bool, basePath string) []byte {
	out := raw
	if appName != "" {
		out = patchVelaHTMLTitle(out, appName)
	}
	if hasCustomIcon && !iconIsSVG {
		out = patchVelaHTMLFavicon(out, basePath)
	}
	if basePath != "" && basePath != "/vela" {
		escaped := html.EscapeString(basePath)
		out = bytes.ReplaceAll(out, []byte(`href="/vela/`), []byte(`href="`+escaped+`/`))
		out = bytes.ReplaceAll(out, []byte(`src="/vela/`), []byte(`src="`+escaped+`/`))
	}
	return out
}

func patchVelaHTMLTitle(raw []byte, brand string) []byte {
	replacement := []byte("<title>" + html.EscapeString(brand) + "</title>")
	loc := htmlTitlePattern.FindIndex(raw)
	if loc == nil {
		return raw
	}
	out := make([]byte, 0, len(raw)-loc[1]+loc[0]+len(replacement))
	out = append(out, raw[:loc[0]]...)
	out = append(out, replacement...)
	out = append(out, raw[loc[1]:]...)
	return out
}

func patchVelaHTMLFavicon(raw []byte, basePath string) []byte {
	if basePath == "" {
		basePath = "/vela"
	}
	replacement := []byte(`<link rel="icon" type="image/png" sizes="32x32" href="` + html.EscapeString(basePath) + `/favicon-32.png" />`)
	loc := htmlIconPattern.FindIndex(raw)
	if loc == nil {
		return raw
	}
	out := make([]byte, 0, len(raw)-loc[1]+loc[0]+len(replacement))
	out = append(out, raw[:loc[0]]...)
	out = append(out, replacement...)
	out = append(out, raw[loc[1]:]...)
	return out
}

func brandedManifestIconsFor(basePath string) []map[string]string {
	if basePath == "" {
		basePath = "/vela"
	}
	return []map[string]string{
		{"src": basePath + "/vela-icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
		{"src": basePath + "/vela-icon-maskable-192.png", "sizes": "192x192", "type": "image/png", "purpose": "maskable"},
		{"src": basePath + "/vela-icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
		{"src": basePath + "/vela-icon-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
	}
}

func rewriteVelaPath(value, basePath string) string {
	if value == "/vela" || value == "/vela/" {
		return basePath
	}
	if strings.HasPrefix(value, "/vela/") {
		return basePath + strings.TrimPrefix(value, "/vela")
	}
	return value
}

func patchVelaManifest(raw []byte, appName string, hasCustomIcon bool, basePath string) []byte {
	if basePath == "" {
		basePath = "/vela"
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest == nil {
		return raw
	}
	if appName != "" {
		if encodedName, err := json.Marshal(appName); err == nil {
			manifest["name"] = encodedName
			manifest["short_name"] = encodedName
		}
	}
	if basePath != "/vela" {
		for _, field := range []string{"id", "start_url", "scope"} {
			if rawField, exists := manifest[field]; exists {
				var current string
				if err := json.Unmarshal(rawField, &current); err == nil {
					if rewritten := rewriteVelaPath(current, basePath); rewritten != current {
						if encoded, err := json.Marshal(rewritten); err == nil {
							manifest[field] = encoded
						}
					}
				}
			}
		}
		if !hasCustomIcon {
			if rawIcons, exists := manifest["icons"]; exists {
				var icons []map[string]any
				if err := json.Unmarshal(rawIcons, &icons); err == nil {
					changed := false
					for _, icon := range icons {
						if src, ok := icon["src"].(string); ok {
							if rewritten := rewriteVelaPath(src, basePath); rewritten != src {
								icon["src"] = rewritten
								changed = true
							}
						}
					}
					if changed {
						if encoded, err := json.Marshal(icons); err == nil {
							manifest["icons"] = encoded
						}
					}
				}
			}
		}
	}
	if hasCustomIcon {
		if encodedIcons, err := json.Marshal(brandedManifestIconsFor(basePath)); err == nil {
			manifest["icons"] = encodedIcons
		}
	}
	out, err := json.Marshal(manifest)
	if err != nil {
		return raw
	}
	return out
}
