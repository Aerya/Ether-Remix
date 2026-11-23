package mediabrowser

import (
	"cmp"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/mediasafety"
	"github.com/braedonsaunders/ether/internal/frontend/kit/rangefile"
	"github.com/braedonsaunders/ether/internal/frontend/kit/reading"
)

// bookReadable reports whether detail represents a leaf plugin book that this
// instance can serve through /Items/{id}/Download and /Items/{id}/File.
func (i *instance) bookReadable(detail frontend.ItemDetail) bool {
	if i == nil || i.host.KindStreams == nil || !detail.Title.Key.IsPluginKey() {
		return false
	}
	if !mbIsBookItem(detail.Title) || mbIsFolder(detail.Title) || detail.Unavailable != nil {
		return false
	}
	if detail.CanRead != nil && !*detail.CanRead {
		return false
	}
	return true
}

// serveBookFile serves GET/HEAD /Items/{id}/Download and /Items/{id}/File for
// readable plugin book items (EPUB, PDF, CBZ) via the shared KindStreams path.
// Non-book items (movies, episodes, audio, folders) and unavailable books are
// refused with 404.
func (i *instance) serveBookFile(w http.ResponseWriter, r *http.Request, attachment bool) {
	viewer, _, err := i.viewer(r)
	if err != nil || viewer == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if i.host.Experience == nil || i.host.KindStreams == nil {
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
		return
	}
	key, err := ids.ParseRatingKeyGUID(r.PathValue("id"))
	if err != nil || !key.IsPluginKey() {
		http.NotFound(w, r)
		return
	}
	detail, err := i.host.Experience.Item(i.identityContext(r), frontend.ItemRequest{
		Viewer: viewer,
		Device: device(r),
		Key:    key,
	})
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !i.bookReadable(detail) {
		http.NotFound(w, r)
		return
	}
	var (
		dlPolicy frontend.DownloadLimitPolicy
		reserved bool
		served   bool
	)
	if attachment && r.Method != http.MethodHead {
		if p, ok := i.host.ViewerPolicy.(frontend.DownloadLimitPolicy); ok {
			dlPolicy = p
			dec := p.AdmitDownload(r.Context(), viewer, key, 0)
			if !dec.Allowed {
				if wait := int(time.Until(dec.ResetAt).Seconds()); wait > 0 {
					w.Header().Set("Retry-After", strconv.Itoa(wait))
				}
				http.Error(w, frontend.DownloadLimitMessage(dec), http.StatusTooManyRequests)
				return
			}
			reserved = dec.Reserved
			defer func() {
				if reserved && !served {
					dlPolicy.ReleaseDownloadReservation(context.Background(), viewer, key)
				}
			}()
		}
	}
	media, err := i.host.KindStreams.OpenKind(r.Context(), viewer, key, reading.AcceptPatterns)
	if err != nil {
		if errors.Is(err, frontend.ErrArchiveUnstreamable) {
			http.Error(w, "Unsupported book format", http.StatusUnprocessableEntity)
			return
		}
		streamError(i.protocol, w, err)
		return
	}
	if media == nil {
		streamError(i.protocol, w, frontend.ErrNotAvailable)
		return
	}
	if media.Closer != nil {
		defer media.Closer.Close()
	}
	if media.ReaderAt == nil || media.Size < 0 {
		streamError(i.protocol, w, frontend.ErrNotAvailable)
		return
	}
	format, mimeType, ok := reading.DetectFormat(media, detail.Title)
	if !ok {
		http.Error(w, "Unsupported book format", http.StatusUnprocessableEntity)
		return
	}
	if dlPolicy != nil && media.Size > 0 {
		if rec := dlPolicy.ReconcileDownloadBytes(r.Context(), viewer, key, media.Size); !rec.Allowed {
			if wait := int(time.Until(rec.ResetAt).Seconds()); wait > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(wait))
			}
			http.Error(w, frontend.DownloadLimitMessage(rec), http.StatusTooManyRequests)
			return
		}
	}
	served = true
	filename := reading.Filename(media.Name, detail.Title.Title, format)
	dispType := "inline"
	if attachment {
		dispType = "attachment"
	}
	w.Header().Set("Content-Type", mimeType)
	if disp := mime.FormatMediaType(dispType, map[string]string{"filename": filename}); disp != "" {
		w.Header().Set("Content-Disposition", disp)
	}
	mediasafety.SetHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	identity := cmp.Or(media.Release.ID, "book:"+key.String()+":"+filename)
	modTime := media.Release.PubDate
	if modTime.IsZero() {
		modTime = detail.Title.UpdatedAt
	}
	sr := io.NewSectionReader(reading.ContextReaderAt(r.Context(), media.ReaderAt), 0, media.Size)
	rangefile.Serve(w, r, filename, identity, media.Size, modTime, sr)
}
