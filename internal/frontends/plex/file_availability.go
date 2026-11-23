package plex

import (
	"context"
	"net/http"
	"sync"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

type fileAvailabilityChecker interface {
	CheckFileAvailability(context.Context, *frontend.Viewer, frontend.RatingKey, frontend.ClientCaps, string) (bool, error)
}

// fileCheckParallel bounds how many checks run at once. The configured
// plex.fileCheckTimeout bounds the whole response. The playback manager caps file
// checks process-wide (its fileCheckSlots) and answers a busy slot as
// unchecked, so a burst of requests cannot fan out further.
const fileCheckParallel = 4

// projectFileAvailability answers checkFiles / asyncCheckFiles /
// checkFileAvailability: every Part carries exists and accessible. Versions
// are checked concurrently within one shared budget. Only a verdict (nil
// error) can mark a Part missing; any error is no verdict (budget spent,
// check slots busy, nothing found yet, another release preferred) and the
// Part keeps the state it is offered with, available. Plex apps hide Play on
// accessible="0", which would also stop the playback-time search that finds
// a release.
func (s *Server) projectFileAvailability(r *http.Request, mc *model.MediaContainer) {
	q := r.URL.Query()
	if !metadataOptionOn(q.Get("checkFiles")) && !metadataOptionOn(q.Get("asyncCheckFiles")) && !metadataOptionOn(q.Get("checkFileAvailability")) {
		return
	}
	checker, ok := s.playback().(fileAvailabilityChecker)
	if !ok {
		return
	}
	type target struct {
		key     frontend.RatingKey
		release string
	}
	parts := map[target][]*model.Part{}
	var order []target
	for i := range mc.Metadata {
		md := &mc.Metadata[i]
		for j := range md.Media {
			for k := range md.Media[j].Part {
				part := &md.Media[j].Part[k]
				key, release, found := s.partTitle(part.ID)
				if !found || key.IsPluginKey() {
					continue
				}
				t := target{key, release}
				if _, seen := parts[t]; !seen {
					order = append(order, t)
				}
				parts[t] = append(parts[t], part)
			}
		}
	}
	if len(order) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg().Plex.FileCheckTimeoutOrDefault())
	defer cancel()
	pr := FromContext(ctx)
	if len(mc.Metadata) > 1 || libraryToolClient(pr) {
		// Plex apps ask checkFiles for the one detail page a viewer opened.
		// A batched lookup (/library/metadata/1,2,…?checkFiles=1), or any
		// lookup from a monitoring or library tool, reconciles a catalogue
		// instead (Tracearr, for instance, checks 100 rating keys per request
		// across the whole library). A warm-up search per title would spend
		// each indexer's background share in minutes, so these get local
		// verdicts only: a blocked release or remembered source still
		// answers, anything else is no verdict and the Part stays available.
		ctx = frontend.WithLocalResponse(ctx)
	}
	caps := s.clientCaps(pr)
	verdicts := make([]*bool, len(order)) // nil: no verdict
	slots := make(chan struct{}, fileCheckParallel)
	var wg sync.WaitGroup
dispatch:
	for i, t := range order {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break dispatch
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			live, err := checker.CheckFileAvailability(ctx, pr.User, t.key, caps, t.release)
			if err != nil {
				return
			}
			verdicts[i] = &live
		}()
	}
	wg.Wait()
	for i, t := range order {
		live := true
		if verdicts[i] != nil {
			live = *verdicts[i]
		}
		anyUnprobed := false
		for _, part := range parts[t] {
			// Plezy treats exists+accessible (with Stream) as proof
			// its cached row is complete and reuses it at Play. A
			// part rendered from the pre-probe estimate is not
			// complete, so leave the flags unset — PMS's shape
			// without a file check — and Plezy re-fetches at Play,
			// where plezyFirstPlay waits on the probe (#618, #678). A
			// missing verdict still stamps, so Play stays hidden
			// for a Part proven gone.
			if live && plezyClient(pr) && !s.plezyPartProbed(ctx, pr, part) {
				anyUnprobed = true
				continue
			}
			part.Exists, part.Accessible = model.Ptr(live), model.Ptr(live)
		}
		if anyUnprobed && pr != nil && pr.User != nil && t.key != 0 {
			s.plezyReasks.armIfIdle(plezyReaskKey{userID: pr.User.ID, clientID: pr.ClientID, key: t.key}, s.now())
		}
	}
}
