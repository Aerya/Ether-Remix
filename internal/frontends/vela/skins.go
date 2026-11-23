package vela

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func init() {
	frontend.RegisterMigration("vela_skins", 1, `CREATE TABLE vela_skins (id TEXT PRIMARY KEY, version TEXT NOT NULL, kind TEXT NOT NULL CHECK (kind IN ('pack', 'document')), name TEXT NOT NULL, installed_at INTEGER NOT NULL);`)
}

const (
	MaxSkinArchiveBytes  = 8 << 20  // 8 MB .vskin archive
	MaxSkinExpandedBytes = 32 << 20 // 32 MB expanded contents
	MaxSkinArchiveFiles  = 200      // max files in .vskin
	MaxSkinPackBytes     = 64 << 10 // 64 KB SkinPack JSON
	MaxSkinDocumentBytes = 2 << 20  // 2 MB SkinDocument JSON

	svgSkinCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"
)

var (
	ErrBuiltinSkin  = errors.New("built-in skins cannot be modified or removed")
	ErrSkinNotFound = errors.New("skin not found")
	ErrSkinInvalid  = errors.New("invalid skin package")
)

var (
	skinVersionPattern  = regexp.MustCompile(`^[0-9][0-9A-Za-z._-]{0,63}$`)
	skinFileNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
)

// BuiltinSkin describes a non-removable skin shipped with Vela.
type BuiltinSkin struct {
	ID       string
	Name     string
	Version  string
	Swatches []string
}

var builtinSkinDocuments = []BuiltinSkin{
	{ID: "cinematic-doc", Name: "Cinematic", Version: "1.0.0", Swatches: []string{"#0b0f17", "#eef4fb", "#6ee7df"}},
	{ID: "mochi", Name: "Mochi", Version: "1.0.0", Swatches: []string{"#1b1524", "#f9eefb", "#f49ac2"}},
	{ID: "marquee", Name: "Marquee", Version: "1.0.0", Swatches: []string{"#130e0a", "#f8f0e5", "#f0b44c"}},
	{ID: "jellytwin", Name: "Jelly Twin", Version: "1.0.0", Swatches: []string{"#101022", "#eef0fa", "#a855f7"}},
	{ID: "index", Name: "Index", Version: "1.0.0", Swatches: []string{"#0e1216", "#e5e9ef", "#94a3b8"}},
	{ID: "lounge", Name: "Lounge", Version: "1.0.0", Swatches: []string{"#171311", "#f4ece6", "#e08e6d"}},
	{ID: "latenight", Name: "Late Night", Version: "1.0.0", Swatches: []string{"#07090f", "#dce6f7", "#60a5fa"}},
	{ID: "kids", Name: "Kids", Version: "1.0.0", Swatches: []string{"#132038", "#ffffff", "#fbbf24"}},
	{ID: "broadcast", Name: "Broadcast", Version: "1.0.0", Swatches: []string{"#0c141d", "#e8f0f8", "#38bdf8"}},
}

// BuiltinSkins returns the non-removable built-in skin documents in display order.
func BuiltinSkins() []BuiltinSkin {
	out := make([]BuiltinSkin, len(builtinSkinDocuments))
	for i, s := range builtinSkinDocuments {
		out[i] = BuiltinSkin{
			ID:       s.ID,
			Name:     s.Name,
			Version:  s.Version,
			Swatches: append([]string(nil), s.Swatches...),
		}
	}
	return out
}

// IsBuiltinSkinID reports whether id names the compiled cinematic engine alias
// or one of Vela's non-removable built-in skin documents.
func IsBuiltinSkinID(id string) bool {
	if id == "cinematic" {
		return true
	}
	for _, s := range builtinSkinDocuments {
		if s.ID == id {
			return true
		}
	}
	return false
}

// ValidSkinID reports whether id matches the Vela skin identifier grammar.
func ValidSkinID(id string) bool {
	return skinIDPattern.MatchString(id)
}

func validSkinVersion(v string) bool {
	if !skinVersionPattern.MatchString(v) {
		return false
	}
	if strings.Contains(v, "..") || strings.HasSuffix(v, ".") || strings.HasSuffix(v, "-") || strings.HasSuffix(strings.ToLower(v), ".json") {
		return false
	}
	return true
}

var allowedSkinAssetMIME = map[string]string{
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".webp":  "image/webp",
	".svg":   "image/svg+xml",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".json":  "application/json",
}

var archiveExtensions = map[string]bool{
	".zip":      true,
	".vskin":    true,
	".etherpkg": true,
	".tar":      true,
	".gz":       true,
	".tgz":      true,
	".bz2":      true,
	".xz":       true,
	".7z":       true,
	".rar":      true,
}

func isImageAssetExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp", ".svg":
		return true
	}
	return false
}

func isFontAssetExt(ext string) bool {
	switch ext {
	case ".ttf", ".otf", ".woff", ".woff2":
		return true
	}
	return false
}

func looksLikeNestedArchive(data []byte) bool {
	if len(data) >= 4 {
		if bytes.HasPrefix(data, []byte("PK\x03\x04")) || bytes.HasPrefix(data, []byte("PK\x05\x06")) || bytes.HasPrefix(data, []byte("PK\x07\x08")) {
			return true
		}
	}
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		return true
	}
	if len(data) >= 6 && (bytes.HasPrefix(data, []byte("Rar!\x1a\x07")) || bytes.HasPrefix(data, []byte("7z\xbc\xaf\x27\x1c")) || bytes.HasPrefix(data, []byte("\xfd7zXZ\x00"))) {
		return true
	}
	if len(data) >= 262 && string(data[257:262]) == "ustar" {
		return true
	}
	return false
}

// SkinDocumentEntry is an entry in GET /vela/v0/appearance documents[].
type SkinDocumentEntry struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	URL      string   `json:"url"`
	Preview  string   `json:"preview,omitempty"`
	Swatches []string `json:"swatches"`
}

// SkinSummary describes a built-in or owner-installed skin for the console.
type SkinSummary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Kind        string   `json:"kind"` // "pack" or "document"
	Builtin     bool     `json:"builtin"`
	Preview     string   `json:"preview,omitempty"`
	Swatches    []string `json:"swatches"`
	InstalledAt int64    `json:"installedAt,omitempty"`
}

type installedSkinRecord struct {
	summary  SkinSummary
	packJSON json.RawMessage
	docEntry *SkinDocumentEntry
}

type skinSnapshot struct {
	generation uint64
	packs      []json.RawMessage
	documents  []SkinDocumentEntry
	kinds      map[string]string
	installed  map[string]installedSkinRecord
	ordered    []SkinSummary
}

// SkinStore manages owner-installed Vela skins on disk and in the database.
type SkinStore struct {
	db  *sql.DB
	dir string
	now func() time.Time

	mu     sync.Mutex
	snapMu sync.RWMutex
	loaded bool
	gen    uint64
	snap   skinSnapshot
}

// NewSkinStore creates a store backed by db and dir (typically <dataDir>/vela-skins).
func NewSkinStore(db *sql.DB, dir string) *SkinStore {
	return &SkinStore{
		db:  db,
		dir: dir,
		now: time.Now,
		snap: skinSnapshot{
			packs:     []json.RawMessage{},
			documents: []SkinDocumentEntry{},
			kinds:     map[string]string{},
			installed: map[string]installedSkinRecord{},
		},
	}
}

func skinValidationError(msg string) error {
	return fmt.Errorf("%w: %s", ErrSkinInvalid, msg)
}

type unpackedSkin struct {
	id       string
	version  string
	kind     string
	name     string
	preview  string
	swatches []string
	skinJSON []byte
	assets   map[string][]byte
}

func validateCleanZipPath(raw string) ([]string, error) {
	if raw == "" {
		return nil, skinValidationError("archive contains an empty file path")
	}
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "\\") || strings.ContainsRune(raw, '\\') || strings.ContainsRune(raw, 0) {
		return nil, skinValidationError("archive paths must be relative POSIX paths")
	}
	if len(raw) >= 2 && ((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) && raw[1] == ':' {
		return nil, skinValidationError("archive paths must not be absolute")
	}
	trimmed := strings.TrimSuffix(raw, "/")
	if trimmed == "" {
		return nil, skinValidationError("archive contains a root directory entry")
	}
	parts := strings.Split(trimmed, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return nil, skinValidationError("path traversal is not allowed in .vskin")
		}
		if !skinFileNamePattern.MatchString(p) || strings.Contains(p, "..") {
			return nil, skinValidationError(fmt.Sprintf("invalid path segment %q in .vskin", p))
		}
	}
	return parts, nil
}

func unpackAndValidateVSkin(pkg []byte) (*unpackedSkin, error) {
	if len(pkg) == 0 {
		return nil, skinValidationError("empty .vskin archive")
	}
	if len(pkg) > MaxSkinArchiveBytes {
		return nil, skinValidationError("skin archive exceeds the 8 MB limit")
	}
	zr, err := zip.NewReader(bytes.NewReader(pkg), int64(len(pkg)))
	if err != nil {
		return nil, skinValidationError("file is not a valid .vskin zip archive")
	}
	if len(zr.File) > MaxSkinArchiveFiles+32 {
		return nil, skinValidationError("skin archive exceeds the 200-file limit")
	}

	var (
		fileCount     int
		totalExpanded int64
		rawSkinJSON   []byte
		hasSkinJSON   bool
		seenRawPaths  = map[string]bool{}
		seenAssetBase = map[string]string{}
		assets        = map[string][]byte{}
		assetLookup   = map[string]string{}
	)

	for _, f := range zr.File {
		mode := f.Mode()
		if mode&os.ModeSymlink != 0 {
			return nil, skinValidationError("symlinks are not allowed in .vskin")
		}
		parts, err := validateCleanZipPath(f.Name)
		if err != nil {
			return nil, err
		}
		cleanRel := strings.Join(parts, "/")
		lowerRel := strings.ToLower(cleanRel)
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			if parts[0] != "assets" {
				return nil, skinValidationError(fmt.Sprintf("unexpected directory %q in .vskin (only assets/ is allowed)", cleanRel))
			}
			continue
		}
		if !mode.IsRegular() {
			return nil, skinValidationError(fmt.Sprintf("irregular file %q is not allowed in .vskin", cleanRel))
		}
		if seenRawPaths[lowerRel] {
			return nil, skinValidationError(fmt.Sprintf("duplicate file %q in .vskin", cleanRel))
		}
		seenRawPaths[lowerRel] = true

		fileCount++
		if fileCount > MaxSkinArchiveFiles {
			return nil, skinValidationError("skin archive exceeds the 200-file limit")
		}
		if f.UncompressedSize64 > MaxSkinExpandedBytes || totalExpanded+int64(f.UncompressedSize64) > MaxSkinExpandedBytes {
			return nil, skinValidationError("expanded skin archive exceeds the 32 MB limit")
		}

		rc, err := f.Open()
		if err != nil {
			return nil, skinValidationError("failed to read .vskin entry")
		}
		limit := MaxSkinExpandedBytes - totalExpanded + 1
		data, readErr := io.ReadAll(io.LimitReader(rc, limit))
		_ = rc.Close()
		if readErr != nil {
			return nil, skinValidationError("failed to decompress .vskin entry")
		}
		totalExpanded += int64(len(data))
		if totalExpanded > MaxSkinExpandedBytes {
			return nil, skinValidationError("expanded skin archive exceeds the 32 MB limit")
		}

		base := parts[len(parts)-1]
		ext := strings.ToLower(path.Ext(base))
		if archiveExtensions[ext] || looksLikeNestedArchive(data) {
			return nil, skinValidationError(fmt.Sprintf("nested archives are not allowed in .vskin (%s)", cleanRel))
		}

		if len(parts) == 1 && base == "skin.json" {
			rawSkinJSON = data
			hasSkinJSON = true
			continue
		}

		if len(parts) > 1 && parts[0] != "assets" {
			return nil, skinValidationError(fmt.Sprintf("asset %q must be at the archive root or under assets/", cleanRel))
		}
		if strings.EqualFold(base, "skin.json") {
			return nil, skinValidationError("skin.json is only allowed at the archive root")
		}
		if _, ok := allowedSkinAssetMIME[ext]; !ok {
			return nil, skinValidationError(fmt.Sprintf("unsupported asset extension %q on %q", ext, cleanRel))
		}
		if ext == ".json" && !json.Valid(data) {
			return nil, skinValidationError(fmt.Sprintf("asset %q is not valid JSON", cleanRel))
		}

		lowerBase := strings.ToLower(base)
		if prev, dup := seenAssetBase[lowerBase]; dup {
			return nil, skinValidationError(fmt.Sprintf("duplicate asset name %q (conflicts with %q)", base, prev))
		}
		seenAssetBase[lowerBase] = base
		assets[base] = data

		for _, alias := range []string{
			cleanRel,
			"./" + cleanRel,
			"/" + cleanRel,
			base,
			"./" + base,
			"assets/" + base,
			"./assets/" + base,
			"/assets/" + base,
		} {
			assetLookup[alias] = base
		}
	}

	if !hasSkinJSON {
		return nil, skinValidationError("skin.json is missing at the archive root")
	}
	return validateAndRewriteSkinJSON(rawSkinJSON, assets, assetLookup)
}

func decodeSingleJSONObject(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil || root == nil {
		return nil, skinValidationError("skin.json must be a valid JSON object")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, skinValidationError("skin.json must contain a single JSON object")
	}
	return root, nil
}

func jsonInt(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := strconv.ParseInt(n.String(), 10, 64)
		return i, err == nil
	case float64:
		if float64(int64(n)) == n {
			return int64(n), true
		}
	}
	return 0, false
}

func hasForbiddenScheme(s string) bool {
	trimmed := strings.TrimSpace(s)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "//") {
		return true
	}
	for _, prefix := range []string{"http:", "https:", "data:", "javascript:", "vbscript:", "file:", "blob:"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func validColorString(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 64 || hasForbiddenScheme(s) {
		return false
	}
	if strings.ContainsAny(s, "<>\"'\\;\n\r\t") || strings.Contains(strings.ToLower(s), "url(") {
		return false
	}
	return true
}

func parseSwatches(v any, required bool) ([]string, error) {
	if v == nil {
		if required {
			return nil, skinValidationError("swatches is required")
		}
		return []string{}, nil
	}
	rawList, ok := v.([]any)
	if !ok {
		return nil, skinValidationError("swatches must be an array of color strings")
	}
	if required && len(rawList) == 0 {
		return nil, skinValidationError("swatches must not be empty")
	}
	if len(rawList) > 16 {
		return nil, skinValidationError("swatches may contain at most 16 colors")
	}
	out := make([]string, 0, len(rawList))
	for _, item := range rawList {
		c, ok := item.(string)
		if !ok || !validColorString(c) {
			return nil, skinValidationError("each swatch must be a valid color string")
		}
		out = append(out, strings.TrimSpace(c))
	}
	return out, nil
}

type skinPackOrdered struct {
	Schema   int            `json:"schema"`
	ID       string         `json:"id"`
	Version  string         `json:"version"`
	Name     string         `json:"name"`
	Engine   string         `json:"engine"`
	Tagline  string         `json:"tagline,omitempty"`
	Palette  map[string]any `json:"palette"`
	Tokens   map[string]any `json:"tokens,omitempty"`
	Fonts    map[string]any `json:"fonts,omitempty"`
	Radius   any            `json:"radius,omitempty"`
	Images   map[string]any `json:"images,omitempty"`
	Swatches []string       `json:"swatches"`
}

func resolveAssetRef(ref, urlPrefix string, assetLookup map[string]string) (canonicalBase string, rewritten string, ok bool) {
	trimmed := strings.TrimSpace(ref)
	if base, found := assetLookup[trimmed]; found {
		return base, urlPrefix + base, true
	}
	if strings.HasPrefix(trimmed, urlPrefix) {
		tail := strings.TrimPrefix(trimmed, urlPrefix)
		if base, found := assetLookup[tail]; found && base == tail {
			return base, urlPrefix + base, true
		}
	}
	return "", "", false
}

func looksLikeAssetPath(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || strings.ContainsAny(trimmed, " \t\n\r") {
		return false
	}
	if strings.HasPrefix(trimmed, "assets/") || strings.HasPrefix(trimmed, "./assets/") || strings.HasPrefix(trimmed, "/assets/") || strings.HasPrefix(trimmed, "/vela/v0/appearance/") {
		return true
	}
	ext := strings.ToLower(path.Ext(trimmed))
	if _, ok := allowedSkinAssetMIME[ext]; ok {
		return true
	}
	if archiveExtensions[ext] {
		return true
	}
	return false
}

func rewriteGenericValue(v any, urlPrefix string, assetLookup map[string]string, requireAsset bool) (any, error) {
	switch val := v.(type) {
	case string:
		if hasForbiddenScheme(val) {
			return nil, skinValidationError(fmt.Sprintf("external or unsafe URL %q is not allowed in skin.json", val))
		}
		if strings.Contains(val, "..") && (strings.Contains(val, "/") || strings.Contains(val, "\\")) {
			return nil, skinValidationError(fmt.Sprintf("path traversal %q is not allowed in skin.json", val))
		}
		if _, rewritten, ok := resolveAssetRef(val, urlPrefix, assetLookup); ok {
			return rewritten, nil
		}
		if requireAsset || looksLikeAssetPath(val) {
			return nil, skinValidationError(fmt.Sprintf("referenced asset %q is missing from .vskin", val))
		}
		return val, nil
	case []any:
		out := make([]any, len(val))
		for idx, item := range val {
			next, err := rewriteGenericValue(item, urlPrefix, assetLookup, requireAsset)
			if err != nil {
				return nil, err
			}
			out[idx] = next
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			next, err := rewriteGenericValue(item, urlPrefix, assetLookup, requireAsset)
			if err != nil {
				return nil, err
			}
			out[k] = next
		}
		return out, nil
	default:
		return v, nil
	}
}

func rewriteImagesObject(raw any, urlPrefix string, assetLookup map[string]string) (map[string]any, string, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, "", skinValidationError("images must be a JSON object")
	}
	out := make(map[string]any, len(obj))
	var preview string
	for slot, v := range obj {
		ref, ok := v.(string)
		if !ok || strings.TrimSpace(ref) == "" {
			return nil, "", skinValidationError(fmt.Sprintf("images.%s must be a non-empty asset path", slot))
		}
		if hasForbiddenScheme(ref) {
			return nil, "", skinValidationError(fmt.Sprintf("images.%s cannot use an external URL", slot))
		}
		base, rewritten, found := resolveAssetRef(ref, urlPrefix, assetLookup)
		if !found {
			return nil, "", skinValidationError(fmt.Sprintf("images.%s references missing asset %q", slot, ref))
		}
		if !isImageAssetExt(strings.ToLower(path.Ext(base))) {
			return nil, "", skinValidationError(fmt.Sprintf("images.%s (%s) must be an image file", slot, base))
		}
		out[slot] = rewritten
		if slot == "preview" {
			preview = rewritten
		}
	}
	return out, preview, nil
}

func validateAndRewriteSkinJSON(rawSkinJSON []byte, assets map[string][]byte, assetLookup map[string]string) (*unpackedSkin, error) {
	if len(rawSkinJSON) > MaxSkinDocumentBytes {
		return nil, skinValidationError("skin.json exceeds maximum size")
	}
	root, err := decodeSingleJSONObject(rawSkinJSON)
	if err != nil {
		return nil, err
	}

	schema, ok := jsonInt(root["schema"])
	if !ok || schema != 1 {
		return nil, skinValidationError("skin.json schema must be 1")
	}
	id, _ := root["id"].(string)
	if !ValidSkinID(id) {
		return nil, skinValidationError("skin.json id must match [a-z][a-z0-9-]{0,31}")
	}
	if IsBuiltinSkinID(id) {
		return nil, fmt.Errorf("%w: id %q is reserved for a built-in skin", ErrBuiltinSkin, id)
	}
	version, _ := root["version"].(string)
	if !validSkinVersion(version) {
		return nil, skinValidationError("skin.json version must be a valid version string")
	}
	name, _ := root["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return nil, skinValidationError("skin.json name must be 1..80 characters")
	}

	_, hasPalette := root["palette"]
	_, hasUI := root["ui"]
	_, hasRoutes := root["routes"]
	_, hasRenderer := root["renderer"]

	isDocument := hasUI || hasRoutes || hasRenderer
	isPack := hasPalette && !isDocument
	if isPack && isDocument {
		return nil, skinValidationError("skin.json cannot mix SkinPack and SkinDocument fields")
	}
	if !isPack && !isDocument {
		return nil, skinValidationError("skin.json must be a SkinPack (palette) or a SkinDocument (ui/routes)")
	}

	if isPack {
		if len(rawSkinJSON) > MaxSkinPackBytes {
			return nil, skinValidationError("SkinPack skin.json exceeds the 64 KB limit")
		}
		allowedKeys := map[string]bool{
			"schema": true, "id": true, "version": true, "name": true, "engine": true,
			"palette": true, "swatches": true, "tagline": true, "tokens": true,
			"fonts": true, "radius": true, "images": true,
		}
		for k := range root {
			if !allowedKeys[k] {
				return nil, skinValidationError(fmt.Sprintf("unknown SkinPack field %q", k))
			}
		}
		engine, _ := root["engine"].(string)
		if engine != "cinematic" {
			return nil, skinValidationError("SkinPack engine must be \"cinematic\"")
		}
		paletteObj, ok := root["palette"].(map[string]any)
		if !ok || len(paletteObj) == 0 {
			return nil, skinValidationError("SkinPack palette must be a non-empty object")
		}
		cleanPalette := make(map[string]any, len(paletteObj))
		for k, v := range paletteObj {
			c, ok := v.(string)
			if !ok || !validColorString(c) {
				return nil, skinValidationError(fmt.Sprintf("palette.%s must be a valid color string", k))
			}
			cleanPalette[k] = strings.TrimSpace(c)
		}
		swatches, err := parseSwatches(root["swatches"], true)
		if err != nil {
			return nil, err
		}

		urlPrefix := "/vela/v0/appearance/packs/" + id + "/" + version + "/"
		out := skinPackOrdered{
			Schema:   1,
			ID:       id,
			Version:  version,
			Name:     name,
			Engine:   "cinematic",
			Palette:  cleanPalette,
			Swatches: swatches,
		}
		if rawTagline, exists := root["tagline"]; exists && rawTagline != nil {
			tl, ok := rawTagline.(string)
			if !ok || utf8.RuneCountInString(tl) > 200 {
				return nil, skinValidationError("tagline must be a string up to 200 characters")
			}
			out.Tagline = strings.TrimSpace(tl)
		}
		if rawTokens, exists := root["tokens"]; exists && rawTokens != nil {
			tokObj, ok := rawTokens.(map[string]any)
			if !ok {
				return nil, skinValidationError("tokens must be a JSON object")
			}
			rewrittenTokens, err := rewriteGenericValue(tokObj, urlPrefix, assetLookup, false)
			if err != nil {
				return nil, err
			}
			out.Tokens = rewrittenTokens.(map[string]any)
		}
		if rawRadius, exists := root["radius"]; exists && rawRadius != nil {
			rewrittenRadius, err := rewriteGenericValue(rawRadius, urlPrefix, assetLookup, false)
			if err != nil {
				return nil, err
			}
			out.Radius = rewrittenRadius
		}
		var preview string
		if rawImages, exists := root["images"]; exists && rawImages != nil {
			imgs, prev, err := rewriteImagesObject(rawImages, urlPrefix, assetLookup)
			if err != nil {
				return nil, err
			}
			out.Images = imgs
			preview = prev
		}
		if rawFonts, exists := root["fonts"]; exists && rawFonts != nil {
			fonts, err := checkAndRewriteFonts(rawFonts, urlPrefix, assetLookup)
			if err != nil {
				return nil, err
			}
			out.Fonts = fonts
		}
		if preview == "" {
			preview = autoPreviewURL(urlPrefix, assetLookup)
		}
		rewrittenBytes, err := json.Marshal(out)
		if err != nil {
			return nil, err
		}
		if len(rewrittenBytes) > MaxSkinPackBytes {
			return nil, skinValidationError("rewritten SkinPack exceeds the 64 KB limit")
		}
		return &unpackedSkin{
			id:       id,
			version:  version,
			kind:     "pack",
			name:     name,
			preview:  preview,
			swatches: swatches,
			skinJSON: rewrittenBytes,
			assets:   assets,
		}, nil
	}

	// SkinDocument validation and asset rewriting.
	renderer, ok := jsonInt(root["renderer"])
	if !ok || renderer < 1 {
		return nil, skinValidationError("SkinDocument renderer must be a positive integer")
	}
	if (!hasUI || root["ui"] == nil) && (!hasRoutes || root["routes"] == nil) {
		return nil, skinValidationError("SkinDocument must include ui or routes")
	}

	urlPrefix := "/vela/v0/appearance/skins/" + id + "/" + version + "/"
	rewrittenDoc := make(map[string]any, len(root))
	var preview string
	for k, v := range root {
		switch k {
		case "schema":
			rewrittenDoc[k] = 1
		case "renderer":
			rewrittenDoc[k] = renderer
		case "id":
			rewrittenDoc[k] = id
		case "version":
			rewrittenDoc[k] = version
		case "name":
			rewrittenDoc[k] = name
		case "swatches":
			sw, err := parseSwatches(v, false)
			if err != nil {
				return nil, err
			}
			rewrittenDoc[k] = sw
		case "preview":
			if v != nil {
				ref, ok := v.(string)
				if !ok || strings.TrimSpace(ref) == "" {
					return nil, skinValidationError("preview must be a non-empty asset path")
				}
				if hasForbiddenScheme(ref) {
					return nil, skinValidationError("preview cannot use an external URL")
				}
				base, rew, found := resolveAssetRef(ref, urlPrefix, assetLookup)
				if !found || !isImageAssetExt(strings.ToLower(path.Ext(base))) {
					return nil, skinValidationError(fmt.Sprintf("preview references missing or invalid image %q", ref))
				}
				rewrittenDoc[k] = rew
				preview = rew
			}
		case "images":
			if v != nil {
				imgs, prev, err := rewriteImagesObject(v, urlPrefix, assetLookup)
				if err != nil {
					return nil, err
				}
				rewrittenDoc[k] = imgs
				if preview == "" {
					preview = prev
				}
			}
		case "fonts":
			if v != nil {
				fonts, err := checkAndRewriteFonts(v, urlPrefix, assetLookup)
				if err != nil {
					return nil, err
				}
				rewrittenDoc[k] = fonts
			}
		case "assets":
			if v != nil {
				rewAssets, prev, err := rewriteDocumentAssets(v, urlPrefix, assetLookup)
				if err != nil {
					return nil, err
				}
				rewrittenDoc[k] = rewAssets
				if preview == "" {
					preview = prev
				}
			}
		default:
			rew, err := rewriteGenericValue(v, urlPrefix, assetLookup, false)
			if err != nil {
				return nil, err
			}
			rewrittenDoc[k] = rew
		}
	}

	swatches, err := extractDocumentSwatches(rewrittenDoc)
	if err != nil {
		return nil, err
	}
	if preview == "" {
		preview = autoPreviewURL(urlPrefix, assetLookup)
	}
	rewrittenBytes, err := json.Marshal(rewrittenDoc)
	if err != nil {
		return nil, err
	}
	if len(rewrittenBytes) > MaxSkinDocumentBytes {
		return nil, skinValidationError("rewritten SkinDocument exceeds the 2 MB limit")
	}
	return &unpackedSkin{
		id:       id,
		version:  version,
		kind:     "document",
		name:     name,
		preview:  preview,
		swatches: swatches,
		skinJSON: rewrittenBytes,
		assets:   assets,
	}, nil
}

func checkAndRewriteFonts(raw any, urlPrefix string, assetLookup map[string]string) (map[string]any, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, skinValidationError("fonts must be a JSON object")
	}
	out := make(map[string]any, len(obj))
	for role, entry := range obj {
		fontDef, ok := entry.(map[string]any)
		if !ok {
			return nil, skinValidationError(fmt.Sprintf("fonts.%s must be an object", role))
		}
		family, ok := fontDef["family"].(string)
		if !ok || strings.TrimSpace(family) == "" {
			return nil, skinValidationError(fmt.Sprintf("fonts.%s.family is required", role))
		}
		rawFiles, ok := fontDef["files"].([]any)
		if !ok || len(rawFiles) == 0 {
			return nil, skinValidationError(fmt.Sprintf("fonts.%s.files must be a non-empty array", role))
		}
		files := make([]any, 0, len(rawFiles))
		for idx, rf := range rawFiles {
			fObj, ok := rf.(map[string]any)
			if !ok {
				return nil, skinValidationError(fmt.Sprintf("fonts.%s.files[%d] must be an object", role, idx))
			}
			urlStr, ok := fObj["url"].(string)
			if !ok || strings.TrimSpace(urlStr) == "" {
				return nil, skinValidationError(fmt.Sprintf("fonts.%s.files[%d].url is required", role, idx))
			}
			if hasForbiddenScheme(urlStr) {
				return nil, skinValidationError(fmt.Sprintf("fonts.%s.files[%d].url cannot use an external URL", role, idx))
			}
			base, rewritten, found := resolveAssetRef(urlStr, urlPrefix, assetLookup)
			if !found {
				return nil, skinValidationError(fmt.Sprintf("fonts.%s.files[%d].url references missing asset %q", role, idx, urlStr))
			}
			if !isFontAssetExt(strings.ToLower(path.Ext(base))) {
				return nil, skinValidationError(fmt.Sprintf("fonts.%s.files[%d].url (%s) must be a font file", role, idx, base))
			}
			copied := make(map[string]any, len(fObj))
			for k, v := range fObj {
				copied[k] = v
			}
			copied["url"] = rewritten
			files = append(files, copied)
		}
		roleCopy := make(map[string]any, len(fontDef))
		for k, v := range fontDef {
			roleCopy[k] = v
		}
		roleCopy["files"] = files
		out[role] = roleCopy
	}
	return out, nil
}

func rewriteDocumentAssets(raw any, urlPrefix string, assetLookup map[string]string) (any, string, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		rew, err := rewriteGenericValue(raw, urlPrefix, assetLookup, true)
		return rew, "", err
	}
	out := make(map[string]any, len(obj))
	var preview string
	for key, val := range obj {
		switch item := val.(type) {
		case string:
			if hasForbiddenScheme(item) {
				return nil, "", skinValidationError(fmt.Sprintf("assets.%s cannot use an external URL", key))
			}
			base, rew, found := resolveAssetRef(item, urlPrefix, assetLookup)
			if !found {
				return nil, "", skinValidationError(fmt.Sprintf("assets.%s references missing asset %q", key, item))
			}
			out[key] = rew
			if key == "preview" && isImageAssetExt(strings.ToLower(path.Ext(base))) {
				preview = rew
			}
		case map[string]any:
			copied := make(map[string]any, len(item))
			for subK, subV := range item {
				requireRef := subK == "url" || subK == "src" || subK == "file" || subK == "path"
				rew, err := rewriteGenericValue(subV, urlPrefix, assetLookup, requireRef)
				if err != nil {
					return nil, "", err
				}
				copied[subK] = rew
				if key == "preview" && (subK == "url" || subK == "src") {
					if s, ok := rew.(string); ok {
						preview = s
					}
				}
			}
			out[key] = copied
		default:
			rew, err := rewriteGenericValue(val, urlPrefix, assetLookup, false)
			if err != nil {
				return nil, "", err
			}
			out[key] = rew
		}
	}
	return out, preview, nil
}

func extractDocumentSwatches(doc map[string]any) ([]string, error) {
	if raw, ok := doc["swatches"]; ok {
		if sw, ok := raw.([]string); ok {
			return sw, nil
		}
		return parseSwatches(raw, false)
	}
	if ui, ok := doc["ui"].(map[string]any); ok {
		if raw, ok := ui["swatches"]; ok {
			return parseSwatches(raw, false)
		}
	}
	return []string{}, nil
}

func autoPreviewURL(urlPrefix string, assetLookup map[string]string) string {
	for _, candidate := range []string{"preview.svg", "preview.png", "preview.webp", "preview.jpg", "preview.jpeg"} {
		if base, ok := assetLookup[candidate]; ok {
			return urlPrefix + base
		}
	}
	return ""
}

// InstallArchive validates, rewrites, atomically unpacks, and persists a .vskin archive.
func (s *SkinStore) InstallArchive(ctx context.Context, pkg []byte) (SkinSummary, error) {
	if s == nil || s.db == nil || s.dir == "" {
		return SkinSummary{}, errors.New("skin storage unavailable")
	}
	unpacked, err := unpackAndValidateVSkin(pkg)
	if err != nil {
		return SkinSummary{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	skinIDDir := filepath.Join(s.dir, unpacked.id)
	if err := os.MkdirAll(skinIDDir, 0o755); err != nil {
		return SkinSummary{}, err
	}

	stageDir, err := os.MkdirTemp(skinIDDir, ".stage-"+unpacked.version+"-")
	if err != nil {
		return SkinSummary{}, err
	}
	stageCleanup := true
	defer func() {
		if stageCleanup {
			_ = os.RemoveAll(stageDir)
		}
	}()

	if err := writeSkinStage(stageDir, unpacked); err != nil {
		return SkinSummary{}, err
	}
	if err := os.Chmod(stageDir, 0o755); err != nil {
		return SkinSummary{}, err
	}

	targetDir := filepath.Join(skinIDDir, unpacked.version)
	var backupDir string
	if _, statErr := os.Stat(targetDir); statErr == nil {
		backupDir = filepath.Join(skinIDDir, fmt.Sprintf(".backup-%d", s.now().UnixNano()))
		if err := os.Rename(targetDir, backupDir); err != nil {
			return SkinSummary{}, err
		}
	}
	if err := os.Rename(stageDir, targetDir); err != nil {
		if backupDir != "" {
			_ = os.Rename(backupDir, targetDir)
		}
		return SkinSummary{}, err
	}
	stageCleanup = false

	installedAt := s.now().UTC().Unix()
	_, dbErr := s.db.ExecContext(ctx, `INSERT INTO vela_skins (id, version, kind, name, installed_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET version=excluded.version, kind=excluded.kind, name=excluded.name, installed_at=excluded.installed_at`,
		unpacked.id, unpacked.version, unpacked.kind, unpacked.name, installedAt)
	if dbErr != nil {
		_ = os.RemoveAll(targetDir)
		if backupDir != "" {
			_ = os.Rename(backupDir, targetDir)
		}
		return SkinSummary{}, dbErr
	}

	// Atomically retire any prior version directories (and same-version backup).
	if entries, err := os.ReadDir(skinIDDir); err == nil {
		for _, entry := range entries {
			if entry.Name() != unpacked.version {
				_ = os.RemoveAll(filepath.Join(skinIDDir, entry.Name()))
			}
		}
	}

	if err := s.reloadLocked(ctx); err != nil {
		return SkinSummary{}, err
	}
	if rec, ok := s.snap.installed[unpacked.id]; ok {
		return rec.summary, nil
	}
	return SkinSummary{
		ID:          unpacked.id,
		Name:        unpacked.name,
		Version:     unpacked.version,
		Kind:        unpacked.kind,
		Builtin:     false,
		Preview:     unpacked.preview,
		Swatches:    unpacked.swatches,
		InstalledAt: installedAt,
	}, nil
}

func writeSkinStage(stageDir string, unpacked *unpackedSkin) error {
	root, err := os.OpenRoot(stageDir)
	if err != nil {
		return err
	}
	defer root.Close()

	if err := writeFileInRoot(root, "skin.json", unpacked.skinJSON); err != nil {
		return err
	}
	names := make([]string, 0, len(unpacked.assets))
	for name := range unpacked.assets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := writeFileInRoot(root, name, unpacked.assets[name]); err != nil {
			return err
		}
	}
	return nil
}

func writeFileInRoot(root *os.Root, name string, data []byte) error {
	f, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// Remove deletes an owner-installed skin from the database and disk.
func (s *SkinStore) Remove(ctx context.Context, id string) error {
	if IsBuiltinSkinID(id) {
		return ErrBuiltinSkin
	}
	if !ValidSkinID(id) {
		return ErrSkinNotFound
	}
	if s == nil || s.db == nil || s.dir == "" {
		return ErrSkinNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.ExecContext(ctx, `DELETE FROM vela_skins WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSkinNotFound
	}
	_ = os.RemoveAll(filepath.Join(s.dir, id))
	return s.reloadLocked(ctx)
}

func (s *SkinStore) ensureLoaded(ctx context.Context) skinSnapshot {
	s.snapMu.RLock()
	if s.loaded {
		snap := s.snap
		s.snapMu.RUnlock()
		return snap
	}
	s.snapMu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapMu.RLock()
	if s.loaded {
		snap := s.snap
		s.snapMu.RUnlock()
		return snap
	}
	s.snapMu.RUnlock()

	_ = s.reloadLocked(ctx)
	s.snapMu.RLock()
	snap := s.snap
	s.snapMu.RUnlock()
	return snap
}

func (s *SkinStore) reloadLocked(ctx context.Context) error {
	if s.db == nil || s.dir == "" {
		s.snapMu.Lock()
		s.gen++
		s.snap = skinSnapshot{
			generation: s.gen,
			packs:      []json.RawMessage{},
			documents:  []SkinDocumentEntry{},
			kinds:      map[string]string{},
			installed:  map[string]installedSkinRecord{},
		}
		s.loaded = true
		s.snapMu.Unlock()
		return nil
	}

	qctx := frontend.DetachQueryStats(ctx)
	rows, err := s.db.QueryContext(qctx, `SELECT id, version, kind, name, installed_at FROM vela_skins ORDER BY LOWER(name), id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type dbRow struct {
		id, version, kind, name string
		installedAt             int64
	}
	var records []dbRow
	for rows.Next() {
		var r dbRow
		if err := rows.Scan(&r.id, &r.version, &r.kind, &r.name, &r.installedAt); err != nil {
			return err
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	packs := make([]json.RawMessage, 0)
	documents := make([]SkinDocumentEntry, 0)
	kinds := make(map[string]string, len(records))
	installed := make(map[string]installedSkinRecord, len(records))
	ordered := make([]SkinSummary, 0, len(records))

	for _, r := range records {
		if !ValidSkinID(r.id) || !validSkinVersion(r.version) {
			continue
		}
		skinFile := filepath.Join(s.dir, r.id, r.version, "skin.json")
		raw, err := os.ReadFile(skinFile)
		if err != nil || !json.Valid(raw) {
			continue
		}
		preview, swatches := extractStoredPreviewAndSwatches(raw, r.id, r.version, r.kind, filepath.Join(s.dir, r.id, r.version))
		summary := SkinSummary{
			ID:          r.id,
			Name:        r.name,
			Version:     r.version,
			Kind:        r.kind,
			Builtin:     false,
			Preview:     preview,
			Swatches:    swatches,
			InstalledAt: r.installedAt,
		}
		rec := installedSkinRecord{summary: summary}
		switch r.kind {
		case "pack":
			msg := append(json.RawMessage(nil), raw...)
			packs = append(packs, msg)
			rec.packJSON = msg
		case "document":
			docEntry := SkinDocumentEntry{
				ID:       r.id,
				Name:     r.name,
				Version:  r.version,
				URL:      "/vela/v0/appearance/skins/" + r.id + "/" + r.version + ".json",
				Preview:  preview,
				Swatches: swatches,
			}
			documents = append(documents, docEntry)
			rec.docEntry = &docEntry
		default:
			continue
		}
		kinds[r.id] = r.kind
		installed[r.id] = rec
		ordered = append(ordered, summary)
	}

	s.snapMu.Lock()
	s.gen++
	s.snap = skinSnapshot{
		generation: s.gen,
		packs:      packs,
		documents:  documents,
		kinds:      kinds,
		installed:  installed,
		ordered:    ordered,
	}
	s.loaded = true
	s.snapMu.Unlock()
	return nil
}

func extractStoredPreviewAndSwatches(raw []byte, id, version, kind, versionDir string) (string, []string) {
	var parsed struct {
		Preview  string            `json:"preview"`
		Swatches []string          `json:"swatches"`
		Images   map[string]string `json:"images"`
		UI       struct {
			Swatches []string `json:"swatches"`
		} `json:"ui"`
		Assets map[string]json.RawMessage `json:"assets"`
	}
	_ = json.Unmarshal(raw, &parsed)
	preview := parsed.Preview
	if preview == "" && parsed.Images != nil {
		preview = parsed.Images["preview"]
	}
	if preview == "" && parsed.Assets != nil {
		if b, ok := parsed.Assets["preview"]; ok {
			var s string
			if json.Unmarshal(b, &s) == nil {
				preview = s
			} else {
				var obj struct {
					URL string `json:"url"`
					Src string `json:"src"`
				}
				if json.Unmarshal(b, &obj) == nil {
					if obj.URL != "" {
						preview = obj.URL
					} else {
						preview = obj.Src
					}
				}
			}
		}
	}
	if preview == "" {
		prefix := "/vela/v0/appearance/skins/" + id + "/" + version + "/"
		if kind == "pack" {
			prefix = "/vela/v0/appearance/packs/" + id + "/" + version + "/"
		}
		for _, candidate := range []string{"preview.svg", "preview.png", "preview.webp", "preview.jpg", "preview.jpeg"} {
			if st, err := os.Stat(filepath.Join(versionDir, candidate)); err == nil && st.Mode().IsRegular() {
				preview = prefix + candidate
				break
			}
		}
	}
	swatches := parsed.Swatches
	if len(swatches) == 0 {
		swatches = parsed.UI.Swatches
	}
	if swatches == nil {
		swatches = []string{}
	}
	return preview, swatches
}

// Snapshot returns the cached appearance entries and installed skin kinds.
func (s *SkinStore) Snapshot(ctx context.Context) (generation uint64, packs []json.RawMessage, documents []SkinDocumentEntry, kinds map[string]string) {
	if s == nil {
		return 0, []json.RawMessage{}, []SkinDocumentEntry{}, nil
	}
	snap := s.ensureLoaded(ctx)
	return snap.generation, snap.packs, snap.documents, snap.kinds
}

// HasInstalled reports whether id is currently installed.
func (s *SkinStore) HasInstalled(ctx context.Context, id string) bool {
	if s == nil {
		return false
	}
	snap := s.ensureLoaded(ctx)
	_, ok := snap.kinds[id]
	return ok
}

// List returns built-in skins followed by owner-installed skins. When velaClientDir
// contains skins/catalog.json, built-in previews, versions, and swatches are enriched from it.
func (s *SkinStore) List(ctx context.Context, velaClientDir string) []SkinSummary {
	catalogByID := readBuiltinCatalog(velaClientDir)
	builtins := BuiltinSkins()
	out := make([]SkinSummary, 0, len(builtins)+8)
	for _, b := range builtins {
		item := SkinSummary{
			ID:       b.ID,
			Name:     b.Name,
			Version:  b.Version,
			Kind:     "document",
			Builtin:  true,
			Swatches: b.Swatches,
		}
		if cat, ok := catalogByID[b.ID]; ok {
			if cat.Name != "" {
				item.Name = cat.Name
			}
			if cat.Version != "" {
				item.Version = cat.Version
			}
			if cat.Preview != "" {
				item.Preview = cat.Preview
			}
			if len(cat.Swatches) > 0 {
				item.Swatches = cat.Swatches
			}
		}
		out = append(out, item)
	}
	if s != nil {
		snap := s.ensureLoaded(ctx)
		for _, inst := range snap.ordered {
			cp := inst
			cp.Swatches = append([]string(nil), inst.Swatches...)
			out = append(out, cp)
		}
	}
	return out
}

type catalogSkinEntry struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Preview  string   `json:"preview"`
	Swatches []string `json:"swatches"`
}

func readBuiltinCatalog(velaClientDir string) map[string]catalogSkinEntry {
	out := map[string]catalogSkinEntry{}
	if velaClientDir == "" {
		return out
	}
	raw, err := os.ReadFile(filepath.Join(velaClientDir, "skins", "catalog.json"))
	if err != nil {
		return out
	}
	var doc struct {
		Documents []catalogSkinEntry `json:"documents"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return out
	}
	for _, entry := range doc.Documents {
		if entry.Preview != "" && !strings.HasPrefix(entry.Preview, "/") && !hasForbiddenScheme(entry.Preview) {
			entry.Preview = "/vela/skins/" + strings.TrimPrefix(entry.Preview, "./")
		}
		out[entry.ID] = entry
	}
	return out
}

func (i *instance) servePackAsset(w http.ResponseWriter, r *http.Request) {
	i.serveInstalledSkinFile(w, r, "pack", r.PathValue("id"), r.PathValue("version"), r.PathValue("file"))
}

func (i *instance) serveSkinDocumentAsset(w http.ResponseWriter, r *http.Request) {
	i.serveInstalledSkinFile(w, r, "document", r.PathValue("id"), r.PathValue("version"), r.PathValue("file"))
}

func (i *instance) serveSkinDocumentJSON(w http.ResponseWriter, r *http.Request) {
	version, ok := strings.CutSuffix(r.PathValue("versionJSON"), ".json")
	if !ok {
		problem(w, http.StatusNotFound, "not-found", "not_found")
		return
	}
	i.serveInstalledSkinFile(w, r, "document", r.PathValue("id"), version, "skin.json")
}

func (i *instance) serveInstalledSkinFile(w http.ResponseWriter, r *http.Request, wantKind, id, version, file string) {
	if i.skins == nil || !ValidSkinID(id) || !validSkinVersion(version) || !skinFileNamePattern.MatchString(file) || strings.Contains(file, "..") {
		problem(w, http.StatusNotFound, "not-found", "not_found")
		return
	}
	if file == "skin.json" && r.PathValue("file") != "" {
		problem(w, http.StatusNotFound, "not-found", "not_found")
		return
	}
	ext := strings.ToLower(path.Ext(file))
	mimeType, ok := allowedSkinAssetMIME[ext]
	if !ok {
		problem(w, http.StatusNotFound, "not-found", "not_found")
		return
	}

	snap := i.skins.ensureLoaded(r.Context())
	rec, exists := snap.installed[id]
	if !exists && wantKind == "document" && IsBuiltinSkinID(id) {
		// Built-in documents ship inside the installed Vela web client
		// (<clients>/vela/skins/<id>/<version>/), and some client builds
		// address them under the appearance path like installed skins.
		i.serveBuiltinSkinFile(w, r, id, version, file, mimeType, ext)
		return
	}
	if !exists || rec.summary.Kind != wantKind || rec.summary.Version != version {
		problem(w, http.StatusNotFound, "not-found", "not_found")
		return
	}

	if file == "skin.json" && wantKind == "document" {
		root, err := os.OpenRoot(filepath.Join(i.skins.dir, id, version))
		if err != nil {
			problem(w, http.StatusNotFound, "not-found", "not_found")
			return
		}
		defer root.Close()
		data, err := root.ReadFile("skin.json")
		if err != nil {
			problem(w, http.StatusNotFound, "not-found", "not_found")
			return
		}
		writeImmutableSkinBytes(w, r, mimeType, ext, data)
		return
	}

	root, err := os.OpenRoot(filepath.Join(i.skins.dir, id, version))
	if err != nil {
		problem(w, http.StatusNotFound, "not-found", "not_found")
		return
	}
	defer root.Close()
	info, err := root.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		problem(w, http.StatusNotFound, "not-found", "not_found")
		return
	}
	data, err := root.ReadFile(file)
	if err != nil {
		problem(w, http.StatusNotFound, "not-found", "not_found")
		return
	}
	writeImmutableSkinBytes(w, r, mimeType, ext, data)
}

func (i *instance) serveBuiltinSkinFile(w http.ResponseWriter, r *http.Request, id, version, file, mimeType, ext string) {
	if i.host.ClientsDir == "" {
		problem(w, http.StatusNotFound, "not-found", "not_found")
		return
	}
	if file == "skin.json" {
		file = "document.json"
	}
	root, err := os.OpenRoot(filepath.Join(i.host.ClientsDir, "vela", "skins", id, version))
	if err != nil {
		problem(w, http.StatusNotFound, "not-found", "not_found")
		return
	}
	defer root.Close()
	info, err := root.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		problem(w, http.StatusNotFound, "not-found", "not_found")
		return
	}
	data, err := root.ReadFile(file)
	if err != nil {
		problem(w, http.StatusNotFound, "not-found", "not_found")
		return
	}
	writeImmutableSkinBytes(w, r, mimeType, ext, data)
}

func writeImmutableSkinBytes(w http.ResponseWriter, r *http.Request, mimeType, ext string, data []byte) {
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if ext == ".svg" {
		w.Header().Set("Content-Security-Policy", svgSkinCSP)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}
