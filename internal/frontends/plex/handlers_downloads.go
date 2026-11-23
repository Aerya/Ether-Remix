package plex

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/rangefile"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// Offline downloads (PMS Download Queue API). Each queue belongs to one
// (user, client); items move deciding -> waiting -> processing ->
// available as the title is resolved with caps.Download set and its
// streams are copied to a temporary faststart MP4 by SessionRuntime.Downloads().
// Queue state is persisted asynchronously when a database is wired. Completed
// files remain subject to the configured retention and disk budget.

const (
	downloadDeciding   = "deciding"
	downloadWaiting    = "waiting"
	downloadProcessing = "processing"
	downloadAvailable  = "available"
	downloadError      = "error"
	downloadExpired    = "expired"
	downloadDone       = "done" // queue-level only
)

// downloadSubscribeFeature is the library provider's download capability
// for Plex's desktop apps (Plex for Mac/Windows, Plex HTPC). Their web
// client (Plex Web 4.163 showSyncDownload) shows Download only when the
// item's provider has features.subscribe.flavor == "download" and, for a
// library provider, the server's allowSync is true; it reads nothing else
// from this feature. The download itself is a media subscription created
// on the app's embedded server (state.embeddedServer, not this one), which
// then pulls the title from this server like any remote PMS client:
// metadata, universal decision, then the part or a start stream. No key
// is advertised because no client calls one for this flavor; the Live TV
// DVR keeps /media/subscriptions.
var downloadSubscribeFeature = model.Feature{Flavor: "download", Type: "subscribe"}

// downloadsEnabled reports whether the server can serve downloads at
// all: owner setting on, remuxer and playback wired.
func (s *Server) downloadsEnabled() bool {
	rt := s.sessionRuntime()
	return s.cfg().Downloads.Enabled && rt != nil && rt.Downloads() != nil && s.playback() != nil
}

// downloadsAllowed reports whether u may download (and so sees
// allowSync="1"): the owner always, friends only with the owner's
// AllowFriends setting unless the access policy decides per user.
func (s *Server) downloadsAllowed(u *frontend.User) bool {
	return frontend.DownloadsAllowed(u, s.downloadsEnabled(), s.cfg().Downloads.AllowFriends, s.policy())
}

// DownloadsAllowed exposes the same admission decision for informational
// frontend capability reporting; the download handler still enforces it.
func (s *Server) DownloadsAllowed(u *frontend.User) bool { return s.downloadsAllowed(u) }

func (s *Server) requireDownloads(w http.ResponseWriter, r *http.Request) bool {
	if !s.downloadsEnabled() {
		writeHTTPError(w, http.StatusNotFound)
		return false
	}
	if !s.downloadsAllowed(FromContext(r.Context()).User) {
		writeHTTPError(w, http.StatusForbidden)
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Store

type downloadItem = frontend.DownloadItem
type downloadQueue = frontend.DownloadQueue

func queueStatus(q *downloadQueue) string {
	seen := map[string]bool{}
	for _, it := range q.Items {
		seen[it.Status] = true
	}
	for _, st := range []string{downloadDeciding, downloadProcessing, downloadWaiting, downloadError} {
		if seen[st] {
			return st
		}
	}
	return downloadDone
}

// ---------------------------------------------------------------------------
// Wire models (components/schemas DownloadQueue, DownloadQueueItem,
// AddedQueueItem and DecisionResult of the PMS OpenAPI spec).

type downloadQueueModel struct {
	ID        int64  `plex:"id"`
	ItemCount int    `plex:"itemCount"`
	Status    string `plex:"status"`
}

type addedQueueItemModel struct {
	ID  int64  `plex:"id"`
	Key string `plex:"key"`
}

type decisionResultModel struct {
	AvailableBandwidth     int    `plex:"availableBandwidth,omitempty"`
	DirectPlayDecisionCode int    `plex:"directPlayDecisionCode"`
	DirectPlayDecisionText string `plex:"directPlayDecisionText"`
	GeneralDecisionCode    int    `plex:"generalDecisionCode"`
	GeneralDecisionText    string `plex:"generalDecisionText"`
	MDEDecisionCode        int    `plex:"mdeDecisionCode"`
	MDEDecisionText        string `plex:"mdeDecisionText"`
	TranscodeDecisionCode  int    `plex:"transcodeDecisionCode"`
	TranscodeDecisionText  string `plex:"transcodeDecisionText"`
}

type downloadQueueItemModel struct {
	ID       int64                `plex:"id"`
	QueueID  int64                `plex:"queueId"`
	Key      string               `plex:"key"`
	Status   string               `plex:"status"`
	Error    string               `plex:"error,omitempty"`
	Decision *decisionResultModel `plex:"DecisionResult,object"`
	Session  *model.TranscodeSess `plex:"TranscodeSession,object"`
}

type downloadContainer struct {
	Size       int    `plex:"size"`
	Identifier string `plex:"identifier,omitempty"`

	Queue []downloadQueueModel     `plex:"DownloadQueue"`
	Items []downloadQueueItemModel `plex:"DownloadQueueItem"`
	Added []addedQueueItemModel    `plex:"AddedQueueItems"`
}

func keyPath(k frontend.RatingKey) string { return "/library/metadata/" + k.String() }

// itemModel renders one queue item. Downloads are converted or remuxed to MP4.
func itemModel(it *downloadItem) downloadQueueItemModel {
	m := downloadQueueItemModel{
		ID: it.ID, QueueID: it.QueueID, Key: keyPath(it.Key), Status: it.Status, Error: it.Error,
	}
	dr := &decisionResultModel{AvailableBandwidth: it.Bandwidth}
	text := "Direct play not available; Conversion to MP4 OK."
	switch it.Status {
	case downloadError:
		dr.DirectPlayDecisionCode, dr.DirectPlayDecisionText = codeDirectPlayError, it.Error
		dr.GeneralDecisionCode, dr.GeneralDecisionText = codeGeneralError, it.Error
		dr.MDEDecisionCode, dr.MDEDecisionText = codeGeneralError, it.Error
		dr.TranscodeDecisionCode, dr.TranscodeDecisionText = codeTranscodeError, it.Error
	case downloadDeciding:
		dr.DirectPlayDecisionCode, dr.DirectPlayDecisionText = codeGeneralError, "Deciding."
		dr.GeneralDecisionCode, dr.GeneralDecisionText = codeGeneralError, "Deciding."
		dr.MDEDecisionCode, dr.MDEDecisionText = codeGeneralError, "Deciding."
		dr.TranscodeDecisionCode, dr.TranscodeDecisionText = codeGeneralError, "Deciding."
	default:
		dr.DirectPlayDecisionCode, dr.DirectPlayDecisionText = codeDirectPlayError, text
		dr.GeneralDecisionCode, dr.GeneralDecisionText = codeConversionOK, text
		dr.MDEDecisionCode, dr.MDEDecisionText = codeConversionOK, text
		dr.TranscodeDecisionCode, dr.TranscodeDecisionText = codeConversionOK, text
	}
	m.Decision = dr
	if it.Status != downloadDeciding {
		m.Session = &model.TranscodeSess{
			Key:              fmt.Sprintf("/downloadQueue/%d/item/%d", it.QueueID, it.ID),
			Complete:         it.Status == downloadAvailable,
			Progress:         it.Progress * 100,
			Duration:         ms(it.Duration),
			Context:          "static",
			SourceVideoCodec: it.VideoCodec,
			SourceAudioCodec: it.AudioCodec,
			VideoDecision:    "copy",
			AudioDecision:    "copy",
			Protocol:         "http",
			Container:        "mp4",
			VideoCodec:       it.VideoCodec,
			AudioCodec:       it.AudioCodec,
		}
	}
	return m
}

// ---------------------------------------------------------------------------
// Handlers

// downloadQueueParam resolves {queue} for the requesting user/client.
func (s *Server) downloadQueueParam(w http.ResponseWriter, r *http.Request) *downloadQueue {
	pr := FromContext(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "queue"), 10, 64)
	if err != nil || pr.User == nil {
		writeHTTPError(w, http.StatusNotFound)
		return nil
	}
	q := s.downloads.QueueByID(id, pr.User.ID, pr.ClientID)
	if q == nil {
		writeHTTPError(w, http.StatusNotFound)
		return nil
	}
	return q
}

func (s *Server) writeQueue(w http.ResponseWriter, r *http.Request, q *downloadQueue) {
	s.downloads.Lock()
	m := downloadQueueModel{ID: q.ID, ItemCount: len(q.Items), Status: queueStatus(q)}
	s.downloads.Unlock()
	s.write(w, r, &downloadContainer{Size: 1, Queue: []downloadQueueModel{m}})
}

// POST /downloadQueue — get or create this client+user's queue.
func (s *Server) handleDownloadQueueCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireDownloads(w, r) {
		return
	}
	pr := FromContext(r.Context())
	q := s.downloads.QueueFor(pr.User.ID, pr.ClientID, true)
	s.stageDownloads()
	s.writeQueue(w, r, q)
}

// GET /downloadQueue/{queue}
func (s *Server) handleDownloadQueue(w http.ResponseWriter, r *http.Request) {
	if !s.requireDownloads(w, r) {
		return
	}
	s.expireDownloads(s.now())
	if q := s.downloadQueueParam(w, r); q != nil {
		s.writeQueue(w, r, q)
	}
}

// parseDownloadKey accepts "/library/metadata/123[/children]" or a bare
// rating key.
func parseDownloadKey(s string) (frontend.RatingKey, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "metadata/"); i >= 0 {
		s = s[i+len("metadata/"):]
	}
	if i := strings.IndexAny(s, "/?"); i >= 0 {
		s = s[:i]
	}
	return frontend.ParseRatingKey(s)
}

// syncVideoQualities maps the legacy videoQuality index clients send to
// (max bitrate kbps, max height); the table is PMS's (see python-plexapi
// sync.VIDEO_QUALITIES).
var syncVideoQualities = []struct{ kbps, height int }{
	{64, 128}, {96, 128}, {208, 160}, {320, 240}, {720, 320}, {1500, 480},
	{2000, 720}, {3000, 720}, {4000, 720}, {8000, 1080}, {10000, 1080},
	{12000, 1080}, {20000, 1080},
}

// downloadCaps builds the caps an add request asked for.
func (s *Server) downloadCaps(r *http.Request, pr *Request) frontend.ClientCaps {
	caps := s.declaredCaps(pr)
	q := r.URL.Query()
	if v := q.Get("videoResolution"); v != "" {
		if _, h, ok := strings.Cut(v, "x"); ok {
			if n, err := strconv.Atoi(h); err == nil && n > 0 {
				caps.MaxHeight = n
			}
		}
	}
	for _, name := range []string{"maxVideoBitrate", "videoBitrate"} {
		if v := q.Get(name); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				caps.MaxBitrateKbps = n
				break
			}
		}
	}
	if caps.MaxBitrateKbps == 0 && q.Get("videoResolution") == "" {
		if v := q.Get("videoQuality"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n < len(syncVideoQualities) {
				caps.MaxBitrateKbps = syncVideoQualities[n].kbps
				caps.MaxHeight = syncVideoQualities[n].height
			}
		}
	}
	if mh := s.cfg().Downloads.MaxHeight; mh > 0 && (caps.MaxHeight == 0 || caps.MaxHeight > mh) {
		caps.MaxHeight = mh
	}
	// Download conversion is independent of playback's per-viewer
	// VideoTranscodingDisabled switch: apply only the download-conversion
	// setting (server-wide or per-viewer) and per-viewer height cap.
	if policy, ok := s.policy().(frontend.DownloadLimitPolicy); ok && pr != nil {
		allowance := policy.DownloadAllowance(pr.User)
		if mh := allowance.MaxHeight; mh > 0 && (caps.MaxHeight == 0 || caps.MaxHeight > mh) {
			caps.MaxHeight = mh
		}
		if allowance.NoTranscode {
			caps.NoVideoTranscode = true
		}
	} else if s.cfg().Downloads.NoTranscode {
		caps.NoVideoTranscode = true
	}
	return caps
}

// expandDownloadKey turns a show/season key into its episodes; playable
// keys pass through. Returns titles the user may see.
func (s *Server) expandDownloadKey(ctx context.Context, u *frontend.User, k frontend.RatingKey) ([]frontend.Title, error) {
	t, err := s.catalog().Get(ctx, k)
	if err != nil {
		return nil, err
	}
	if !s.visibleTitle(ctx, u, t) {
		return nil, frontend.ErrNotFound
	}
	switch t.Type {
	case frontend.TypeMovie, frontend.TypeEpisode:
		return []frontend.Title{*t}, nil
	case frontend.TypeSeason:
		kids, err := s.catalog().Children(ctx, k)
		if err != nil {
			return nil, err
		}
		return s.visibleTitles(u, kids), nil
	case frontend.TypeShow:
		eps, err := s.catalog().Grandchildren(ctx, k)
		if err != nil {
			return nil, err
		}
		return s.visibleTitles(u, eps), nil
	}
	return nil, frontend.ErrNotFound
}

// POST /downloadQueue/{queue}/add?keys=...
func (s *Server) handleDownloadQueueAdd(w http.ResponseWriter, r *http.Request) {
	if !s.requireDownloads(w, r) {
		return
	}
	q := s.downloadQueueParam(w, r)
	if q == nil {
		return
	}
	pr := FromContext(r.Context())
	ctx := r.Context()
	raw := r.URL.Query()["keys"]
	if len(raw) == 0 {
		raw = r.URL.Query()["key"]
	}
	var keys []frontend.RatingKey
	for _, v := range raw {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part == "" {
				continue
			}
			k, err := parseDownloadKey(part)
			if err != nil {
				writeHTTPError(w, http.StatusBadRequest)
				return
			}
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		writeHTTPError(w, http.StatusBadRequest)
		return
	}
	caps := s.downloadCaps(r, pr)
	burn := r.URL.Query().Get("subtitles") == "burn"
	added := []addedQueueItemModel{}
	for _, k := range keys {
		titles, err := s.expandDownloadKey(ctx, pr.User, k)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		for _, t := range titles {
			it := s.addDownloadItem(q, pr.User, t, caps, burn)
			est := s.estimateDownloadBytes(ctx, &t, caps)
			if msg := s.admitDownload(ctx, it, est); msg != "" {
				// Over the viewer's limit: the item is listed with the
				// reason, as a failed download is, and never starts.
				s.downloads.Fail(ctx, it, msg)
			}
			added = append(added, addedQueueItemModel{ID: it.ID, Key: keyPath(t.Key)})
		}
	}
	s.scheduleDownloads()
	s.write(w, r, &downloadContainer{Size: len(added), Added: added})
}

type downloadLimitError struct {
	Decision frontend.DownloadLimitDecision
}

func (e *downloadLimitError) Error() string {
	return frontend.DownloadLimitMessage(e.Decision)
}

// estimateDownloadBytes returns a conservative size estimate for reserving GB
// quota before the title has been resolved and probed.
func (s *Server) estimateDownloadBytes(ctx context.Context, t *frontend.Title, caps frontend.ClientCaps) int64 {
	if s.playback() == nil || t == nil {
		return 0
	}
	info := s.playback().Estimate(ctx, t, caps)
	if caps.MaxBitrateKbps > 0 && info.Duration > 0 && (info.BitrateKbps == 0 || caps.MaxBitrateKbps < info.BitrateKbps) {
		return int64(caps.MaxBitrateKbps) * 1000 / 8 * int64(info.Duration.Seconds())
	}
	if info.Size > 0 {
		return info.Size
	}
	if info.BitrateKbps > 0 && info.Duration > 0 {
		return int64(info.BitrateKbps) * 1000 / 8 * int64(info.Duration.Seconds())
	}
	return 0
}

// preparedDownloadBytes estimates the download output size once the source
// media has been probed, honoring an explicit bitrate cap when lower.
func preparedDownloadBytes(info frontend.MediaInfo, opts frontend.DecideOptions) int64 {
	if opts.MaxBitrateKbps > 0 && info.Duration > 0 && (info.BitrateKbps == 0 || opts.MaxBitrateKbps < info.BitrateKbps) {
		return int64(opts.MaxBitrateKbps) * 1000 / 8 * int64(info.Duration.Seconds())
	}
	if info.Size > 0 {
		return info.Size
	}
	if info.BitrateKbps > 0 && info.Duration > 0 {
		return int64(info.BitrateKbps) * 1000 / 8 * int64(info.Duration.Seconds())
	}
	return 0
}

// admitDownload reserves estimatedBytes against it.User's download quota and
// returns the message to show when over limit, or "" when admitted.
func (s *Server) admitDownload(ctx context.Context, it *downloadItem, estimatedBytes int64) string {
	policy, ok := s.policy().(frontend.DownloadLimitPolicy)
	if !ok || it == nil || it.User == nil {
		return ""
	}
	dec := policy.AdmitDownload(ctx, it.User, it.Key, estimatedBytes)
	if !dec.Allowed {
		return frontend.DownloadLimitMessage(dec)
	}
	if dec.Reserved {
		s.downloads.Lock()
		it.LimitReserved = true
		s.downloads.Unlock()
	}
	return ""
}

// releaseDownloadLimit frees an uncompleted item's fresh usage reservation.
func (s *Server) releaseDownloadLimit(ctx context.Context, it *downloadItem) {
	if it == nil || it.User == nil {
		return
	}
	s.downloads.Lock()
	reserved := it.LimitReserved
	it.LimitReserved = false
	s.downloads.Unlock()
	if !reserved {
		return
	}
	if policy, ok := s.policy().(frontend.DownloadLimitPolicy); ok {
		policy.ReleaseDownloadReservation(ctx, it.User, it.Key)
	}
}

func (s *Server) downloadTranscodingOffMessage(it *downloadItem) string {
	if s.cfg().Downloads.NoTranscode {
		return frontend.MsgDownloadTranscodingOff
	}
	if it != nil && it.Caps.NoVideoTranscode {
		return frontend.MsgViewerDownloadTranscodingOff
	}
	return frontend.MsgVideoTranscodingOff
}

// addDownloadItem appends an item, or returns the existing one when the
// same title is already queued with the same parameters (PMS dedupes).
func (s *Server) addDownloadItem(q *downloadQueue, u *frontend.User, t frontend.Title, caps frontend.ClientCaps, burn bool) *downloadItem {
	return s.downloads.Add(q, u, t, caps, burn)
}

// parseItemIDs parses "{item}" path params and ?ids= lists; PMS allows
// comma-separated ids in one segment.
func parseItemIDs(r *http.Request) []int64 {
	raw := chi.URLParam(r, "item")
	if raw == "" {
		raw = r.URL.Query().Get("ids")
	}
	var out []int64
	for _, p := range strings.Split(raw, ",") {
		if n, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func (s *Server) writeDownloadItems(w http.ResponseWriter, r *http.Request, q *downloadQueue, only map[int64]bool) {
	s.downloads.Lock()
	items := []downloadQueueItemModel{}
	for _, it := range q.Items {
		if only != nil && !only[it.ID] {
			continue
		}
		items = append(items, itemModel(it))
	}
	s.downloads.Unlock()
	s.write(w, r, &downloadContainer{Size: len(items), Items: items})
}

// GET /downloadQueue/{queue}/items
func (s *Server) handleDownloadQueueItems(w http.ResponseWriter, r *http.Request) {
	if !s.requireDownloads(w, r) {
		return
	}
	s.expireDownloads(s.now())
	if q := s.downloadQueueParam(w, r); q != nil {
		s.writeDownloadItems(w, r, q, nil)
	}
}

// GET /downloadQueue/{queue}/items/{item}
func (s *Server) handleDownloadQueueItem(w http.ResponseWriter, r *http.Request) {
	if !s.requireDownloads(w, r) {
		return
	}
	s.expireDownloads(s.now())
	q := s.downloadQueueParam(w, r)
	if q == nil {
		return
	}
	only := map[int64]bool{}
	for _, id := range parseItemIDs(r) {
		only[id] = true
	}
	s.writeDownloadItems(w, r, q, only)
}

// DELETE /downloadQueue/{queue}/items/{item} — cancel and clean up.
func (s *Server) handleDownloadQueueItemDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireDownloads(w, r) {
		return
	}
	q := s.downloadQueueParam(w, r)
	if q == nil {
		return
	}
	ids := parseItemIDs(r)
	if len(ids) == 0 {
		writeHTTPError(w, http.StatusBadRequest)
		return
	}
	removed := s.downloads.Remove(q, ids)
	for _, it := range removed {
		s.cancelDownloadItem(it)
	}
	s.scheduleDownloads()
	w.WriteHeader(http.StatusNoContent)
}

// POST /downloadQueue/{queue}/items/{item}/restart — reprocess with the
// original decision parameters.
func (s *Server) handleDownloadQueueItemRestart(w http.ResponseWriter, r *http.Request) {
	if !s.requireDownloads(w, r) {
		return
	}
	q := s.downloadQueueParam(w, r)
	if q == nil {
		return
	}
	ids := parseItemIDs(r)
	s.downloads.Restart(q, ids, s.sessionRuntime().Downloads().ReleaseDownload)
	// A restart must not be a way around the daily cap: an item that was
	// refused for it is refused again unless today's tally has room.
	if pr := FromContext(r.Context()); pr != nil && pr.User != nil {
		var restarted []*downloadItem
		s.downloads.Lock()
		for _, it := range q.Items {
			for _, id := range ids {
				if it.ID == id {
					restarted = append(restarted, it)
					break
				}
			}
		}
		s.downloads.Unlock()
		for _, it := range restarted {
			// Release any uncompleted reservation from the previous attempt
			// before re-checking against current usage and policy.
			s.releaseDownloadLimit(r.Context(), it)
			var est int64
			if t, err := s.catalog().Get(r.Context(), it.Key); err == nil && t != nil {
				est = s.estimateDownloadBytes(r.Context(), t, it.Caps)
			}
			if msg := s.admitDownload(r.Context(), it, est); msg != "" {
				s.downloads.Fail(r.Context(), it, msg)
			}
		}
	}
	s.scheduleDownloads()
	w.WriteHeader(http.StatusOK)
}

// cancelDownloadItem stops an item's remux (if running), frees its uncompleted
// quota reservation (if any), and releases its finished file (if any).
func (s *Server) cancelDownloadItem(it *downloadItem) {
	p := s.downloads.CancelItem(it)
	s.releaseDownloadLimit(context.Background(), it)
	if rt := s.sessionRuntime(); p != "" && rt != nil && rt.Downloads() != nil {
		rt.Downloads().ReleaseDownload(p)
	}
}

// downloadItemParam resolves queue+item for the single-item endpoints.
func (s *Server) downloadItemParam(w http.ResponseWriter, r *http.Request) (*downloadQueue, *downloadItem) {
	q := s.downloadQueueParam(w, r)
	if q == nil {
		return nil, nil
	}
	ids := parseItemIDs(r)
	if len(ids) != 1 {
		writeHTTPError(w, http.StatusBadRequest)
		return nil, nil
	}
	s.downloads.Lock()
	it := q.Item(ids[0])
	s.downloads.Unlock()
	if it == nil {
		writeHTTPError(w, http.StatusNotFound)
		return nil, nil
	}
	return q, it
}

// GET /downloadQueue/{queue}/item/{item}/decision — the playback-style
// decision for the item's chosen media.
func (s *Server) handleDownloadItemDecision(w http.ResponseWriter, r *http.Request) {
	if !s.requireDownloads(w, r) {
		return
	}
	s.expireDownloads(s.now())
	_, it := s.downloadItemParam(w, r)
	if it == nil {
		return
	}
	ctx := r.Context()
	pr := FromContext(ctx)
	t, err := s.catalog().Get(ctx, it.Key)
	if err == nil && !s.visibleTitle(ctx, pr.User, t) {
		err = frontend.ErrNotFound // restrictions can change after queueing
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.downloads.Lock()
	m := itemModel(it)
	status, size, dur, vCodec, aCodec := it.Status, it.Size, it.Duration, it.VideoCodec, it.AudioCodec
	s.downloads.Unlock()
	mc := s.itemContainer(t.Type)
	md := s.metadata(ctx, pr, t, renderOpts{detail: true, media: true, session: true})
	decisionItem(&md)
	if len(md.Media) > 0 {
		chosen := md.Media[0]
		chosen.Selected = model.Ptr(true)
		chosen.SetAlphabetical()
		if len(chosen.Part) > 0 {
			chosen.Part[0].Selected = model.Ptr(true)
			chosen.Part[0].SetAlphabetical()
			for i := range chosen.Part[0].Stream {
				chosen.Part[0].Stream[i].SetAlphabetical()
			}
		}
		if status != downloadDeciding && status != downloadError {
			chosen.Container = "mp4"
			chosen.Protocol = "http"
			chosen.OptimizedForStreaming = model.Ptr(1)
			if dur > 0 {
				chosen.Duration = ms(dur)
				md.Duration = chosen.Duration
			}
			if vCodec != "" {
				chosen.VideoCodec = plexCodec(vCodec)
			}
			if aCodec != "" {
				chosen.AudioCodec = plexCodec(aCodec)
			}
			if size > 0 && dur > 0 {
				if sec := dur.Seconds(); sec > 0 {
					if kbps := int(float64(size*8) / 1000 / sec); kbps > 0 {
						chosen.Bitrate = kbps
					}
				}
			}
			if len(chosen.Part) > 0 {
				part := &chosen.Part[0]
				part.Decision = "transcode"
				part.Protocol = "http"
				part.Container = "mp4"
				part.Key = ""
				part.File = ""
				part.OptimizedForStreaming = model.Ptr(true)
				if size > 0 {
					part.Size = size
				} else {
					part.Size = 0
				}
				if dur > 0 {
					part.Duration = ms(dur)
				}
				videoMarked, audioMarked := false, false
				for i := range part.Stream {
					switch part.Stream[i].StreamType {
					case 1:
						if !videoMarked {
							part.Stream[i].Decision = "copy"
							part.Stream[i].Location = "direct"
							videoMarked = true
						}
					case 2:
						if part.Stream[i].Selected != nil && *part.Stream[i].Selected {
							part.Stream[i].Decision = "copy"
							part.Stream[i].Location = "direct"
							audioMarked = true
						}
					}
				}
				if !audioMarked {
					for i := range part.Stream {
						if part.Stream[i].StreamType == 2 {
							part.Stream[i].Decision = "copy"
							part.Stream[i].Location = "direct"
							break
						}
					}
				}
			}
		}
		md.Media = []model.Media{chosen}
	}
	mc.Metadata = []model.Metadata{md}
	mc.Size = 1
	mc.SetDecision()
	dr := m.Decision
	mc.Extra = []model.Attr{
		model.A("directPlayDecisionCode", dr.DirectPlayDecisionCode),
		model.A("directPlayDecisionText", dr.DirectPlayDecisionText),
		model.A("generalDecisionCode", dr.GeneralDecisionCode),
		model.A("generalDecisionText", dr.GeneralDecisionText),
		model.A("transcodeDecisionCode", dr.TranscodeDecisionCode),
		model.A("transcodeDecisionText", dr.TranscodeDecisionText),
		model.A("mdeDecisionCode", dr.MDEDecisionCode),
		model.A("mdeDecisionText", dr.MDEDecisionText),
	}
	if rs := cmp.Or(pr.SessionID, pr.ClientID); rs != "" {
		mc.Extra = append(mc.Extra, model.A("resourceSession", rs))
	}
	s.write(w, r, mc)
}

// GET /downloadQueue/{queue}/item/{item}/media — the remuxed MP4 with
// Range support and stable validators; 503 + Retry-After while it is still
// being produced.
func (s *Server) handleDownloadItemMedia(w http.ResponseWriter, r *http.Request) {
	if !s.requireDownloads(w, r) {
		return
	}
	s.expireDownloads(s.now())
	_, it := s.downloadItemParam(w, r)
	if it == nil {
		return
	}
	s.downloads.Lock()
	status, p, readyAt, title, key := it.Status, it.Path, it.ReadyAt, it.Title, it.Key
	s.downloads.Unlock()
	// Restrictions can change after an item was queued: serve the cached
	// file only while the viewer may still see the title.
	if t, err := s.catalog().Get(r.Context(), key); err != nil || !s.visibleTitle(r.Context(), FromContext(r.Context()).User, t) {
		writeHTTPError(w, http.StatusNotFound)
		return
	}
	switch status {
	case downloadDeciding, downloadWaiting, downloadProcessing:
		w.Header().Set("Retry-After", "-1")
		writeHTTPError(w, http.StatusServiceUnavailable)
		return
	case downloadExpired:
		writeHTTPError(w, http.StatusGone)
		return
	case downloadError:
		writeHTTPError(w, http.StatusInternalServerError)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeHTTPError(w, http.StatusGone)
		return
	}
	defer f.Close()
	name := downloadFilename(title)
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", contentDisposition(name))
	cw := s.newCountWriter(w, r)
	if err := rangefile.ServeFile(cw, r, name, filepath.Base(p), readyAt, f); err != nil {
		writeHTTPError(w, http.StatusGone)
		return
	}
	cw.finish()
}

// downloadFilename is the client-visible file name for a download.
func downloadFilename(title string) string {
	if title == "" {
		title = "download"
	}
	var b strings.Builder
	for _, c := range title {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == ' ', c == '-', c == '_', c == '.':
			b.WriteRune(c)
		default:
			b.WriteRune('_')
		}
	}
	return strings.TrimSpace(b.String()) + ".mp4"
}

func contentDisposition(name string) string {
	return fmt.Sprintf("attachment; filename*=UTF-8''%s; filename=\"%s\"",
		pathEscape(strings.NewReplacer("\r", "", "\n", "").Replace(name)), strings.NewReplacer("+", "_", "\"", "_", "\r", "", "\n", "", "\\", "_").Replace(name))
}

func pathEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Processing

// scheduleDownloads starts queued items up to the per-user cap. The
// remuxer enforces the server-wide cap.
func (s *Server) scheduleDownloads() {
	defer s.stageDownloads()
	jobs := s.downloads.StartReady(s.ctx, s.cfg().Downloads.MaxConcurrentPerUser, s.downloadsAllowed)
	for _, job := range jobs {
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.processDownloadItem(job.Context, job.Item, job.Run) }()
	}
}

func (s *Server) processDownloadItem(ctx context.Context, it *downloadItem, run int64) {
	defer s.scheduleDownloads()
	fail := func(msg string) { s.downloads.Fail(ctx, it, msg) }

	noRelease := func(err error) bool {
		return err != nil && errors.Is(err, frontend.ErrNotAvailable) && playbackMessage(err) == msgNotAvailable
	}
	rt := s.sessionRuntime()
	job := rt.NewDownloadJob(s.playback(), rt.Downloads())
	p, size, err := job.Run(ctx, frontend.DownloadRequest{Viewer: it.User, Item: it.Key, Caps: it.Caps, Run: run, NoRelease: noRelease,
		Prepared: func(ps *frontend.PlaySession) (frontend.DecideOptions, error) {
			if !s.downloads.Prepared(ctx, it, ps.Info) {
				return frontend.DecideOptions{}, ctx.Err()
			}

			var viewerID int64
			if it.User != nil {
				viewerID = it.User.ID
			}
			prefCtx := withRequest(ctx, &Request{User: it.User, ClientID: it.Caps.ClientID})
			original := ""
			if title, err := s.catalog().Get(ctx, it.Key); err == nil && title != nil {
				original = title.Language
			}
			sel := resolveSelection(s.selections.get(viewerID, int64(it.Key)), ps.Info)
			sel = s.preferredSelection(prefCtx, it.User, it.Key, ps.Info, original, sel)
			audioPos := -1
			if sel.audioSet {
				audioPos = sel.audio
			}
			if audioPos < 0 || audioPos >= len(ps.Info.Streams) {
				audioPos = pickAudio(ps.Info, s.audioLanguages(it.User))
			}
			audio := -1
			if audioPos >= 0 && audioPos < len(ps.Info.Streams) && ps.Info.Streams[audioPos].Type == "audio" {
				st := ps.Info.Streams[audioPos]
				audio = st.Index
				if st.Codec != "" {
					s.downloads.Lock()
					if ctx.Err() == nil {
						it.AudioCodec = st.Codec
					}
					s.downloads.Unlock()
				}
			}
			subtitle := -1
			if sel.subtitleSet && sel.subtitle >= 0 && sel.subtitle < len(ps.Info.Streams) {
				st := ps.Info.Streams[sel.subtitle]
				if st.Type == "subtitle" && !st.External {
					subtitle = st.Index
				}
			}
			opts := frontend.DecideOptions{
				DirectPlay: false, DirectStream: true,
				MaxBitrateKbps: it.Bandwidth, MaxHeight: it.Caps.MaxHeight, AudioStream: audio, SubtitleStream: subtitle,
				BurnSubtitles: it.Burn,
			}
			if policy, ok := s.policy().(frontend.DownloadLimitPolicy); ok && it.User != nil {
				if est := preparedDownloadBytes(ps.Info, opts); est > 0 {
					if dec := policy.ReconcileDownloadBytes(ctx, it.User, it.Key, est); !dec.Allowed {
						return frontend.DecideOptions{}, &downloadLimitError{Decision: dec}
					}
				}
			}
			return opts, nil
		}, Progress: func(f float64) {
			s.downloads.Progress(ctx, it, f)
		}})

	if err != nil {
		var limitErr *downloadLimitError
		if errors.As(err, &limitErr) {
			s.releaseDownloadLimit(context.Background(), it)
			fail(limitErr.Error())
			return
		}
		var preparation *frontend.DownloadPreparationError
		if errors.As(err, &preparation) {
			if ctx.Err() != nil {
				return
			}
			s.releaseDownloadLimit(context.Background(), it)
			s.log.Warn("download preparation failed", "key", it.Key, "err", err)
			var reqLang *frontend.RequiredAudioLanguageError
			if errors.As(err, &reqLang) {
				fail(reqLang.Error())
			} else if noRelease(err) {
				fail("No release available for download at this quality.")
			} else if errors.Is(err, frontend.ErrVideoTranscodingDisabled) {
				fail(s.downloadTranscodingOffMessage(it))
			} else {
				fail(playbackMessage(err))
			}
			return
		}
		if ctx.Err() == nil {
			s.releaseDownloadLimit(context.Background(), it)
			s.log.Warn("download processing failed", "key", it.Key, "err", err)
		}
		var reqLang *frontend.RequiredAudioLanguageError
		switch {
		case ctx.Err() != nil:
			// Deleted, restarted or shutting down; state already reset.
		case errors.As(err, &reqLang):
			fail(reqLang.Error())
		case errors.Is(err, frontend.ErrDownloadNeedsTranscode):
			fail("This title cannot be downloaded: it cannot be converted to a playable MP4.")
		case errors.Is(err, frontend.ErrVideoTranscodingDisabled):
			fail(s.downloadTranscodingOffMessage(it))
		case errors.Is(err, frontend.ErrDownloadBudget):
			fail("Not enough temporary disk space on the server for this download.")
		default:
			fail(msgPlayFailed)
		}
		return
	}
	if !s.downloads.Complete(ctx, it, p, size, s.now()) {
		s.releaseDownloadLimit(context.Background(), it)
		s.sessionRuntime().Downloads().ReleaseDownload(p)
		return
	}
	s.downloads.Lock()
	it.LimitReserved = false
	s.downloads.Unlock()
	if policy, ok := s.policy().(frontend.DownloadLimitPolicy); ok {
		policy.RecordDownloadBytes(ctx, it.User, it.Key, size)
	}
	s.retainDownload(p, it.ReadyAt)
}

// expireDownloads frees finished downloads older than the configured
// retention; their items stay listed as expired until deleted.
func (s *Server) expireDownloads(now time.Time) {
	if s.downloads == nil {
		return
	}
	paths := s.downloads.Expire(now, s.cfg().Downloads.Expiry.D())
	if len(paths) > 0 {
		s.stageDownloads()
	}
	if rt := s.sessionRuntime(); rt != nil && rt.Downloads() != nil {
		for _, p := range paths {
			rt.Downloads().ReleaseDownload(p)
		}
	}
}

// releaseAllDownloads cancels in-flight remuxes and deletes every temp
// file (server shutdown).
func (s *Server) releaseAllDownloads() {
	if s.downloads == nil {
		return
	}
	items := s.downloads.AllItems()
	for _, it := range items {
		s.cancelDownloadItem(it)
	}
}
