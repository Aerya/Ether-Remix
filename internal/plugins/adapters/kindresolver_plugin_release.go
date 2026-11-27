package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/kindstream"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// Plugin releases (#792): an indexer_search candidate a source plugin
// answered for a category it declared. It has no info hash or NZB, so
// release_files asks the plugin (GET /ether/files/{id}) and a release
// source opens through the plugin's open route (GET /ether/open/{id} with
// the chosen file). The bytes then stream like any plugin range source:
// HTTP Range reads whose every host, redirects included, must be in the
// source plugin's permissions.network.

const (
	maxPluginReleaseFiles   = 2000
	pluginReleaseFilesRoute = "/ether/files/"
)

// pluginReleaseOf is the plugin release a hint names, or nil.
func pluginReleaseOf(hint *core.ReleaseHint) *core.PluginReleaseRef {
	if hint == nil || hint.Candidate == nil || hint.Candidate.PluginRelease == nil {
		return nil
	}
	ref := hint.Candidate.PluginRelease
	if strings.TrimSpace(ref.Plugin) == "" || strings.TrimSpace(ref.ID) == "" {
		return nil
	}
	return ref
}

// pluginReleaseSource checks that a handed-back reference names an enabled
// source plugin that declared category searches; a caller cannot point
// Ether at any other plugin's routes.
func (r *KindResolver) pluginReleaseSource(ctx context.Context, ref core.PluginReleaseRef) (plugins.Record, error) {
	if r.mgr == nil {
		return plugins.Record{}, core.ErrSourceUnavailable
	}
	if len(ref.ID) > maxPluginReleaseIDBytes {
		return plugins.Record{}, fmt.Errorf("plugin release id too long: %w", core.ErrReleaseRejected)
	}
	rec, err := r.mgr.Get(ctx, ref.Plugin)
	if err != nil || !rec.Enabled || rec.Manifest.SourceSearch() == nil {
		return plugins.Record{}, fmt.Errorf("plugin release from %s: %w", ref.Plugin, core.ErrSourceUnavailable)
	}
	return rec, nil
}

// pluginReleaseFiles answers release_files for a plugin candidate.
func (r *KindResolver) pluginReleaseFiles(ctx context.Context, cand core.SourceCandidate) ([]core.SourceCandidate, error) {
	ref := *cand.PluginRelease
	rec, err := r.pluginReleaseSource(ctx, ref)
	if err != nil {
		return nil, err
	}
	workKey := "release_files:plugin:" + rec.ID + ":" + ref.ID
	if handed, ok := r.takeSourceHandoff(workKey); ok {
		noteResolveSourceWork(ctx, workKey, true)
		return handed, nil
	}
	endWork := beginResolveSourceWork(ctx, workKey)
	defer endWork()
	raw, _, err := r.runDetachedSourceFlight(ctx, workKey, sourceReleaseFilesTimeout, func(workCtx context.Context) (any, error) {
		resp, err := r.mgr.Call(workCtx, rec.ID, plugins.Request{Method: http.MethodGet, Path: pluginReleaseFilesRoute + url.PathEscape(ref.ID)})
		if err != nil {
			return nil, fmt.Errorf("plugin %s files %s: %w", rec.ID, ref.ID, core.ErrSourceUnavailable)
		}
		if resp.Status < 200 || resp.Status > 299 {
			return nil, fmt.Errorf("plugin %s files %s status %d: %w", rec.ID, ref.ID, resp.Status, core.ErrSourceUnavailable)
		}
		var answer struct {
			Files []struct {
				Path string `json:"path"`
				Size int64  `json:"size"`
			} `json:"files"`
		}
		if err := json.Unmarshal(resp.Body, &answer); err != nil {
			return nil, fmt.Errorf("plugin %s files %s invalid json: %w", rec.ID, ref.ID, core.ErrSourceUnavailable)
		}
		out := cand
		out.Files = make([]core.ReleaseFile, 0, min(len(answer.Files), maxPluginReleaseFiles))
		for _, f := range answer.Files {
			if len(out.Files) == maxPluginReleaseFiles {
				break
			}
			p := strings.TrimSpace(f.Path)
			if p == "" {
				continue
			}
			out.Files = append(out.Files, core.ReleaseFile{Index: len(out.Files), Path: p, Size: max(f.Size, 0)})
		}
		out.FileCount = len(out.Files)
		return []core.SourceCandidate{out}, nil
	}, func(res any) {
		if cands, ok := res.([]core.SourceCandidate); ok {
			r.storeSourceHandoff(workKey, cands)
		}
	})
	if err != nil {
		return nil, err
	}
	cands, _ := raw.([]core.SourceCandidate)
	return cloneSourceCandidates(cands), nil
}

// openPluginRelease opens a plugin release's chosen file through the
// source plugin's open route.
func (r *KindResolver) openPluginRelease(ctx context.Context, ref core.PluginReleaseRef, rel core.Release, mime string) (core.MediaSource, error) {
	rec, err := r.pluginReleaseSource(ctx, ref)
	if err != nil {
		return nil, err
	}
	ss := r.sources
	if ss == nil {
		ss, _ = r.indexer.(*StreamSources)
	}
	if ss != nil {
		if err := ss.spendGrab(ctx, rec.ID); err != nil {
			return nil, err
		}
	}
	q := map[string]string{}
	if t := rel.Target; t != nil {
		if t.ExactFile != "" {
			q["file"] = t.ExactFile
		}
		if t.FileIndex != nil {
			q["fileIndex"] = strconv.Itoa(*t.FileIndex)
		}
	}
	openCtx, cancel := context.WithTimeout(ctx, openDeadline)
	resp, err := r.mgr.Call(openCtx, rec.ID, plugins.Request{Method: http.MethodGet, Path: "/ether/open/" + url.PathEscape(ref.ID), Query: q})
	cancel()
	if err != nil {
		return nil, fmt.Errorf("plugin %s open %s: %w", rec.ID, ref.ID, core.ErrSourceUnavailable)
	}
	if resp.Status < 200 || resp.Status > 299 {
		return nil, fmt.Errorf("plugin %s open %s status %d: %w", rec.ID, ref.ID, resp.Status, core.ErrSourceUnavailable)
	}
	var opened openResponse
	if err := json.Unmarshal(resp.Body, &opened); err != nil {
		return nil, fmt.Errorf("plugin %s open %s invalid json: %w", rec.ID, ref.ID, core.ErrSourceUnavailable)
	}
	if f := strings.TrimSpace(opened.File); f != "" {
		local := &StreamSources{mgr: r.mgr}
		payload, err := local.prepareLocalFile(ctx, rel, rec.ID, rec.Name, f)
		if err != nil {
			return nil, err
		}
		return local.WrapEngine(nil).Open(ctx, rel.ID, payload)
	}
	if strings.TrimSpace(opened.URL) == "" {
		return nil, fmt.Errorf("plugin %s open %s returned no url or file: %w", rec.ID, ref.ID, core.ErrSourceUnavailable)
	}
	// The byte source outlives the open call, so the allowlist check runs
	// on an uncancelled context.
	allowCtx := context.WithoutCancel(ctx)
	host := r.mgr.HostFor(rec.ID)
	bs, err := kindstream.Open(ctx, core.PlayableSource{
		Pattern: kindstream.PatternRange,
		URL:     strings.TrimSpace(opened.URL),
		Headers: opened.Headers,
		MIME:    mime,
	}, kindstream.Deps{
		HTTP:    r.http,
		Backoff: r.backoff,
		Secret: func(sctx context.Context, name string) (string, error) {
			return host.SecretGet(sctx, name)
		},
		AllowHost: func(h string) bool {
			hctx, cancel := context.WithTimeout(allowCtx, 5*time.Second)
			defer cancel()
			return r.mgr.AllowNetworkHost(hctx, rec.ID, h)
		},
	})
	if err != nil {
		return nil, err
	}
	return kindstream.MediaSource(bs, rel.ID), nil
}
