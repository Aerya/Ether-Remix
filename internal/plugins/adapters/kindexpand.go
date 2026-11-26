package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/pluginitems"
	"github.com/braedonsaunders/ether/internal/plugins"
)

const (
	MaxKindExpandPages           = 20
	MaxKindExpandItems           = 2000
	containerExpandFacet         = "__container__"
	maxContainerHydratePerPlugin = 4
	containerHydrateBGTimeout    = 2 * time.Minute
)

// containerHydrateWait bounds how long opening an unhydrated (or stale)
// container waits for its first page of children before returning stored rows
// while pagination continues in the background.
var containerHydrateWait = 1800 * time.Millisecond

type containerHydrateCall struct {
	firstPage chan struct{}
	startedAt time.Time
}

// expansionErrorCooldown suppresses immediately re-launching a facet or
// container expansion after a failed attempt, so repeated opens of a failing
// upstream slice do not block for firstOpenWait on every request.
var expansionErrorCooldown = 15 * time.Minute

type expandWaitState struct {
	firstPage chan struct{}
	startedAt time.Time
}

// firstOpenWait bounds how long the first page of a studio or performer
// waits for its fill's first page: the plugin returns the newest titles first,
// so once that page is stored the first screen is already right, and the rest
// of the catalogue keeps filling in behind the viewer's scroll. Matches
// containerHydrateWait so cold person/facet opens stay responsive (~1.8s)
// while background expansion completes.
var firstOpenWait = 1800 * time.Millisecond

var (
	_              experience.PluginSliceExpander = (*KindCatalog)(nil)
	expandLaunchMu sync.Mutex
	// expandReady holds one channel per running fill (dedup key), closed once
	// the fill has stored its first page (or ended), so first-page readers
	// can wait for it.
	expandReady sync.Map
)

type expandTarget struct {
	facet    string
	valueKey string
	req      core.KindExpandRequest
}

// MaybeExpand checks whether viewer browse query q opens a single declared
// expandable facet bucket or a plugin person's credits on lib. When no fresh
// expansion exists within ttlHours it launches one via Manager.StartExpandJob.
// The first page of the slice then waits (at most firstOpenWait) for the
// fill's first stored page, so the first open shows the newest titles. It
// reports whether it gave up before that page arrived; later pages never wait.
func (c *KindCatalog) MaybeExpand(ctx context.Context, lib core.Library, q frontend.ItemQuery) bool {
	if c == nil || c.items == nil || c.mgr == nil || lib.Source != core.LibraryPlugin {
		return false
	}
	if q.SuppressExpand || strings.TrimSpace(q.Filter.Query) != "" {
		return false
	}
	if !c.IsPluginLibraryActive(ctx, lib) {
		return false
	}
	rec, err := c.items.GetKindByLibrary(ctx, lib.ID)
	if err != nil || rec.Decl.Browse.Expand == nil || len(rec.Decl.Browse.Expand.Facets) == 0 {
		return false
	}

	expandable := make(map[string]bool, len(rec.Decl.Browse.Expand.Facets))
	for _, fk := range rec.Decl.Browse.Expand.Facets {
		expandable[fk] = true
	}
	peopleFields := make(map[string]bool)
	for _, fd := range rec.Decl.Fields {
		if fd.Type == "people" {
			peopleFields[fd.Key] = true
		}
	}

	var targets []expandTarget
	switch {
	case q.Filter.Person == 0:
		mapped := mapFilterToFacets(rec.Decl, q.Filter)
		if len(mapped) != 1 {
			return false
		}
		for field, vals := range mapped {
			if !expandable[field] || len(vals) != 1 {
				return false
			}
			val := strings.TrimSpace(vals[0])
			if val == "" {
				return false
			}
			if peopleFields[field] {
				if pid, canonName, ok, _ := c.items.LookupPersonFacetByName(ctx, lib.ID, field, val); ok && core.IsPluginPersonID(pid) {
					targets = append(targets, expandTarget{
						facet:    field,
						valueKey: strconv.FormatInt(pid, 10),
						req: core.KindExpandRequest{
							Facet:   field,
							Value:   canonName,
							ValueID: pid,
						},
					})
					break
				}
			}
			targets = append(targets, expandTarget{
				facet:    field,
				valueKey: val,
				req: core.KindExpandRequest{
					Facet: field,
					Value: val,
				},
			})
		}

	case core.IsPluginPersonID(q.Filter.Person) && len(q.Filter.Facets) == 0 && len(q.Filter.Genres) == 0 && q.Filter.Studio == "":
		var expandPeople []string
		for _, fk := range rec.Decl.Browse.Expand.Facets {
			if peopleFields[fk] && !slices.Contains(expandPeople, fk) {
				expandPeople = append(expandPeople, fk)
			}
		}
		if len(expandPeople) == 0 {
			return false
		}
		matches, err := c.items.LookupPersonFacetsByID(ctx, lib.ID, expandPeople, q.Filter.Person)
		if err != nil || len(matches) == 0 {
			return false
		}
		pidKey := strconv.FormatInt(q.Filter.Person, 10)
		for _, m := range matches {
			targets = append(targets, expandTarget{
				facet:    m.Field,
				valueKey: pidKey,
				req: core.KindExpandRequest{
					Facet:   m.Field,
					Value:   m.Name,
					ValueID: q.Filter.Person,
				},
			})
		}
	}

	if len(targets) == 0 {
		return false
	}

	ttl := time.Duration(rec.Decl.Browse.Expand.EffectiveTTLHours()) * time.Hour
	errCooldown := expansionErrorCooldown
	if ttl > 0 && ttl < errCooldown {
		errCooldown = ttl
	}
	var waits []*expandWaitState
	now := c.items.Now()
	routePath := "/ether/v1/kinds/" + rec.KindKey + "/expand"
	anyRunning := false
	hasExpiredInflight := false

	for _, t := range targets {
		dedupKey := rec.KindKey + "\x00" + t.facet + "\x00" + t.valueKey
		expandLaunchMu.Lock()
		if c.mgr.IsExpandJobRunning(rec.PluginID, dedupKey) {
			if v, ok := expandReady.Load(dedupKey); ok {
				ws := v.(*expandWaitState)
				if firstOpenWait > 0 && time.Since(ws.startedAt) < firstOpenWait {
					waits = append(waits, ws)
				} else {
					hasExpiredInflight = true
				}
			} else {
				hasExpiredInflight = true
			}
			expandLaunchMu.Unlock()
			anyRunning = true
			continue
		}
		exp, ok, err := c.items.GetFacetExpansion(ctx, rec.PluginID, rec.KindKey, t.facet, t.valueKey)
		if err == nil && ok && (exp.IsFresh(now, ttl) || exp.IsInErrorCooldown(now, errCooldown)) {
			expandLaunchMu.Unlock()
			continue
		}
		ready := make(chan struct{})
		firstPage := make(chan struct{})
		var firstOnce sync.Once
		signal := func() { firstOnce.Do(func() { close(firstPage) }) }
		target := t
		started, startErr := c.mgr.StartExpandJob(rec.PluginID, routePath, dedupKey, func(jobCtx context.Context, run *plugins.ExpandJobRunner) error {
			defer func() {
				expandReady.Delete(dedupKey)
				signal()
			}()
			<-ready
			return c.runFacetExpansion(jobCtx, run, rec, lib.ID, routePath, target, signal)
		})
		if startErr == nil && !started {
			close(ready)
			expandLaunchMu.Unlock()
			anyRunning = true
			hasExpiredInflight = true
			continue
		}
		if startErr != nil {
			close(ready)
			expandLaunchMu.Unlock()
			c.log.Debug("facet expansion deferred", "plugin", rec.PluginID, "kind", rec.KindKey, "facet", t.facet, "err", startErr)
			continue
		}
		markCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = c.items.MarkFacetExpansionRunning(markCtx, rec.PluginID, rec.KindKey, lib.ID, t.facet, t.valueKey)
		cancel()
		ws := &expandWaitState{firstPage: firstPage, startedAt: time.Now()}
		expandReady.Store(dedupKey, ws)
		waits = append(waits, ws)
		close(ready)
		expandLaunchMu.Unlock()
		anyRunning = true
	}

	if !anyRunning {
		return false
	}
	// The first page waits at most once per running fill (remaining time from
	// startedAt up to firstOpenWait) for the first stored page; callers arriving
	// after that initial window and later pages never block.
	if q.Offset != 0 || q.StartAt != 0 || q.AdjacentTo != 0 || len(waits) == 0 || firstOpenWait <= 0 {
		return true
	}
	var wait time.Duration
	for _, ws := range waits {
		if rem := firstOpenWait - time.Since(ws.startedAt); rem > wait {
			wait = rem
		}
	}
	if wait <= 0 {
		return true
	}
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl); rem <= 0 {
			return true
		} else if rem < wait {
			wait = rem
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for _, ws := range waits {
		select {
		case <-ws.firstPage:
		case <-timer.C:
			return true
		case <-ctx.Done():
			return true
		}
	}
	return hasExpiredInflight
}

func (c *KindCatalog) runFacetExpansion(jobCtx context.Context, run *plugins.ExpandJobRunner, rec pluginitems.KindRecord, libID core.LibraryID, routePath string, target expandTarget, firstPage func()) error {
	var (
		pages              int
		callTime, saveTime time.Duration
		began              = time.Now()
	)
	finish := func(state string, added int, errMsg string) {
		c.log.Info("facet expansion finished", "plugin", rec.PluginID, "kind", rec.KindKey, "facet", target.facet,
			"state", state, "pages", pages, "added", added, "took", time.Since(began), "plugin_time", callTime, "save_time", saveTime, "err", errMsg)
		finCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.items.FinishFacetExpansion(finCtx, rec.PluginID, rec.KindKey, libID, target.facet, target.valueKey, state, added, errMsg)
		c.items.BumpGeneration()
	}

	rootLevel := "item"
	if len(rec.Decl.Levels) > 0 {
		rootLevel = rec.Decl.Levels[0].Key
	}

	upsertSlice := func(items []core.KindItem) (accepted int, added int, err error) {
		normalized := make([]core.KindItem, len(items))
		copy(normalized, items)
		for i := range normalized {
			if normalized[i].Level == "" && normalized[i].ParentID == "" {
				normalized[i].Level = rootLevel
			}
		}
		for start := 0; start < len(normalized); start += pluginitems.MaxUpsertBatch {
			end := min(start+pluginitems.MaxUpsertBatch, len(normalized))
			acc, add, uErr := c.items.UpsertPinnedItemsWithAdded(jobCtx, rec.PluginID, rec.KindKey, normalized[start:end])
			if uErr != nil {
				return accepted, added, uErr
			}
			accepted += acc
			added += add
		}
		return accepted, added, nil
	}

	var (
		cursor        string
		totalAccepted int
		totalAdded    int
		seenCursor    = make(map[string]bool)
	)

	for pageIdx := 0; pageIdx < MaxKindExpandPages && totalAccepted < MaxKindExpandItems; pageIdx++ {
		req := target.req
		req.Cursor = cursor
		body, err := json.Marshal(req)
		if err != nil {
			finish(pluginitems.ExpansionStateError, totalAdded, err.Error())
			return err
		}

		callStart := time.Now()
		resp, err := run.Call(plugins.WithBackgroundCall(jobCtx), plugins.Request{
			PluginID: rec.PluginID,
			Method:   http.MethodPost,
			Path:     routePath,
			Headers:  map[string]string{"content-type": "application/json"},
			Body:     body,
		})
		callTime += time.Since(callStart)
		pages++
		if err != nil {
			finish(pluginitems.ExpansionStateError, totalAdded, err.Error())
			return err
		}
		if resp.Status == http.StatusNotFound || resp.Status == http.StatusNotImplemented {
			finish(pluginitems.ExpansionStateDone, totalAdded, "")
			return nil
		}
		if resp.Status < 200 || resp.Status >= 300 {
			callErr := fmt.Errorf("expand %s/%s (%s=%s): status %d", rec.PluginID, rec.KindKey, target.facet, target.req.Value, resp.Status)
			finish(pluginitems.ExpansionStateError, totalAdded, callErr.Error())
			return callErr
		}

		var page core.KindExpandResponse
		if len(resp.Body) > 0 {
			if err := json.Unmarshal(resp.Body, &page); err != nil {
				finish(pluginitems.ExpansionStateError, totalAdded, err.Error())
				return err
			}
		}

		if len(page.Items) > 0 {
			remaining := MaxKindExpandItems - totalAccepted
			batch := page.Items
			if len(batch) > remaining {
				batch = batch[:remaining]
			}
			saveStart := time.Now()
			acc, add, uErr := upsertSlice(batch)
			saveTime += time.Since(saveStart)
			if uErr != nil {
				finish(pluginitems.ExpansionStateError, totalAdded, uErr.Error())
				return uErr
			}
			totalAccepted += acc
			totalAdded += add
			c.items.BumpGeneration()
			firstPage()
			run.SetProgress(fmt.Sprintf("%s: %s (%d items)", target.facet, target.req.Value, totalAccepted))
		}

		if jobPath := strings.TrimSpace(page.Job); jobPath != "" {
			beforeCount, _ := c.items.CountKindItems(jobCtx, rec.PluginID, rec.KindKey)
			stepAdded := 0
			contErr := run.RunContinuation(jobCtx, jobPath, body, func(stepBody json.RawMessage) error {
				var stepItems struct {
					Items []core.KindItem `json:"items"`
				}
				if json.Unmarshal(stepBody, &stepItems) == nil && len(stepItems.Items) > 0 && totalAccepted < MaxKindExpandItems {
					rem := MaxKindExpandItems - totalAccepted
					batch := stepItems.Items
					if len(batch) > rem {
						batch = batch[:rem]
					}
					acc, add, uErr := upsertSlice(batch)
					if uErr != nil {
						return uErr
					}
					totalAccepted += acc
					totalAdded += add
					stepAdded += add
					c.items.BumpGeneration()
					firstPage()
				}
				return nil
			})
			cntCtx, cntCancel := context.WithTimeout(context.Background(), 5*time.Second)
			afterCount, _ := c.items.CountKindItems(cntCtx, rec.PluginID, rec.KindKey)
			cntCancel()
			if hostAdded := afterCount - beforeCount - stepAdded; hostAdded > 0 {
				totalAdded += hostAdded
			}
			if contErr != nil {
				finish(pluginitems.ExpansionStateError, totalAdded, contErr.Error())
				return contErr
			}
			finish(pluginitems.ExpansionStateDone, totalAdded, "")
			return nil
		}

		next := page.EffectiveCursor()
		if next == "" || len(page.Items) == 0 || seenCursor[next] || totalAccepted >= MaxKindExpandItems {
			break
		}
		seenCursor[next] = true
		cursor = next
	}

	finish(pluginitems.ExpansionStateDone, totalAdded, "")
	return nil
}

func containerExpandDedupKey(pluginID, kindKey string, itemRef core.ItemRef) string {
	return pluginID + "\x00" + kindKey + "\x00" + containerExpandFacet + "\x00" + string(itemRef)
}

// maybeExpandContainer checks whether an opened container at a level declared
// in browse.expand.levels needs on-demand expansion via
// POST /ether/v1/kinds/{kind}/expand ({parentId, level, cursor?}).
//
// It triggers only when no fresh expansion exists within ttlHours OR the
// container currently has 0 stored direct children, unless a recent failure
// recorded on plugin_facet_expansions is still within expansionErrorCooldown.
// Concurrent opens of the same (plugin, kind, itemRef) share one single-flight
// expand job via Manager.StartExpandJob. Callers wait at most the remaining
// firstOpenWait window from the job's start (clamped by ctx.Deadline()) for
// page 1 to upsert, while later pages continue in the background through the
// expand job.
//
// Returns (didExpand, timedOut): didExpand is true if an expand job was
// launched or joined (so Item callers should re-read the container for any
// self-refresh), and timedOut is true when page 1 did not finish within the
// bounded wait.
func (c *KindCatalog) maybeExpandContainer(ctx context.Context, rec pluginitems.KindRecord, lib core.Library, container core.KindItem) (bool, bool) {
	return c.expandContainer(ctx, rec, lib, container, true)
}

// expandContainer starts (or joins) a container's expansion. A viewer's open
// (interactive) may use one slot beyond the background pool; when even that
// is busy the expansion is queued and retried, never dropped.
func (c *KindCatalog) expandContainer(ctx context.Context, rec pluginitems.KindRecord, lib core.Library, container core.KindItem, interactive bool) (bool, bool) {
	if c == nil || c.items == nil || c.mgr == nil || core.NoContainerExpand(ctx) {
		return false, false
	}
	if !rec.Decl.Browse.Expand.ExpandsLevel(container.Level) {
		return false, false
	}
	if !c.IsPluginLibraryActive(ctx, lib) {
		return false, false
	}

	ttl := time.Duration(rec.Decl.Browse.Expand.EffectiveTTLHours()) * time.Hour
	errCooldown := expansionErrorCooldown
	if ttl > 0 && ttl < errCooldown {
		errCooldown = ttl
	}
	routePath := "/ether/v1/kinds/" + rec.KindKey + "/expand"
	dedupKey := containerExpandDedupKey(rec.PluginID, rec.KindKey, container.Ref)

	expandLaunchMu.Lock()
	if c.mgr.IsExpandJobRunning(rec.PluginID, dedupKey) {
		var ws *expandWaitState
		if v, ok := expandReady.Load(dedupKey); ok {
			ws, _ = v.(*expandWaitState)
		}
		expandLaunchMu.Unlock()
		if ws != nil {
			return true, c.waitFirstOpen(ctx, ws)
		}
		return true, false
	}
	if ctx.Err() != nil {
		expandLaunchMu.Unlock()
		return false, false
	}
	now := c.items.Now()
	exp, hasExp, expErr := c.items.GetFacetExpansion(ctx, rec.PluginID, rec.KindKey, containerExpandFacet, container.ID)
	if expErr == nil && hasExp {
		if exp.IsInErrorCooldown(now, errCooldown) {
			expandLaunchMu.Unlock()
			return false, false
		}
		if exp.IsFresh(now, ttl) {
			hasChildren, _, childErr := c.items.ChildrenFreshness(ctx, lib.ID, container.Ref)
			if childErr == nil && hasChildren {
				expandLaunchMu.Unlock()
				return false, false
			}
		}
	}

	ready := make(chan struct{})
	firstPage := make(chan struct{})
	var firstOnce sync.Once
	signal := func() { firstOnce.Do(func() { close(firstPage) }) }

	started, startErr := c.mgr.StartExpandJobPriority(rec.PluginID, routePath, dedupKey, interactive, func(jobCtx context.Context, run *plugins.ExpandJobRunner) error {
		defer func() {
			expandReady.Delete(dedupKey)
			signal()
			c.kickDeferredExpansions()
		}()
		<-ready
		return c.runContainerExpansion(jobCtx, run, rec, lib.ID, routePath, container, signal)
	})
	if startErr == nil && !started {
		var ws *expandWaitState
		if v, ok := expandReady.Load(dedupKey); ok {
			ws, _ = v.(*expandWaitState)
		}
		close(ready)
		expandLaunchMu.Unlock()
		if ws != nil {
			return true, c.waitFirstOpen(ctx, ws)
		}
		return true, false
	}
	if startErr != nil {
		close(ready)
		expandLaunchMu.Unlock()
		c.log.Debug("container expansion deferred", "plugin", rec.PluginID, "kind", rec.KindKey, "container", container.ID, "level", container.Level, "err", startErr)
		c.deferExpansion(dedupKey, rec, lib, container)
		return false, false
	}
	markCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = c.items.MarkFacetExpansionRunning(markCtx, rec.PluginID, rec.KindKey, lib.ID, containerExpandFacet, container.ID)
	cancel()
	ws := &expandWaitState{firstPage: firstPage, startedAt: time.Now()}
	expandReady.Store(dedupKey, ws)
	close(ready)
	expandLaunchMu.Unlock()

	return true, c.waitFirstOpen(ctx, ws)
}

func (c *KindCatalog) waitFirstOpen(ctx context.Context, ws *expandWaitState) bool {
	if ws == nil || ws.firstPage == nil {
		return false
	}
	select {
	case <-ws.firstPage:
		return false
	default:
	}
	if firstOpenWait <= 0 {
		return true
	}
	wait := firstOpenWait
	if !ws.startedAt.IsZero() {
		wait -= time.Since(ws.startedAt)
		if wait <= 0 {
			return true
		}
	}
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl); rem <= 0 {
			return true
		} else if rem < wait {
			wait = rem
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ws.firstPage:
		return false
	case <-timer.C:
		return true
	case <-ctx.Done():
		return true
	}
}

func (c *KindCatalog) validateContainerExpandItems(ctx context.Context, rec pluginitems.KindRecord, container core.KindItem, knownAncestors map[string]string, items []core.KindItem) ([]core.KindItem, error) {
	_, containerAt, _ := rec.Decl.Level(container.Level)
	out := make([]core.KindItem, len(items))
	batchItems := make(map[string]core.KindItem, len(items))
	for i, it := range items {
		if it.ID == container.ID {
			if it.Level == "" {
				it.Level = container.Level
			}
			if it.Level != container.Level {
				return nil, fmt.Errorf("%w: container self-refresh %q: expected level %q, got %q",
					pluginitems.ErrInvalidItem, it.ID, container.Level, it.Level)
			}
			// Refresh the container's own fields in place while keeping its
			// stored parent: a collaboration album stored under a featured
			// artist may return its primary artist as parentId (which might
			// not even be stored). Never reparent out of the opened parent or
			// fail the expansion and drop its children.
			if it.ParentID != container.ParentID && (container.ParentID != "" || containerAt == 0) {
				if it.ParentID != "" {
					c.log.Debug("container self-refresh kept stored parent",
						"plugin", rec.PluginID, "kind", rec.KindKey, "container", container.ID,
						"stored_parent", container.ParentID, "incoming_parent", it.ParentID)
				}
				it.ParentID = container.ParentID
			}
			out[i] = it
			continue
		}
		_, at, ok := rec.Decl.Level(it.Level)
		if !ok {
			return nil, fmt.Errorf("%w: item %q: undeclared level %q", pluginitems.ErrInvalidItem, it.ID, it.Level)
		}
		if at == 0 {
			return nil, fmt.Errorf("%w: item %q: root-level item cannot be returned when expanding container %q",
				pluginitems.ErrInvalidItem, it.ID, container.ID)
		}
		if it.ParentID == "" {
			return nil, fmt.Errorf("%w: item %q: level %q needs a parentId", pluginitems.ErrInvalidItem, it.ID, it.Level)
		}
		batchItems[it.ID] = it
		out[i] = it
	}

	maxHops := max(len(rec.Decl.Levels), 1)
	dbParents := make(map[string]core.KindItem)
	for _, it := range out {
		if it.ID == container.ID {
			continue
		}
		curID := it.ID
		curLevel := it.Level
		parentID := it.ParentID
		visited := map[string]bool{curID: true}
		reached := false
		for hops := 0; hops < maxHops && parentID != ""; hops++ {
			if visited[parentID] {
				return nil, fmt.Errorf("%w: item %q: cyclic parentId chain at %q", pluginitems.ErrInvalidItem, it.ID, parentID)
			}
			visited[parentID] = true
			expectedParent := rec.Decl.LevelParent(curLevel)
			if expectedParent == "" {
				return nil, fmt.Errorf("%w: item %q: level %q has no declared parent level", pluginitems.ErrInvalidItem, curID, curLevel)
			}
			if pLevel, ok := knownAncestors[parentID]; ok {
				if pLevel != expectedParent {
					return nil, fmt.Errorf("%w: item %q: level %q requires parent level %q, got %q",
						pluginitems.ErrInvalidItem, curID, curLevel, expectedParent, pLevel)
				}
				reached = true
				break
			}
			if pItem, ok := batchItems[parentID]; ok {
				if pItem.Level != expectedParent {
					return nil, fmt.Errorf("%w: item %q: level %q requires parent level %q, got %q",
						pluginitems.ErrInvalidItem, curID, curLevel, expectedParent, pItem.Level)
				}
				curID = pItem.ID
				curLevel = pItem.Level
				parentID = pItem.ParentID
				continue
			}
			stored, cached := dbParents[parentID]
			if !cached {
				var err error
				stored, _, err = c.items.GetByExternal(ctx, rec.PluginID, rec.KindKey, parentID)
				if err != nil {
					return nil, fmt.Errorf("%w: item %q: parentId %q not found", pluginitems.ErrInvalidItem, curID, parentID)
				}
				dbParents[parentID] = stored
			}
			if stored.Level != expectedParent {
				return nil, fmt.Errorf("%w: item %q: level %q requires parent level %q, got %q",
					pluginitems.ErrInvalidItem, curID, curLevel, expectedParent, stored.Level)
			}
			curID = stored.ID
			curLevel = stored.Level
			parentID = stored.ParentID
		}
		if !reached {
			return nil, fmt.Errorf("%w: item %q: parentId %q is outside container %q",
				pluginitems.ErrInvalidItem, it.ID, it.ParentID, container.ID)
		}
	}

	for _, it := range out {
		if it.ID != container.ID && it.ID != "" {
			knownAncestors[it.ID] = it.Level
		}
	}
	return out, nil
}

func (c *KindCatalog) runContainerExpansion(jobCtx context.Context, run *plugins.ExpandJobRunner, rec pluginitems.KindRecord, libID core.LibraryID, routePath string, container core.KindItem, firstPage func()) error {
	var (
		pages              int
		callTime, saveTime time.Duration
		began              = time.Now()
	)
	finish := func(state string, added int, errMsg string) {
		c.log.Info("container expansion finished", "plugin", rec.PluginID, "kind", rec.KindKey, "container", container.ID, "level", container.Level,
			"state", state, "pages", pages, "added", added, "took", time.Since(began), "plugin_time", callTime, "save_time", saveTime, "err", errMsg)
		finCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.items.FinishFacetExpansion(finCtx, rec.PluginID, rec.KindKey, libID, containerExpandFacet, container.ID, state, added, errMsg)
		c.items.BumpGeneration()
	}

	knownAncestors := map[string]string{
		container.ID: container.Level,
	}

	upsertSlice := func(items []core.KindItem) (accepted int, added int, err error) {
		validated, err := c.validateContainerExpandItems(jobCtx, rec, container, knownAncestors, items)
		if err != nil {
			return 0, 0, err
		}
		for start := 0; start < len(validated); start += pluginitems.MaxUpsertBatch {
			end := min(start+pluginitems.MaxUpsertBatch, len(validated))
			acc, add, uErr := c.items.UpsertPinnedItemsWithAdded(jobCtx, rec.PluginID, rec.KindKey, validated[start:end])
			if uErr != nil {
				return accepted, added, uErr
			}
			accepted += acc
			added += add
		}
		return accepted, added, nil
	}

	var (
		cursor        string
		totalAccepted int
		totalAdded    int
		seenCursor    = make(map[string]bool)
	)

	for pageIdx := 0; pageIdx < MaxKindExpandPages && totalAccepted < MaxKindExpandItems; pageIdx++ {
		req := core.KindExpandRequest{
			ParentID: container.ID,
			Level:    container.Level,
			Cursor:   cursor,
		}
		body, err := json.Marshal(req)
		if err != nil {
			finish(pluginitems.ExpansionStateError, totalAdded, err.Error())
			return err
		}

		callStart := time.Now()
		resp, err := run.Call(plugins.WithBackgroundCall(jobCtx), plugins.Request{
			PluginID: rec.PluginID,
			Method:   http.MethodPost,
			Path:     routePath,
			Headers:  map[string]string{"content-type": "application/json"},
			Body:     body,
		})
		callTime += time.Since(callStart)
		pages++
		if err != nil {
			finish(pluginitems.ExpansionStateError, totalAdded, err.Error())
			return err
		}
		if resp.Status == http.StatusNotFound || resp.Status == http.StatusNotImplemented {
			finish(pluginitems.ExpansionStateDone, totalAdded, "")
			return nil
		}
		if resp.Status < 200 || resp.Status >= 300 {
			callErr := fmt.Errorf("expand %s/%s (%s=%s): status %d", rec.PluginID, rec.KindKey, container.Level, container.ID, resp.Status)
			finish(pluginitems.ExpansionStateError, totalAdded, callErr.Error())
			return callErr
		}

		var page core.KindExpandResponse
		if len(resp.Body) > 0 {
			if err := json.Unmarshal(resp.Body, &page); err != nil {
				finish(pluginitems.ExpansionStateError, totalAdded, err.Error())
				return err
			}
		}

		jobPath := strings.TrimSpace(page.Job)
		next := page.EffectiveCursor()
		hasMore := jobPath != "" || (next != "" && len(page.Items) > 0 && !seenCursor[next] && totalAccepted+len(page.Items) < MaxKindExpandItems)

		if len(page.Items) > 0 {
			remaining := MaxKindExpandItems - totalAccepted
			batch := page.Items
			if len(batch) > remaining {
				batch = batch[:remaining]
			}
			saveStart := time.Now()
			acc, add, uErr := upsertSlice(batch)
			saveTime += time.Since(saveStart)
			if uErr != nil {
				finish(pluginitems.ExpansionStateError, totalAdded, uErr.Error())
				return uErr
			}
			totalAccepted += acc
			totalAdded += add
			c.items.BumpGeneration()
			run.SetProgress(fmt.Sprintf("%s: %s (%d items)", container.Level, container.ID, totalAccepted))
			if hasMore {
				firstPage()
			}
		}

		if jobPath != "" {
			beforeCount, _ := c.items.CountKindItems(jobCtx, rec.PluginID, rec.KindKey)
			stepAdded := 0
			contErr := run.RunContinuation(jobCtx, jobPath, body, func(stepBody json.RawMessage) error {
				var stepItems struct {
					Items []core.KindItem `json:"items"`
				}
				if json.Unmarshal(stepBody, &stepItems) == nil && len(stepItems.Items) > 0 && totalAccepted < MaxKindExpandItems {
					rem := MaxKindExpandItems - totalAccepted
					batch := stepItems.Items
					if len(batch) > rem {
						batch = batch[:rem]
					}
					acc, add, uErr := upsertSlice(batch)
					if uErr != nil {
						return uErr
					}
					totalAccepted += acc
					totalAdded += add
					stepAdded += add
					c.items.BumpGeneration()
					firstPage()
				}
				return nil
			})
			cntCtx, cntCancel := context.WithTimeout(context.Background(), 5*time.Second)
			afterCount, _ := c.items.CountKindItems(cntCtx, rec.PluginID, rec.KindKey)
			cntCancel()
			if hostAdded := afterCount - beforeCount - stepAdded; hostAdded > 0 {
				totalAdded += hostAdded
			}
			if contErr != nil {
				finish(pluginitems.ExpansionStateError, totalAdded, contErr.Error())
				return contErr
			}
			finish(pluginitems.ExpansionStateDone, totalAdded, "")
			return nil
		}

		if next == "" || len(page.Items) == 0 || seenCursor[next] || totalAccepted >= MaxKindExpandItems {
			break
		}
		seenCursor[next] = true
		cursor = next
	}

	finish(pluginitems.ExpansionStateDone, totalAdded, "")
	return nil
}

// maybeHydrateContainer checks in O(1) whether an opened container (with
// ChildCount > 0) has missing or stale children past the kind's expand TTL.
// When hydration is needed, it launches a per-container deduplicated fetch
// via POST /ether/v1/kinds/{kind}/expand with {parentId, level} (falling back
// to POST /ether/v1/kinds/{kind}/browse with parentId), waits up to
// containerHydrateWait (~1.8s, clamped by ctx) for the first page to commit,
// and lets remaining pages finish in the background.
func (c *KindCatalog) maybeHydrateContainer(ctx context.Context, rec pluginitems.KindRecord, lib core.Library, parentItem core.KindItem, levelIdx int) {
	if c == nil || c.items == nil || c.mgr == nil {
		return
	}
	if parentItem.ChildCount <= 0 || levelIdx < 0 || levelIdx >= len(rec.Decl.Levels) {
		return
	}
	isContainer := rec.Decl.HasChildLevels(parentItem.Level) || rec.Decl.Levels[levelIdx].Container
	if !isContainer || !c.IsPluginLibraryActive(ctx, lib) {
		return
	}

	ttlHours := plugins.DefaultKindExpandTTLHours
	if rec.Decl.Browse.Expand != nil {
		ttlHours = rec.Decl.Browse.Expand.EffectiveTTLHours()
	}
	ttl := time.Duration(ttlHours) * time.Hour
	errCooldown := expansionErrorCooldown
	if ttl > 0 && ttl < errCooldown {
		errCooldown = ttl
	}
	now := c.items.Now()

	sfKey := rec.PluginID + "\x00" + rec.KindKey + "\x00" + parentItem.ID

	c.hydrateMu.Lock()
	if call, inflight := c.hydrateInflight[sfKey]; inflight {
		c.hydrateMu.Unlock()
		waitContainerHydrate(ctx, call.firstPage, call.startedAt)
		return
	}
	// Check durability + stored child freshness under the lock only after
	// verifying no inflight hydration is running, avoiding duplicate launches.
	exp, hasExp, expErr := c.items.GetFacetExpansion(ctx, rec.PluginID, rec.KindKey, containerExpandFacet, parentItem.ID)
	if expErr == nil && hasExp && (exp.IsFresh(now, ttl) || exp.IsInErrorCooldown(now, errCooldown)) {
		c.hydrateMu.Unlock()
		return
	}
	childCount, sampleAt, childErr := c.items.ChildrenCountAndFreshness(ctx, lib.ID, parentItem.Ref)
	if childErr == nil && childCount > 0 && (parentItem.ChildCount <= 0 || childCount >= parentItem.ChildCount) && !sampleAt.IsZero() && now.Sub(sampleAt) < ttl {
		c.hydrateMu.Unlock()
		return
	}

	call := &containerHydrateCall{firstPage: make(chan struct{}), startedAt: time.Now()}
	if c.hydrateInflight == nil {
		c.hydrateInflight = make(map[string]*containerHydrateCall)
	}
	if c.hydrateSems == nil {
		c.hydrateSems = make(map[string]chan struct{})
	}
	c.hydrateInflight[sfKey] = call
	sem := c.hydrateSems[rec.PluginID]
	if sem == nil {
		sem = make(chan struct{}, maxContainerHydratePerPlugin)
		c.hydrateSems[rec.PluginID] = sem
	}
	c.hydrateMu.Unlock()

	go c.runContainerHydration(sfKey, sem, rec, lib.ID, parentItem, levelIdx, call)
	waitContainerHydrate(ctx, call.firstPage, call.startedAt)
}

func waitContainerHydrate(ctx context.Context, firstPage <-chan struct{}, startedAt time.Time) {
	wait := containerHydrateWait
	if wait <= 0 {
		return
	}
	if !startedAt.IsZero() {
		wait -= time.Since(startedAt)
		if wait <= 0 {
			return
		}
	}
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl); rem <= 0 {
			return
		} else if rem < wait {
			wait = rem
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-firstPage:
	case <-timer.C:
	case <-ctx.Done():
	}
}

func (c *KindCatalog) runContainerHydration(
	sfKey string,
	sem chan struct{},
	rec pluginitems.KindRecord,
	libID core.LibraryID,
	parentItem core.KindItem,
	levelIdx int,
	call *containerHydrateCall,
) {
	var firstOnce sync.Once
	signalFirst := func() { firstOnce.Do(func() { close(call.firstPage) }) }
	defer func() {
		signalFirst()
		c.hydrateMu.Lock()
		delete(c.hydrateInflight, sfKey)
		c.hydrateMu.Unlock()
	}()

	parentCtx := c.syncCtx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	bgCtx, cancel := context.WithTimeout(parentCtx, containerHydrateBGTimeout)
	defer cancel()

	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-bgCtx.Done():
		return
	}

	markCtx, markCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = c.items.MarkFacetExpansionRunning(markCtx, rec.PluginID, rec.KindKey, libID, containerExpandFacet, parentItem.ID)
	markCancel()

	finish := func(state string, added int, errMsg string) {
		finCtx, finCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer finCancel()
		_ = c.items.FinishFacetExpansion(finCtx, rec.PluginID, rec.KindKey, libID, containerExpandFacet, parentItem.ID, state, added, errMsg)
		c.items.BumpGeneration()
	}

	childLevels := make([]string, 0, 2)
	for _, lv := range rec.Decl.ChildLevels(parentItem.Level) {
		childLevels = append(childLevels, lv.Key)
	}
	if len(childLevels) == 0 {
		childLevels = []string{""}
	}
	expandPath := "/ether/v1/kinds/" + rec.KindKey + "/expand"
	browsePath := "/ether/v1/kinds/" + rec.KindKey + "/browse"

	upsertSlice := func(defaultLevel string, items []core.KindItem) (accepted int, added int, err error) {
		normalized := make([]core.KindItem, len(items))
		copy(normalized, items)
		for i := range normalized {
			if normalized[i].ParentID == "" {
				normalized[i].ParentID = parentItem.ID
			}
			if normalized[i].Level == "" && defaultLevel != "" {
				normalized[i].Level = defaultLevel
			}
		}
		for start := 0; start < len(normalized); start += pluginitems.MaxUpsertBatch {
			end := min(start+pluginitems.MaxUpsertBatch, len(normalized))
			acc, add, uErr := c.items.UpsertPinnedItemsWithAdded(bgCtx, rec.PluginID, rec.KindKey, normalized[start:end])
			if uErr != nil {
				return accepted, added, uErr
			}
			accepted += acc
			added += add
		}
		return accepted, added, nil
	}

	var (
		totalAccepted int
		totalAdded    int
	)

	type levelState struct {
		childLevel string
		cursor     string
		useBrowse  bool
		done       bool
		seenCursor map[string]bool
	}
	states := make([]*levelState, len(childLevels))
	for i, cl := range childLevels {
		states[i] = &levelState{
			childLevel: cl,
			seenCursor: make(map[string]bool),
		}
	}

	callExpand := func(childLevel, cur string) (items []core.KindItem, next string, fallback bool, err error) {
		body, mErr := json.Marshal(core.KindExpandRequest{
			ParentID: parentItem.ID,
			Level:    childLevel,
			Cursor:   cur,
		})
		if mErr != nil {
			return nil, "", false, mErr
		}
		resp, cErr := c.mgr.Call(plugins.WithBackgroundCall(bgCtx), rec.PluginID, plugins.Request{
			PluginID: rec.PluginID,
			Method:   http.MethodPost,
			Path:     expandPath,
			Headers:  map[string]string{"content-type": "application/json"},
			Body:     body,
		})
		if cErr != nil {
			return nil, "", false, cErr
		}
		if resp.Status == http.StatusNotFound || resp.Status == http.StatusNotImplemented || resp.Status == http.StatusBadRequest {
			return nil, "", true, nil
		}
		if resp.Status < 200 || resp.Status >= 300 {
			return nil, "", false, fmt.Errorf("container expand %s/%s (%s): status %d", rec.PluginID, rec.KindKey, parentItem.ID, resp.Status)
		}
		var page core.KindExpandResponse
		if len(resp.Body) > 0 {
			if uErr := json.Unmarshal(resp.Body, &page); uErr != nil {
				return nil, "", false, uErr
			}
		}
		return page.Items, page.EffectiveCursor(), false, nil
	}

	callBrowse := func(childLevel, cur string) (items []core.KindItem, next string, unsupported bool, err error) {
		body, mErr := json.Marshal(core.KindBrowseRequest{
			ParentID: parentItem.ID,
			Level:    childLevel,
			Cursor:   cur,
			Limit:    pluginitems.DefaultWalkPageLimit,
		})
		if mErr != nil {
			return nil, "", false, mErr
		}
		resp, cErr := c.mgr.Call(plugins.WithBackgroundCall(bgCtx), rec.PluginID, plugins.Request{
			PluginID: rec.PluginID,
			Method:   http.MethodPost,
			Path:     browsePath,
			Headers:  map[string]string{"content-type": "application/json"},
			Body:     body,
		})
		if cErr != nil {
			return nil, "", false, cErr
		}
		if resp.Status == http.StatusNotFound || resp.Status == http.StatusNotImplemented {
			return nil, "", true, nil
		}
		if resp.Status < 200 || resp.Status >= 300 {
			return nil, "", false, fmt.Errorf("container browse %s/%s (%s): status %d", rec.PluginID, rec.KindKey, parentItem.ID, resp.Status)
		}
		var page core.KindPage
		if len(resp.Body) > 0 {
			if uErr := json.Unmarshal(resp.Body, &page); uErr != nil {
				return nil, "", false, uErr
			}
		}
		return page.Items, strings.TrimSpace(page.NextCursor), false, nil
	}

	fetchOnePage := func(st *levelState, pageIdx int) (abort bool) {
		var (
			pageItems   []core.KindItem
			next        string
			fallback    bool
			unsupported bool
			err         error
		)
		if !st.useBrowse {
			pageItems, next, fallback, err = callExpand(st.childLevel, st.cursor)
			if err != nil {
				finish(pluginitems.ExpansionStateError, totalAdded, err.Error())
				return true
			}
			// Fall back to /browse if /expand doesn't support container hydration
			// (404/501/400) or returned an empty first page for a non-empty container.
			if fallback || (pageIdx == 0 && len(pageItems) == 0 && next == "") {
				st.useBrowse = true
				pageItems, next, unsupported, err = callBrowse(st.childLevel, st.cursor)
				if err != nil {
					finish(pluginitems.ExpansionStateError, totalAdded, err.Error())
					return true
				}
				if unsupported {
					st.done = true
					return false
				}
			}
		} else {
			pageItems, next, unsupported, err = callBrowse(st.childLevel, st.cursor)
			if err != nil {
				finish(pluginitems.ExpansionStateError, totalAdded, err.Error())
				return true
			}
			if unsupported {
				st.done = true
				return false
			}
		}

		if len(pageItems) > 0 {
			remaining := MaxKindExpandItems - totalAccepted
			batch := pageItems
			if len(batch) > remaining {
				batch = batch[:remaining]
			}
			acc, add, uErr := upsertSlice(st.childLevel, batch)
			if uErr != nil {
				finish(pluginitems.ExpansionStateError, totalAdded, uErr.Error())
				return true
			}
			totalAccepted += acc
			totalAdded += add
			c.items.BumpGeneration()
		}

		if next == "" || len(pageItems) == 0 || st.seenCursor[next] || totalAccepted >= MaxKindExpandItems {
			st.done = true
			return false
		}
		st.seenCursor[next] = true
		st.cursor = next
		return false
	}

	// Phase 1: commit the first page of every declared child level before
	// unblocking the caller so branching containers hydrate all branches (and
	// populate every branch in plugin_item_parent_tally) on first open.
	for _, st := range states {
		if totalAccepted >= MaxKindExpandItems {
			break
		}
		if fetchOnePage(st, 0) {
			return
		}
	}
	signalFirst()

	// Phase 2: finish remaining pages for each branch in the background.
	for _, st := range states {
		for pageIdx := 1; pageIdx < MaxKindExpandPages && totalAccepted < MaxKindExpandItems && !st.done; pageIdx++ {
			if fetchOnePage(st, pageIdx) {
				return
			}
		}
	}

	finish(pluginitems.ExpansionStateDone, totalAdded, "")
}

// FillChildren opens a container for a frontend adapter's viewer
// (kinds.expand) exactly as opening it in an Ether app does: an expanding
// level starts or joins its expansion on the interactive path and waits as
// long as an open does; any other level hydrates missing children. pending
// reports children still arriving.
func (c *KindCatalog) FillChildren(ctx context.Context, lib core.Library, externalID string) (bool, error) {
	if c == nil || c.items == nil {
		return false, plugins.ErrUnavailable
	}
	rec, err := c.items.GetKindByLibrary(ctx, lib.ID)
	if err != nil {
		return false, err
	}
	item, libID, err := c.items.GetByExternal(ctx, lib.PluginID, lib.KindKey, externalID)
	if err != nil {
		return false, err
	}
	if libID != lib.ID {
		return false, core.ErrNotFound
	}
	if core.NoContainerExpand(ctx) {
		return false, nil
	}
	if rec.Decl.Browse.Expand.ExpandsLevel(item.Level) {
		_, pending := c.maybeExpandContainer(ctx, rec, lib, item)
		return pending, nil
	}
	if _, levelIdx, ok := rec.Decl.Level(item.Level); ok {
		c.maybeHydrateContainer(ctx, rec, lib, item, levelIdx)
	}
	return false, nil
}

// SearchCount runs an app's search of lib for a frontend adapter's viewer
// (kinds.search): local matches plus the kind's bounded live search, whose
// finds Ether stores. It returns how many matches the library holds.
func (c *KindCatalog) SearchCount(ctx context.Context, lib core.Library, query string, limit int) (int, error) {
	titles, err := c.Search(ctx, lib, query, frontend.ItemQuery{}, limit)
	return len(titles), err
}
