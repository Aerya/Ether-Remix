package adapters

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

const (
	relatedCallDeadline = 8 * time.Second
	relatedMemoMax      = 4096
	relatedHitTTL       = 6 * time.Hour
	relatedMissTTL      = time.Hour
	relatedMaxItems     = 100
)

type relatedKey struct {
	key     core.RatingKey
	purpose string
}

type relatedMemo struct {
	at    time.Time
	label string
	keys  []core.RatingKey
	done  chan struct{} // closed when the call finishes
}

type relatedMemos struct {
	mu sync.Mutex
	m  map[relatedKey]*relatedMemo
}

var kindRelated relatedMemos

// RelatedAvailable reports whether the item's kind answers purpose.
func (c *KindCatalog) RelatedAvailable(ctx context.Context, key core.RatingKey, purpose string) bool {
	if c == nil || c.items == nil || !key.IsPluginKey() {
		return false
	}
	_, libID, err := c.items.GetByKey(ctx, key)
	if err != nil {
		return false
	}
	rec, err := c.items.GetKindByLibrary(ctx, libID)
	return err == nil && rec.Decl.Related.Answers(purpose)
}

// Related asks the item's kind plugin for items related to it ("similar" or
// "continuation"), stores them (pinned, like live search results) and returns
// them in order. One call per item and purpose is in flight; answers are held
// for six hours (misses for one). wait bounds how long the caller waits: the
// call itself runs on to completion in the background and fills the memo, so
// the next ask is instant.
func (c *KindCatalog) Related(ctx context.Context, key core.RatingKey, purpose string, limit int, wait time.Duration) (string, []core.Title, error) {
	if c == nil || c.items == nil || c.mgr == nil || !key.IsPluginKey() {
		return "", nil, core.ErrNotFound
	}
	if purpose != "similar" && purpose != "continuation" {
		return "", nil, core.ErrNotFound
	}
	item, libID, err := c.items.GetByKey(ctx, key)
	if err != nil {
		return "", nil, core.ErrNotFound
	}
	rec, err := c.items.GetKindByLibrary(ctx, libID)
	if err != nil || !rec.Decl.Related.Answers(purpose) {
		return "", nil, core.ErrNotFound
	}
	lib, err := c.libs.Get(ctx, libID)
	if err != nil {
		return "", nil, core.ErrNotFound
	}
	if limit <= 0 || limit > relatedMaxItems {
		limit = 25
	}
	mk := relatedKey{key, purpose}
	kindRelated.mu.Lock()
	if kindRelated.m == nil {
		kindRelated.m = map[relatedKey]*relatedMemo{}
	}
	memo := kindRelated.m[mk]
	if memo != nil {
		select {
		case <-memo.done:
			ttl := relatedMissTTL
			if len(memo.keys) > 0 {
				ttl = relatedHitTTL
			}
			if time.Since(memo.at) > ttl {
				memo = nil
			}
		default:
		}
	}
	if memo == nil {
		if len(kindRelated.m) >= relatedMemoMax {
			for k, m := range kindRelated.m {
				select {
				case <-m.done:
					delete(kindRelated.m, k)
				default:
				}
				if len(kindRelated.m) < relatedMemoMax {
					break
				}
			}
		}
		memo = &relatedMemo{done: make(chan struct{})}
		kindRelated.m[mk] = memo
		go c.fetchRelated(rec.PluginID, rec.KindKey, item, purpose, limit, memo)
	}
	kindRelated.mu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-memo.done:
	case <-timer.C:
		return "", nil, core.ErrNotAvailable
	case <-ctx.Done():
		return "", nil, ctx.Err()
	}
	if len(memo.keys) == 0 {
		return "", nil, core.ErrNotFound
	}
	stored := make([]core.KindItem, 0, len(memo.keys))
	for _, k := range memo.keys {
		if it, _, err := c.items.GetByKey(ctx, k); err == nil {
			stored = append(stored, it)
		}
	}
	return memo.label, c.projectItems(ctx, rec, lib, stored), nil
}

func (c *KindCatalog) fetchRelated(pluginID, kindKey string, item core.KindItem, purpose string, limit int, memo *relatedMemo) {
	defer func() {
		memo.at = time.Now()
		close(memo.done)
	}()
	ctx, cancel := context.WithTimeout(c.syncCtx, relatedCallDeadline)
	defer cancel()
	body, err := json.Marshal(map[string]any{"item": item, "purpose": purpose, "limit": limit})
	if err != nil {
		return
	}
	resp, err := c.mgr.Call(ctx, pluginID, plugins.Request{Method: http.MethodPost, Path: "/ether/v1/kinds/" + kindKey + "/related", Body: body})
	if err != nil || resp.Status != http.StatusOK {
		return
	}
	var answer struct {
		Label string          `json:"label"`
		Items []core.KindItem `json:"items"`
		Order []string        `json:"order"`
	}
	if json.Unmarshal(resp.Body, &answer) != nil || len(answer.Items) == 0 {
		return
	}
	if len(answer.Items) > 4*relatedMaxItems {
		answer.Items = answer.Items[:4*relatedMaxItems]
	}
	if _, err := c.items.UpsertPinnedItems(ctx, pluginID, kindKey, answer.Items); err != nil {
		c.log.Debug("related upsert failed", "plugin", pluginID, "kind", kindKey, "err", err)
		return
	}
	// The answer lists what to show in order; supporting items (an album a
	// track needs, its artist) may come with it. "order" names them when
	// given, else every item of the purpose's kind of thing is shown.
	order := answer.Order
	if len(order) == 0 {
		for _, it := range answer.Items {
			if purpose == "continuation" && !it.Playable {
				continue
			}
			if purpose == "similar" && it.Level != item.Level {
				continue
			}
			order = append(order, it.ID)
		}
	}
	keys := make([]core.RatingKey, 0, min(len(order), limit))
	seen := map[core.RatingKey]bool{item.Key: true}
	for _, id := range order {
		if len(keys) >= limit {
			break
		}
		stored, _, err := c.items.GetByExternal(ctx, pluginID, kindKey, strings.TrimSpace(id))
		if err != nil || seen[stored.Key] || (purpose == "continuation" && !stored.Playable) {
			continue
		}
		seen[stored.Key] = true
		keys = append(keys, stored.Key)
	}
	memo.label = strings.TrimSpace(answer.Label)
	memo.keys = keys
}
