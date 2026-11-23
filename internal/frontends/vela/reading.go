package vela

import (
	"cmp"
	"crypto/rand"
	"encoding/base64"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/rangefile"
	"github.com/braedonsaunders/ether/internal/frontend/kit/reading"
)

const (
	readTicketTTL    = 15 * time.Minute
	readTicketMaxTTL = time.Hour
	maxReadTickets   = 64
	maxLocatorBytes  = 4096
	maxLabelBytes    = 512
)

var readingFormats = reading.Formats

type readTicket struct {
	mu        sync.RWMutex
	closed    bool
	media     *frontend.KindMedia
	viewerID  int64
	key       frontend.RatingKey
	format    string
	mimeType  string
	name      string
	identity  string
	modTime   time.Time
	size      int64
	createdAt time.Time
	expiresAt time.Time
	lastUsed  time.Time
}

func (t *readTicket) close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	m := t.media
	t.media = nil
	t.mu.Unlock()
	if m != nil && m.Closer != nil {
		_ = m.Closer.Close()
	}
}

func (i *instance) readingAvailable() bool {
	if i == nil || i.host.KindStreams == nil {
		return false
	}
	_, ok := i.host.Experience.(frontend.ReadingProgressWriter)
	return ok
}

func (i *instance) closeReadTickets() {
	i.readMu.Lock()
	tickets := i.readTickets
	i.readTickets = nil
	i.readMu.Unlock()
	for _, t := range tickets {
		t.close()
	}
}

func (i *instance) read(w http.ResponseWriter, r *http.Request) {
	v, _, e := i.viewer(r)
	if e != nil {
		fail(w, e, r)
		return
	}
	if i.host.KindStreams == nil {
		i.unavailable(w, r)
		return
	}
	k, e := key(r)
	if e != nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	detail, e := i.host.Experience.Item(r.Context(), frontend.ItemRequest{Viewer: v, Device: requestDevice(r), Key: k})
	if e != nil {
		fail(w, e, r)
		return
	}
	if !k.IsPluginKey() || !reading.IsBookTitle(detail.Title) ||
		detail.Unavailable != nil || (detail.CanRead != nil && !*detail.CanRead) {
		problem(w, 400, "request", "not_readable")
		return
	}
	media, e := i.host.KindStreams.OpenKind(r.Context(), v, k, reading.AcceptPatterns)
	if e != nil {
		fail(w, e, r)
		return
	}
	if media == nil {
		fail(w, frontend.ErrNotAvailable, r)
		return
	}
	format, mimeType, ok := reading.DetectFormat(media, detail.Title)
	if !ok {
		if media.Closer != nil {
			_ = media.Closer.Close()
		}
		problem(w, 422, "media", "unsupported_format")
		return
	}

	now := time.Now().UTC()
	if i.host.Now != nil {
		now = i.host.Now().UTC()
	}
	token, err := mintReadTicketToken()
	if err != nil {
		if media.Closer != nil {
			_ = media.Closer.Close()
		}
		fail(w, err, r)
		return
	}
	expires := now.Add(readTicketTTL)
	name := reading.Filename(media.Name, detail.Title.Title, format)
	modTime := media.Release.PubDate
	if modTime.IsZero() {
		modTime = detail.Title.UpdatedAt
	}
	entry := &readTicket{
		media:     media,
		viewerID:  v.ID,
		key:       k,
		format:    format,
		mimeType:  mimeType,
		name:      name,
		identity:  cmp.Or(media.Release.ID, "book:"+k.String()+":"+name),
		modTime:   modTime,
		size:      media.Size,
		createdAt: now,
		expiresAt: expires,
		lastUsed:  now,
	}
	i.storeReadTicket(token, entry, now)

	out := object{
		"titleId":   detail.Title.Key.String(),
		"format":    format,
		"mimeType":  mimeType,
		"url":       "/vela/v0/read/" + token,
		"expiresAt": expires.Format(time.RFC3339),
	}
	if media.Size > 0 {
		out["byteLength"] = media.Size
	}
	if rp := readingProgressWire(detail.UserData.Reading); rp != nil {
		out["progress"] = rp
	}
	send(w, http.StatusOK, out)
}

func (i *instance) readFile(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.PathValue("ticket"))
	if token == "" {
		http.NotFound(w, r)
		return
	}
	now := time.Now().UTC()
	if i.host.Now != nil {
		now = i.host.Now().UTC()
	}
	entry := i.lookupReadTicket(token, now)
	if entry == nil {
		http.NotFound(w, r)
		return
	}
	entry.mu.RLock()
	defer entry.mu.RUnlock()
	if entry.closed || entry.media == nil || entry.media.ReaderAt == nil || entry.size < 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", entry.mimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Cache-Control", "no-store")
	sr := io.NewSectionReader(reading.ContextReaderAt(r.Context(), entry.media.ReaderAt), 0, entry.size)
	rangefile.Serve(w, r, entry.name, entry.identity, entry.size, entry.modTime, sr)
}

func (i *instance) readingProgress(w http.ResponseWriter, r *http.Request) {
	v, _, e := i.viewer(r)
	if e != nil {
		fail(w, e, r)
		return
	}
	writer, ok := i.host.Experience.(frontend.ReadingProgressWriter)
	if !ok {
		i.unavailable(w, r)
		return
	}
	k, e := key(r)
	if e != nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	if _, e := i.host.Experience.Item(r.Context(), frontend.ItemRequest{Viewer: v, Device: requestDevice(r), Key: k}); e != nil {
		fail(w, e, r)
		return
	}
	var b struct {
		Locator    string   `json:"locator"`
		Fraction   *float64 `json:"fraction"`
		Label      string   `json:"label"`
		ObservedAt string   `json:"observedAt"`
	}
	if body(r, &b) != nil {
		problem(w, 400, "request", "invalid_request")
		return
	}
	locator := strings.TrimSpace(b.Locator)
	if locator == "" || len(locator) > maxLocatorBytes || b.Fraction == nil {
		problem(w, 400, "request", "invalid_request")
		return
	}
	frac := *b.Fraction
	if math.IsNaN(frac) || math.IsInf(frac, 0) || frac < 0 || frac > 1 {
		problem(w, 400, "request", "invalid_request")
		return
	}
	label := strings.TrimSpace(b.Label)
	if len(label) > maxLabelBytes {
		problem(w, 400, "request", "invalid_request")
		return
	}
	var observedAt time.Time
	if rawObs := strings.TrimSpace(b.ObservedAt); rawObs != "" {
		t, err := time.Parse(time.RFC3339Nano, rawObs)
		if err != nil {
			problem(w, 400, "request", "invalid_request")
			return
		}
		observedAt = t.UTC()
	}
	err := writer.SetReadingProgress(r.Context(), v, k, frontend.ReadingProgress{
		Locator:    locator,
		Fraction:   frac,
		Label:      label,
		ObservedAt: observedAt,
	})
	if err != nil {
		fail(w, err, r)
		return
	}
	send(w, http.StatusOK, object{"accepted": true})
}

func (i *instance) storeReadTicket(token string, entry *readTicket, now time.Time) {
	var evicted []*readTicket
	i.readMu.Lock()
	if i.readTickets == nil {
		i.readTickets = map[string]*readTicket{}
	}
	for k, existing := range i.readTickets {
		if !existing.expiresAt.After(now) {
			evicted = append(evicted, existing)
			delete(i.readTickets, k)
		}
	}
	for len(i.readTickets) >= maxReadTickets {
		var oldestKey string
		var oldest *readTicket
		for k, existing := range i.readTickets {
			if oldest == nil || existing.lastUsed.Before(oldest.lastUsed) {
				oldestKey = k
				oldest = existing
			}
		}
		if oldest == nil {
			break
		}
		evicted = append(evicted, oldest)
		delete(i.readTickets, oldestKey)
	}
	i.readTickets[token] = entry
	i.readMu.Unlock()
	for _, old := range evicted {
		old.close()
	}
}

func (i *instance) lookupReadTicket(token string, now time.Time) *readTicket {
	var expired *readTicket
	i.readMu.Lock()
	entry := i.readTickets[token]
	if entry != nil {
		if !entry.expiresAt.After(now) {
			delete(i.readTickets, token)
			expired = entry
			entry = nil
		} else {
			entry.lastUsed = now
			// Slide expiry while actively reading, bounded by readTicketMaxTTL.
			extended := now.Add(readTicketTTL)
			capTime := entry.createdAt.Add(readTicketMaxTTL)
			if extended.After(capTime) {
				extended = capTime
			}
			if extended.After(entry.expiresAt) {
				entry.expiresAt = extended
			}
		}
	}
	i.readMu.Unlock()
	if expired != nil {
		expired.close()
	}
	return entry
}

func mintReadTicketToken() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func readingProgressWire(r *frontend.ReadingProgress) object {
	if r == nil || r.Locator == "" {
		return nil
	}
	out := object{
		"locator":  r.Locator,
		"fraction": r.Fraction,
	}
	if r.Label != "" {
		out["label"] = r.Label
	}
	if !r.UpdatedAt.IsZero() {
		out["updatedAt"] = r.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return out
}
