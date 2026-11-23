package app

import (
	"context"
	"slices"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// pluginKindVersions lists the playable version menu of a plugin kind item
// (Auto slot + remaining /resolve sources). It is the broker's KindVersions
// dep and the plugin branch of the Vela/Plex version surfaces; it reuses the
// KindResolver candidate cache so a warm list never re-runs the plugin.
func (a *App) pluginKindVersions(ctx context.Context, viewer *core.Viewer, key core.RatingKey) ([]core.ReleaseVariant, error) {
	if a.pluginsRT == nil || a.pluginsRT.kindResolver == nil || viewer == nil || !key.IsPluginKey() {
		return nil, core.ErrNotFound
	}
	return a.pluginsRT.kindResolver.KindVersions(ctx, viewer, key, kindAccept, true)
}

// kindAccept lists the retrieval patterns the transcode bridge can turn into
// bytes. "direct" is excluded: a redirect cannot be probed or encoded, so a
// kind source must be a real stream.
var kindAccept = []string{"range", "download", "container", "release"}

// kindSourceFunc is the broker's KindSource dep: it projects a plugin kind
// item and hands back an opener for its bytes (spec §15.1). The opener
// re-authorizes the viewer through KindResolver.OpenKindExcluding, so
// authorization is enforced at open time, not on the projection alone.
func (a *App) kindSourceFunc() func(context.Context, *core.Viewer, core.RatingKey) (*core.KindInput, error) {
	if a.pluginsRT == nil || a.pluginsRT.kindCatalog == nil || a.pluginsRT.kindResolver == nil {
		return nil
	}
	catalog := a.pluginsRT.kindCatalog
	resolver := a.pluginsRT.kindResolver
	return func(ctx context.Context, viewer *core.Viewer, key core.RatingKey) (*core.KindInput, error) {
		if viewer == nil || !key.IsPluginKey() {
			return nil, core.ErrNotFound
		}
		piv, err := catalog.Item(ctx, key)
		if err != nil {
			return nil, core.ErrNotFound
		}
		title := piv.Title
		if title.MediaClass == core.MediaClassBook || title.MBItemType == "Book" {
			return nil, core.ErrNoUsableFile
		}
		in := &core.KindInput{
			Title:         &title,
			SourceID:      key.String(),
			OpenCandidate: nil,
		}
		in.OpenCandidate = func(octx context.Context, exclude []string) (core.MediaSource, core.Release, error) {
			if core.IndexerPurpose(octx) == "" {
				octx = core.WithIndexerPurpose(octx, core.IndexerPurposePlayback)
			}
			var media *frontend.KindMedia
			var err error
			if in.PreferSourceID != "" && in.PreferSourceID != core.KindAutoVersionID {
				if slices.Contains(exclude, in.PreferSourceID) {
					if in.Exact {
						return nil, core.Release{}, core.ErrNotAvailable
					}
					media, err = resolver.OpenKindExcluding(octx, viewer, key, kindAccept, exclude)
				} else {
					media, err = resolver.OpenKindSource(octx, viewer, key, in.PreferSourceID, kindAccept)
				}
			} else {
				media, err = resolver.OpenKindExcluding(octx, viewer, key, kindAccept, exclude)
			}
			if err != nil {
				return nil, core.Release{}, err
			}
			rel := media.Release
			if rel.ID == "" {
				rel.ID = "kind:" + key.String()
			}
			if rel.Size <= 0 {
				rel.Size = media.Size
			}
			if ms := media.MediaSource; ms != nil && ms.Size() == media.Size && ms.ReleaseID() == rel.ID {
				return ms, rel, nil
			}
			return wrapKindMediaSource(media, rel.ID), rel, nil
		}
		in.Open = func(octx context.Context) (core.MediaSource, error) {
			ms, _, err := in.OpenCandidate(octx, nil)
			return ms, err
		}
		return in, nil
	}
}

// wrapKindMediaSource adapts a frontend.KindMedia into a core.MediaSource while
// preserving optional playback interfaces (concurrent range reads, direct-play
// retries, delivery reporting, damage/repair hooks, archive volumes, direct
// URLs) supported by the underlying source.
func wrapKindMediaSource(media *frontend.KindMedia, releaseID string) core.MediaSource {
	var inner any = media.ReaderAt
	if media.MediaSource != nil && media.MediaSource.Size() == media.Size {
		inner = media.MediaSource
	}
	base := &kindMediaSource{
		media:     media,
		inner:     inner,
		releaseID: releaseID,
	}
	if avs, ok := inner.(core.ArchiveVolumeSource); ok {
		arch := &kindMediaSourceArchive{kindMediaSource: base, avs: avs}
		if sd, ok := inner.(core.SourceDeliveryReporter); ok {
			return &kindMediaSourceArchiveDelivery{kindMediaSourceArchive: arch, sd: sd}
		}
		return arch
	}
	cr, hasCR := inner.(core.ConcurrentRangeReaderAt)
	dr, hasDR := inner.(core.DirectPlayRetrier)
	du, hasDU := inner.(core.DirectURLSource)
	sd, hasSD := inner.(core.SourceDeliveryReporter)
	switch {
	case hasCR && hasDR && hasDU:
		return &kindMediaSourceConcurrentRetrierDirect{kindMediaSource: base, cr: cr, dr: dr, du: du}
	case hasCR && hasDR:
		return &kindMediaSourceConcurrentRetrier{kindMediaSource: base, cr: cr, dr: dr}
	case hasCR:
		return &kindMediaSourceConcurrent{kindMediaSource: base, cr: cr}
	case hasDR:
		return &kindMediaSourceRetrier{kindMediaSource: base, dr: dr}
	case hasSD:
		return &kindMediaSourceDelivery{kindMediaSource: base, sd: sd}
	case hasDU:
		return &kindMediaSourceDirect{kindMediaSource: base, du: du}
	default:
		return base
	}
}

// kindMediaSource adapts a frontend.KindMedia (a byte-addressable plugin
// stream) into a core.MediaSource so the playback session pipeline can probe
// and encode it like any other source.
type kindMediaSource struct {
	media     *frontend.KindMedia
	inner     any
	releaseID string
}

func (k *kindMediaSource) ReadAt(p []byte, off int64) (int, error) { return k.media.ReadAt(p, off) }
func (k *kindMediaSource) ReadAtContext(ctx context.Context, p []byte, off int64) (int, error) {
	if cr, ok := k.inner.(core.ContextReaderAt); ok {
		return cr.ReadAtContext(ctx, p, off)
	}
	if cr, ok := k.media.ReaderAt.(core.ContextReaderAt); ok {
		return cr.ReadAtContext(ctx, p, off)
	}
	return k.media.ReadAt(p, off)
}
func (k *kindMediaSource) ReadMetadataAtContext(ctx context.Context, p []byte, off int64) (int, error) {
	if mr, ok := k.inner.(core.MetadataReaderAt); ok {
		return mr.ReadMetadataAtContext(ctx, p, off)
	}
	return k.ReadAtContext(ctx, p, off)
}
func (k *kindMediaSource) ReadAtPrefetchContext(ctx context.Context, p []byte, off int64) (int, error) {
	if pr, ok := k.inner.(core.PrefetchReaderAt); ok {
		return pr.ReadAtPrefetchContext(ctx, p, off)
	}
	return k.ReadAtContext(ctx, p, off)
}
func (k *kindMediaSource) ReadAtAnalysisContext(ctx context.Context, p []byte, off int64) (int, error) {
	if ar, ok := k.inner.(core.AnalysisReaderAt); ok {
		return ar.ReadAtAnalysisContext(ctx, p, off)
	}
	return k.ReadAtPrefetchContext(ctx, p, off)
}
func (k *kindMediaSource) BufferedAhead() int64 {
	if bs, ok := k.inner.(core.BufferedSource); ok {
		return bs.BufferedAhead()
	}
	return 0
}
func (k *kindMediaSource) SetPlaybackDuration(d time.Duration) {
	if ps, ok := k.inner.(core.PlaybackDurationSetter); ok {
		ps.SetPlaybackDuration(d)
	}
}
func (k *kindMediaSource) SetDamageHandler(handler func(int64, int64) bool) {
	if dr, ok := k.inner.(core.DamageReporter); ok {
		dr.SetDamageHandler(handler)
	}
}
func (k *kindMediaSource) SetRepairHandler(handler func(core.RepairEvent) core.RepairWindow) {
	if rr, ok := k.inner.(core.RepairReporter); ok {
		rr.SetRepairHandler(handler)
	}
}
func (k *kindMediaSource) MediaCapWaitKnown() bool {
	if mc, ok := k.inner.(core.MediaCapWaitSource); ok {
		return mc.MediaCapWaitKnown()
	}
	return false
}
func (k *kindMediaSource) Origin() string {
	if os, ok := k.inner.(core.OriginSource); ok {
		return os.Origin()
	}
	return ""
}
func (k *kindMediaSource) ResolvedArchiveMember() bool {
	if rm, ok := k.inner.(core.ResolvedMemberSource); ok {
		return rm.ResolvedArchiveMember()
	}
	return false
}
func (k *kindMediaSource) Size() int64 { return k.media.Size }
func (k *kindMediaSource) Name() string {
	if k.media.Name != "" {
		return k.media.Name
	}
	return k.releaseID
}
func (k *kindMediaSource) ReleaseID() string { return k.releaseID }
func (k *kindMediaSource) MIME() string {
	if k.media.MIME != "" {
		return k.media.MIME
	}
	if ms, ok := k.inner.(core.MIMESource); ok {
		return ms.MIME()
	}
	return ""
}
func (k *kindMediaSource) Close() error { return k.media.Close() }

type kindMediaSourceConcurrent struct {
	*kindMediaSource
	cr core.ConcurrentRangeReaderAt
}

func (k *kindMediaSourceConcurrent) ReadAtConcurrentContext(ctx context.Context, p []byte, off int64) (int, error) {
	return k.cr.ReadAtConcurrentContext(ctx, p, off)
}

type kindMediaSourceConcurrentRetrier struct {
	*kindMediaSource
	cr core.ConcurrentRangeReaderAt
	dr core.DirectPlayRetrier
}

func (k *kindMediaSourceConcurrentRetrier) ReadAtConcurrentContext(ctx context.Context, p []byte, off int64) (int, error) {
	return k.cr.ReadAtConcurrentContext(ctx, p, off)
}
func (k *kindMediaSourceConcurrentRetrier) RetryDirectPlay() bool { return k.dr.RetryDirectPlay() }

type kindMediaSourceConcurrentRetrierDirect struct {
	*kindMediaSource
	cr core.ConcurrentRangeReaderAt
	dr core.DirectPlayRetrier
	du core.DirectURLSource
}

func (k *kindMediaSourceConcurrentRetrierDirect) ReadAtConcurrentContext(ctx context.Context, p []byte, off int64) (int, error) {
	return k.cr.ReadAtConcurrentContext(ctx, p, off)
}
func (k *kindMediaSourceConcurrentRetrierDirect) RetryDirectPlay() bool {
	return k.dr.RetryDirectPlay()
}
func (k *kindMediaSourceConcurrentRetrierDirect) DirectURL() (core.DirectSourceLink, bool) {
	return k.du.DirectURL()
}

type kindMediaSourceRetrier struct {
	*kindMediaSource
	dr core.DirectPlayRetrier
}

func (k *kindMediaSourceRetrier) RetryDirectPlay() bool { return k.dr.RetryDirectPlay() }

type kindMediaSourceDelivery struct {
	*kindMediaSource
	sd core.SourceDeliveryReporter
}

func (k *kindMediaSourceDelivery) SourceDelivery() core.SourceDelivery {
	return k.sd.SourceDelivery()
}

type kindMediaSourceDirect struct {
	*kindMediaSource
	du core.DirectURLSource
}

func (k *kindMediaSourceDirect) DirectURL() (core.DirectSourceLink, bool) {
	return k.du.DirectURL()
}

type kindMediaSourceArchiveDelivery struct {
	*kindMediaSourceArchive
	sd core.SourceDeliveryReporter
}

func (k *kindMediaSourceArchiveDelivery) SourceDelivery() core.SourceDelivery {
	return k.sd.SourceDelivery()
}

type kindMediaSourceArchive struct {
	*kindMediaSource
	avs core.ArchiveVolumeSource
}

func (k *kindMediaSourceArchive) ArchiveVolumes() ([]core.MediaSource, string, string) {
	return k.avs.ArchiveVolumes()
}
