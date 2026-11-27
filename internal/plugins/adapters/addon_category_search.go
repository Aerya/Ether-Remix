package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/newznab"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/stremio"
)

// Category searches on source plugins (#792). A stream_source plugin that
// declares ether.contributes.source.search answers GET /ether/search with
// free text, the requested categories it declared and, optionally, a typed
// lookup it declared. Its answer has the stream route's shape: lazy
// releases come back as plugin releases (the plugin lists their files on
// /ether/files/{id} and opens them on /ether/open/{id}), torrents and NZBs
// as ordinary releases. Every other add-on keeps refusing category
// searches.

const sourceSearchRoute = "/ether/search"

var _ newznab.CategoryBackend = (*addonBackend)(nil)

// AnswersCategories implements newznab.CategoryBackend.
func (b *addonBackend) AnswersCategories(ctx context.Context, cats []int) bool {
	_, rec, err := b.record(ctx)
	if err != nil || !rec.Enabled {
		return false
	}
	return rec.Manifest.SourceSearch().AnswersCategories(cats)
}

// categorySearch asks the plugin's search route once.
func (b *addonBackend) categorySearch(ctx context.Context, mgr *plugins.Manager, rec plugins.Record, req core.SearchRequest, reserve func(context.Context, string) error) ([]core.Release, error) {
	decl := rec.Manifest.SourceSearch()
	requested, err := core.SearchCategories(req.Category)
	if err != nil {
		return nil, err
	}
	cats := decl.MatchCategories(requested)
	if len(cats) == 0 {
		return nil, fmt.Errorf("indexer %q does not support category searches", rec.Name)
	}
	if err := reserve(ctx, "hit"); err != nil {
		return nil, err
	}
	if err := b.s.spendSearch(ctx, rec); err != nil {
		return nil, err
	}
	resp, err := mgr.Call(ctx, rec.ID, plugins.Request{Method: http.MethodGet, Path: sourceSearchRoute, Query: sourceSearchQuery(ctx, req, cats, decl)})
	if err != nil {
		b.s.noteFailure(rec.ID)
		return nil, err
	}
	if resp.Status < 200 || resp.Status > 299 {
		he := &stremio.HTTPError{Status: resp.Status, RetryAfter: header(resp.Headers, "Retry-After")}
		b.s.noteFailure(rec.ID)
		if he.RateLimited() {
			return nil, &newznab.RateLimitError{RetryAfter: he.RetryAfter, Err: he}
		}
		return nil, he
	}
	var answer struct {
		Streams []stremio.Stream `json:"streams"`
	}
	if err := json.Unmarshal(resp.Body, &answer); err != nil {
		b.s.noteFailure(rec.ID)
		return nil, errors.New("the source plugin answered its search with invalid JSON")
	}
	return b.convertCategory(rec, req, cats[0], answer.Streams), nil
}

// sourceSearchQuery is the search route's query string: query, category
// (the declared ones requested, comma separated), purpose, and the typed
// lookup only when the plugin declared its type.
func sourceSearchQuery(ctx context.Context, req core.SearchRequest, cats []int, decl *plugins.SourceSearchMeta) map[string]string {
	ids := make([]string, len(cats))
	for i, c := range cats {
		ids[i] = strconv.Itoa(c)
	}
	q := map[string]string{"query": strings.TrimSpace(req.Title), "category": strings.Join(ids, ",")}
	if p := core.IndexerPurpose(ctx); p != "" && p != core.IndexerPurposeOther {
		q["purpose"] = p
	}
	if l := req.Lookup; l != nil && decl.AnswersLookup(l.Type) {
		q["lookup"] = l.Type
		for k, v := range l.Fields {
			if v = strings.TrimSpace(v); v != "" {
				q["lookup."+k] = v
			}
		}
	}
	return q
}

// convertCategory turns a search answer into releases in the plugin's
// order: lazy entries become plugin releases, torrents and NZBs ordinary
// ones. Plain HTTP links and owner-folder files have no release identity a
// caller could hand back, so they are skipped.
func (b *addonBackend) convertCategory(rec plugins.Record, req core.SearchRequest, cat int, streams []stremio.Stream) []core.Release {
	if len(streams) > maxAddonResults {
		streams = streams[:maxAddonResults]
	}
	origin := ""
	if u, err := stremio.NormalizeURL(rec.SourceURL); err == nil {
		origin = stremio.Origin(u)
	}
	out := make(map[int]core.Release, len(streams))
	var ordinary []stremio.Stream
	var ordinaryAt []int
	skipped := 0
	for i := range streams {
		st := streams[i]
		var eb etherStream
		if len(st.Ether) > 0 {
			_ = json.Unmarshal(st.Ether, &eb)
		}
		if eb.Lazy {
			if rel, ok := pluginCategoryRelease(rec.ID, st, eb, cat); ok {
				out[i] = rel
			} else {
				skipped++
			}
			continue
		}
		if strings.TrimSpace(eb.File) != "" {
			skipped++
			continue
		}
		if eb.ReleaseName != "" {
			st.BehaviorHints.Filename = eb.ReleaseName
			if st.Description == "" && st.Title == "" {
				st.Title = eb.ReleaseName
			}
		}
		if eb.Size > 0 {
			st.BehaviorHints.VideoSize = eb.Size
		}
		ordinary = append(ordinary, st)
		ordinaryAt = append(ordinaryAt, i)
	}
	if len(ordinary) > 0 {
		conv := stremio.Convert(ordinary, core.SearchRequest{Title: req.Title, Category: req.Category}, stremio.Lookup{}, stremio.ConvertOptions{Origin: origin, Instance: b.instance})
		skipped += conv.SkippedNoHash + conv.SkippedAmbiguous
		for j, rel := range conv.Releases {
			if rel.Protocol != core.ProtocolTorrent && rel.Protocol != core.ProtocolUsenet {
				skipped++
				continue
			}
			rel.Category = cat
			rel.StreamURL, rel.StreamHeaders, rel.StreamOrigin = "", nil, ""
			out[ordinaryAt[conv.Source[j]]] = rel
		}
	}
	if skipped > 0 {
		slog.Default().Debug("source plugin search answers skipped without a lazy id, torrent or NZB", "plugin", rec.ID, "skipped", skipped)
	}
	keys := make([]int, 0, len(out))
	for i := range out {
		keys = append(keys, i)
	}
	sort.Ints(keys)
	rels := make([]core.Release, 0, len(keys))
	for _, i := range keys {
		rels = append(rels, out[i])
	}
	return rels
}

// pluginCategoryRelease is the release of one lazy search answer; its GUID
// names the plugin and the lazy id as a title lookup's lazy stream does.
func pluginCategoryRelease(pluginID string, st stremio.Stream, eb etherStream, cat int) (core.Release, bool) {
	id := strings.TrimSpace(eb.ID)
	name := strings.TrimSpace(eb.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(st.Title)
	}
	if name == "" {
		name = strings.TrimSpace(st.Name)
	}
	if id == "" || name == "" || len(id) > maxPluginReleaseIDBytes {
		return core.Release{}, false
	}
	rel := core.Release{
		Name:     name,
		Size:     max(eb.Size, 0),
		Category: cat,
		Info:     releaseparse.Parse(name),
		Protocol: "direct",
		GUID:     fmt.Sprintf("plugin:%s:lazy:%s", pluginID, id),
	}
	if len(eb.Languages) > 0 {
		rel.Info.Languages = mergeLanguages(rel.Info.Languages, eb.Languages)
	}
	return rel, true
}

// maxPluginReleaseIDBytes bounds a lazy id a caller can hand back.
const maxPluginReleaseIDBytes = 512
