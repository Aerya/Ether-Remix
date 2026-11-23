package actions

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
)

const (
	defaultExportMaxItems = 5000
	hardExportMaxItems    = 50000
	hardExportMaxBytes    = 50 * 1024 * 1024 // 50 MiB file ceiling
)

var safeExportNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// FileExportSink writes export and digest artifacts atomically inside Dir,
// enforcing bounded size and retention and rejecting any path traversal.
type FileExportSink struct {
	Dir             string
	BaseDownloadURL string // e.g. "/api/flows/exports"
	MaxBytes        int64
	MaxFiles        int
}

// Save atomically persists data to a sanitized filename inside s.Dir.
func (s *FileExportSink) Save(_ context.Context, filename string, data []byte) (ExportArtifact, error) {
	if s == nil || strings.TrimSpace(s.Dir) == "" {
		return ExportArtifact{}, errors.New("export directory not configured")
	}
	limit := s.MaxBytes
	if limit <= 0 {
		limit = hardExportMaxBytes
	}
	if int64(len(data)) > limit {
		return ExportArtifact{}, fmt.Errorf("export artifact exceeds %d byte limit", limit)
	}
	cleanName := sanitizeExportFilename(filename)
	if cleanName == "" {
		return ExportArtifact{}, errors.New("invalid export filename")
	}
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return ExportArtifact{}, fmt.Errorf("create export dir: %w", err)
	}
	dst := filepath.Join(s.Dir, cleanName)
	absDir, err := filepath.Abs(s.Dir)
	if err != nil {
		return ExportArtifact{}, err
	}
	absDst, err := filepath.Abs(dst)
	if err != nil || !strings.HasPrefix(absDst, absDir+string(os.PathSeparator)) {
		return ExportArtifact{}, errors.New("invalid export path")
	}

	tmp, err := os.CreateTemp(s.Dir, ".export-*")
	if err != nil {
		return ExportArtifact{}, fmt.Errorf("create temp export file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return ExportArtifact{}, err
	}
	if err := tmp.Close(); err != nil {
		return ExportArtifact{}, err
	}
	if err := os.Chmod(tmpPath, 0o640); err != nil {
		return ExportArtifact{}, err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return ExportArtifact{}, err
	}
	cleanup = false

	if s.MaxFiles > 0 {
		s.pruneOldExports()
	}

	dlURL := ""
	if base := strings.TrimRight(strings.TrimSpace(s.BaseDownloadURL), "/"); base != "" {
		dlURL = base + "/" + cleanName
	}
	return ExportArtifact{
		Filename:    cleanName,
		ExportPath:  dst,
		DownloadURL: dlURL,
		SizeBytes:   int64(len(data)),
	}, nil
}

func (s *FileExportSink) pruneOldExports() {
	entries, err := os.ReadDir(s.Dir)
	if err != nil || len(entries) <= s.MaxFiles {
		return
	}
	type fileInfo struct {
		name string
		mod  time.Time
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if info, err := e.Info(); err == nil {
			files = append(files, fileInfo{name: e.Name(), mod: info.ModTime()})
		}
	}
	if len(files) <= s.MaxFiles {
		return
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].mod.Before(files[j].mod)
	})
	for _, f := range files[:len(files)-s.MaxFiles] {
		_ = os.Remove(filepath.Join(s.Dir, f.name))
	}
}

func sanitizeExportFilename(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	base = strings.TrimLeft(base, ".")
	base = safeExportNameRe.ReplaceAllString(base, "_")
	if len(base) > 120 {
		ext := filepath.Ext(base)
		base = base[:120-len(ext)] + ext
	}
	if base == "" || base == "." || base == ".." {
		return ""
	}
	return base
}

// NewLibraryRefresh constructs the built-in library.refresh action handler.
func NewLibraryRefresh(deps Deps) flows.ActionHandler {
	spec := flows.ActionSpec{
		Type:        "library.refresh",
		Owner:       events.OwnerBuiltin,
		Group:       "library",
		Label:       "Refresh library or collection",
		Description: "Trigger a refresh of a library (built-in, catalog source, or plugin library) or rebuild a collection.",
		Icon:        "refresh-cw",
		Idempotent:  true,
		SideEffect:  true,
		Params: []flows.ParamSchema{
			{Key: "libraryId", Type: events.FieldString, Label: "Library", Description: "Library ID or key to refresh.", Template: true, Picker: flows.PickerLibrary},
			{Key: "collectionId", Type: events.FieldString, Label: "Collection ID", Description: "Collection ID to rebuild instead of refreshing a library.", Template: true},
			{Key: "fullSync", Type: events.FieldBool, Label: "Full sync", Description: "Force a full scan/walk when the library source supports incremental sync.", Default: false},
		},
		Outputs: []events.FieldSchema{
			{Name: "refreshed", Type: events.FieldBool, Label: "Refreshed"},
			{Name: "target", Type: events.FieldString, Label: "Target"},
			{Name: "kind", Type: events.FieldString, Label: "Target kind"},
			{Name: "items", Type: events.FieldInt, Label: "Items processed"},
			{Name: "durationMs", Type: events.FieldDurationMS, Label: "Duration", Unit: "ms"},
			{Name: "dryRun", Type: events.FieldBool, Label: "Dry run"},
		},
	}

	return flows.ActionFunc{ActionSpec: spec, Run: func(ctx context.Context, actx flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
		libraryID := strings.TrimSpace(paramString(params, "libraryId", ""))
		collectionID := strings.TrimSpace(paramString(params, "collectionId", ""))
		fullSync, _, err := paramBool(params, "fullSync", false)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("library.refresh: %w", err)
		}

		target := libraryID
		kind := "library"
		if collectionID != "" {
			target = collectionID
			kind = "collection"
		} else if libraryID == "" {
			target = "catalog"
			kind = "catalog"
		}

		if actx.DryRun {
			return flows.ActionResult{
				Output: map[string]any{
					"dryRun":     true,
					"refreshed":  false,
					"target":     target,
					"kind":       kind,
					"items":      0,
					"durationMs": int64(0),
				},
			}, nil
		}
		if deps.Libraries == nil {
			return flows.ActionResult{}, errors.New("library.refresh: library controller unavailable")
		}

		start := deps.now()
		var res RefreshResult
		if collectionID != "" {
			res, err = deps.Libraries.RefreshCollection(ctx, collectionID)
		} else {
			res, err = deps.Libraries.RefreshLibrary(ctx, libraryID, fullSync)
		}
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("library.refresh: %w", err)
		}
		if res.DurationMS == 0 {
			if d := deps.now().Sub(start).Milliseconds(); d > 0 {
				res.DurationMS = d
			}
		}
		if res.Target == "" {
			res.Target = target
		}
		if res.Kind == "" {
			res.Kind = kind
		}
		return flows.ActionResult{
			Output: map[string]any{
				"dryRun":     false,
				"refreshed":  true,
				"target":     res.Target,
				"kind":       res.Kind,
				"items":      res.Items,
				"durationMs": res.DurationMS,
			},
		}, nil
	}}
}

// NewLibraryExport constructs the built-in library.export action handler.
func NewLibraryExport(deps Deps) flows.ActionHandler {
	spec := flows.ActionSpec{
		Type:        "library.export",
		Owner:       events.OwnerBuiltin,
		Group:       "library",
		Label:       "Export library metadata",
		Description: "Export server, library, collection, playlist, or item metadata to CSV, JSON, XML, or M3U with configurable metadata, media info, and artwork detail levels.",
		Icon:        "download",
		Idempotent:  true,
		SideEffect:  true,
		Params: []flows.ParamSchema{
			{
				Key:     "scope",
				Type:    events.FieldEnum,
				Label:   "Export scope",
				Default: "library",
				Enum: []events.EnumOption{
					{Value: "server", Label: "Entire server"},
					{Value: "library", Label: "Library"},
					{Value: "collection", Label: "Collection"},
					{Value: "playlist", Label: "Playlist"},
					{Value: "item", Label: "Single item"},
				},
			},
			{Key: "targetId", Type: events.FieldString, Label: "Target ID", Description: "Library, collection, playlist, or item key (optional for server scope).", Template: true, Picker: flows.PickerLibrary},
			{
				Key:     "format",
				Type:    events.FieldEnum,
				Label:   "Output format",
				Default: "csv",
				Enum: []events.EnumOption{
					{Value: "csv", Label: "CSV"},
					{Value: "json", Label: "JSON"},
					{Value: "xml", Label: "XML"},
					{Value: "m3u", Label: "M3U playlist"},
				},
			},
			{Key: "metadataLevel", Type: events.FieldInt, Label: "Metadata level (0–3)", Description: "0: core keys, 1: basic details, 2: full cast/tags/IDs, 3: extended.", Default: 2},
			{Key: "mediaInfoLevel", Type: events.FieldInt, Label: "Media info level (0–2)", Description: "0: none, 1: resolution/codec/size summary, 2: detailed HDR & audio.", Default: 1},
			{Key: "artworkLevel", Type: events.FieldInt, Label: "Artwork level (0–1)", Description: "0: omit artwork URLs, 1: include poster and backdrop URLs.", Default: 1},
			{Key: "maxItems", Type: events.FieldInt, Label: "Max items", Description: "Upper bound on exported items (1–50000).", Default: defaultExportMaxItems},
			{Key: "filename", Type: events.FieldString, Label: "Filename stem", Description: "Optional custom filename stem (extension added automatically).", Template: true},
		},
		Outputs: []events.FieldSchema{
			{Name: "exportPath", Type: events.FieldString, Label: "Export file path"},
			{Name: "downloadUrl", Type: events.FieldString, Label: "Download URL"},
			{Name: "filename", Type: events.FieldString, Label: "Filename"},
			{Name: "format", Type: events.FieldString, Label: "Format"},
			{Name: "itemCount", Type: events.FieldInt, Label: "Exported item count"},
			{Name: "sizeBytes", Type: events.FieldInt, Label: "Export size", Unit: "bytes"},
			{Name: "dryRun", Type: events.FieldBool, Label: "Dry run"},
		},
	}

	return flows.ActionFunc{ActionSpec: spec, Run: func(ctx context.Context, actx flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
		scope := strings.ToLower(strings.TrimSpace(paramString(params, "scope", "library")))
		switch scope {
		case "server", "library", "collection", "playlist", "item":
		default:
			return flows.ActionResult{}, fmt.Errorf("library.export: unsupported scope %q", scope)
		}
		targetID := strings.TrimSpace(paramString(params, "targetId", ""))
		if scope != "server" && targetID == "" {
			return flows.ActionResult{}, fmt.Errorf("library.export: targetId is required for scope %q", scope)
		}
		format := strings.ToLower(strings.TrimSpace(paramString(params, "format", "csv")))
		switch format {
		case "csv", "json", "xml", "m3u":
		default:
			return flows.ActionResult{}, fmt.Errorf("library.export: unsupported format %q", format)
		}
		metaLevel, _, err := paramInt(params, "metadataLevel", 2)
		if err != nil || metaLevel < 0 || metaLevel > 3 {
			return flows.ActionResult{}, errors.New("library.export: metadataLevel must be 0–3")
		}
		mediaLevel, _, err := paramInt(params, "mediaInfoLevel", 1)
		if err != nil || mediaLevel < 0 || mediaLevel > 2 {
			return flows.ActionResult{}, errors.New("library.export: mediaInfoLevel must be 0–2")
		}
		artLevel, _, err := paramInt(params, "artworkLevel", 1)
		if err != nil || artLevel < 0 || artLevel > 1 {
			return flows.ActionResult{}, errors.New("library.export: artworkLevel must be 0 or 1")
		}
		maxItems, _, err := paramInt(params, "maxItems", defaultExportMaxItems)
		if err != nil || maxItems < 1 {
			return flows.ActionResult{}, errors.New("library.export: maxItems must be >= 1")
		}
		if maxItems > hardExportMaxItems {
			maxItems = hardExportMaxItems
		}

		now := deps.now().UTC()
		stem := strings.TrimSpace(paramString(params, "filename", ""))
		if stem == "" {
			if targetID != "" {
				stem = fmt.Sprintf("export_%s_%s_%s", scope, targetID, now.Format("20060102_150405"))
			} else {
				stem = fmt.Sprintf("export_%s_%s", scope, now.Format("20060102_150405"))
			}
		}
		stem = strings.TrimSuffix(stem, "."+format)
		filename := sanitizeExportFilename(stem + "." + format)
		if filename == "" {
			filename = fmt.Sprintf("export_%s.%s", now.Format("20060102_150405"), format)
		}

		if deps.Libraries == nil {
			if actx.DryRun {
				return flows.ActionResult{
					Output: map[string]any{
						"dryRun":      true,
						"exportPath":  "",
						"downloadUrl": "",
						"filename":    filename,
						"format":      format,
						"itemCount":   0,
						"sizeBytes":   int64(0),
					},
				}, nil
			}
			return flows.ActionResult{}, errors.New("library.export: library controller unavailable")
		}

		q := ExportQuery{
			Scope:          scope,
			TargetID:       targetID,
			MetadataLevel:  metaLevel,
			MediaInfoLevel: mediaLevel,
			ArtworkLevel:   artLevel,
			MaxItems:       maxItems,
		}
		items, err := deps.Libraries.ListExportItems(ctx, q)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("library.export: list items: %w", err)
		}
		if len(items) > maxItems {
			items = items[:maxItems]
		}
		projected := projectExportItems(items, metaLevel, mediaLevel, artLevel)
		data, err := encodeExport(projected, q, format, now)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("library.export: encode %s: %w", format, err)
		}

		if actx.DryRun {
			return flows.ActionResult{
				Output: map[string]any{
					"dryRun":      true,
					"exportPath":  "",
					"downloadUrl": "",
					"filename":    filename,
					"format":      format,
					"itemCount":   len(projected),
					"sizeBytes":   int64(len(data)),
				},
			}, nil
		}
		if deps.Exports == nil {
			return flows.ActionResult{}, errors.New("library.export: export sink not configured")
		}
		artifact, err := deps.Exports.Save(ctx, filename, data)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("library.export: save: %w", err)
		}
		return flows.ActionResult{
			Output: map[string]any{
				"dryRun":      false,
				"exportPath":  artifact.ExportPath,
				"downloadUrl": artifact.DownloadURL,
				"filename":    artifact.Filename,
				"format":      format,
				"itemCount":   len(projected),
				"sizeBytes":   artifact.SizeBytes,
			},
		}, nil
	}}
}

func projectExportItems(items []ExportItem, metaLevel, mediaLevel, artLevel int) []ExportItem {
	out := make([]ExportItem, len(items))
	for i, it := range items {
		p := ExportItem{
			Key:         it.Key,
			Title:       it.Title,
			ShowTitle:   it.ShowTitle,
			Season:      it.Season,
			Episode:     it.Episode,
			MediaType:   it.MediaType,
			Year:        it.Year,
			LibraryID:   it.LibraryID,
			LibraryName: it.LibraryName,
			PlayURL:     it.PlayURL,
		}
		if metaLevel >= 1 {
			p.Summary = it.Summary
			p.ContentRating = it.ContentRating
			p.Studio = it.Studio
			p.Rating = it.Rating
			p.AddedAt = it.AddedAt
			p.Genres = it.Genres
		}
		if metaLevel >= 2 {
			p.Actors = it.Actors
			p.Directors = it.Directors
			p.Labels = it.Labels
			p.ExternalIDs = it.ExternalIDs
		}
		if mediaLevel >= 1 {
			p.DurationMS = it.DurationMS
			p.Container = it.Container
			p.VideoCodec = it.VideoCodec
			p.VideoWidth = it.VideoWidth
			p.VideoHeight = it.VideoHeight
			p.BitrateKbps = it.BitrateKbps
			p.FileSizeBytes = it.FileSizeBytes
		}
		if mediaLevel >= 2 {
			p.DynamicRange = it.DynamicRange
			p.AudioCodec = it.AudioCodec
			p.AudioChannels = it.AudioChannels
		}
		if artLevel >= 1 {
			p.PosterURL = it.PosterURL
			p.BackdropURL = it.BackdropURL
		}
		out[i] = p
	}
	return out
}

func encodeExport(items []ExportItem, q ExportQuery, format string, now time.Time) ([]byte, error) {
	switch format {
	case "csv":
		return encodeExportCSV(items, q.MetadataLevel, q.MediaInfoLevel, q.ArtworkLevel)
	case "json":
		envelope := struct {
			ExportedAt     string       `json:"exportedAt"`
			Scope          string       `json:"scope"`
			TargetID       string       `json:"targetId,omitempty"`
			MetadataLevel  int          `json:"metadataLevel"`
			MediaInfoLevel int          `json:"mediaInfoLevel"`
			ArtworkLevel   int          `json:"artworkLevel"`
			ItemCount      int          `json:"itemCount"`
			Items          []ExportItem `json:"items"`
		}{
			ExportedAt:     now.Format(time.RFC3339),
			Scope:          q.Scope,
			TargetID:       q.TargetID,
			MetadataLevel:  q.MetadataLevel,
			MediaInfoLevel: q.MediaInfoLevel,
			ArtworkLevel:   q.ArtworkLevel,
			ItemCount:      len(items),
			Items:          items,
		}
		return json.MarshalIndent(envelope, "", "  ")
	case "xml":
		type xmlDoc struct {
			XMLName    xml.Name     `xml:"LibraryExport"`
			ExportedAt string       `xml:"exportedAt,attr"`
			Scope      string       `xml:"scope,attr"`
			TargetID   string       `xml:"targetId,attr,omitempty"`
			ItemCount  int          `xml:"itemCount,attr"`
			Items      []ExportItem `xml:"Items>Item"`
		}
		doc := xmlDoc{
			ExportedAt: now.Format(time.RFC3339),
			Scope:      q.Scope,
			TargetID:   q.TargetID,
			ItemCount:  len(items),
			Items:      items,
		}
		body, err := xml.MarshalIndent(doc, "", "  ")
		if err != nil {
			return nil, err
		}
		return append([]byte(xml.Header), append(body, '\n')...), nil
	case "m3u":
		return encodeExportM3U(items), nil
	default:
		return nil, fmt.Errorf("unsupported format %q", format)
	}
}

func encodeExportCSV(items []ExportItem, metaLevel, mediaLevel, artLevel int) ([]byte, error) {
	cols := []string{"key", "title", "mediaType", "year", "showTitle", "season", "episode", "libraryId", "libraryName"}
	if metaLevel >= 1 {
		cols = append(cols, "contentRating", "studio", "rating", "genres", "addedAt", "summary")
	}
	if metaLevel >= 2 {
		cols = append(cols, "directors", "actors", "labels", "externalIds")
	}
	if mediaLevel >= 1 {
		cols = append(cols, "durationMs", "container", "videoCodec", "videoWidth", "videoHeight", "bitrateKbps", "fileSizeBytes")
	}
	if mediaLevel >= 2 {
		cols = append(cols, "dynamicRange", "audioCodec", "audioChannels")
	}
	if artLevel >= 1 {
		cols = append(cols, "posterUrl", "backdropUrl")
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(cols); err != nil {
		return nil, err
	}
	for _, it := range items {
		row := []string{
			it.Key,
			it.Title,
			it.MediaType,
			intStrOrEmpty(it.Year),
			it.ShowTitle,
			intStrOrEmpty(it.Season),
			intStrOrEmpty(it.Episode),
			it.LibraryID,
			it.LibraryName,
		}
		if metaLevel >= 1 {
			rating := ""
			if it.Rating > 0 {
				rating = strconv.FormatFloat(it.Rating, 'f', 1, 64)
			}
			row = append(row,
				it.ContentRating,
				it.Studio,
				rating,
				strings.Join(it.Genres, "; "),
				it.AddedAt,
				it.Summary,
			)
		}
		if metaLevel >= 2 {
			row = append(row,
				strings.Join(it.Directors, "; "),
				strings.Join(it.Actors, "; "),
				strings.Join(it.Labels, "; "),
				strings.Join(it.ExternalIDs, "; "),
			)
		}
		if mediaLevel >= 1 {
			row = append(row,
				int64StrOrEmpty(it.DurationMS),
				it.Container,
				it.VideoCodec,
				intStrOrEmpty(it.VideoWidth),
				intStrOrEmpty(it.VideoHeight),
				intStrOrEmpty(it.BitrateKbps),
				int64StrOrEmpty(it.FileSizeBytes),
			)
		}
		if mediaLevel >= 2 {
			row = append(row,
				it.DynamicRange,
				it.AudioCodec,
				intStrOrEmpty(it.AudioChannels),
			)
		}
		if artLevel >= 1 {
			row = append(row, it.PosterURL, it.BackdropURL)
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeExportM3U(items []ExportItem) []byte {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for _, it := range items {
		durSec := -1
		if it.DurationMS > 0 {
			durSec = int(it.DurationMS / 1000)
		}
		display := it.Title
		if it.ShowTitle != "" && it.Season > 0 && it.Episode > 0 {
			display = fmt.Sprintf("%s - S%02dE%02d - %s", it.ShowTitle, it.Season, it.Episode, it.Title)
		} else if it.Year > 0 {
			display = fmt.Sprintf("%s (%d)", it.Title, it.Year)
		}
		display = strings.NewReplacer("\r", " ", "\n", " ").Replace(display)

		b.WriteString("#EXTINF:")
		b.WriteString(strconv.Itoa(durSec))
		if it.Key != "" {
			b.WriteString(` tvg-id="`)
			b.WriteString(m3uAttr(it.Key))
			b.WriteByte('"')
		}
		if it.PosterURL != "" {
			b.WriteString(` tvg-logo="`)
			b.WriteString(m3uAttr(it.PosterURL))
			b.WriteByte('"')
		}
		group := it.LibraryName
		if group == "" {
			group = it.MediaType
		}
		if group != "" {
			b.WriteString(` group-title="`)
			b.WriteString(m3uAttr(group))
			b.WriteByte('"')
		}
		b.WriteByte(',')
		b.WriteString(display)
		b.WriteByte('\n')

		loc := it.PlayURL
		if loc == "" {
			loc = "ether://item/" + it.Key
		}
		b.WriteString(strings.NewReplacer("\r", "", "\n", "").Replace(loc))
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func m3uAttr(s string) string {
	return strings.NewReplacer(`"`, "'", "\r", " ", "\n", " ").Replace(s)
}

func intStrOrEmpty(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func int64StrOrEmpty(n int64) string {
	if n == 0 {
		return ""
	}
	return strconv.FormatInt(n, 10)
}
