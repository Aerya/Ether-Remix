package kindstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// ErrDurationMismatch is returned when a PlayableSource opts into open-time
// duration verification (DurationSec > 0 and DurationToleranceSec > 0) and
// the container's observed running time differs from DurationSec by more than
// DurationToleranceSec seconds. Errors returned by Open wrap ErrDurationMismatch,
// core.ErrWrongContent, and ErrPluginAnswer so OpenSources tries the next
// candidate and KindResolver reports the bad answer (#641).
var ErrDurationMismatch = errors.New("kindstream: container duration does not match declared duration")

const (
	// DefaultDurationProbeTimeout bounds one container probe (verify-on-open
	// and media.probe).
	DefaultDurationProbeTimeout = 10 * time.Second
	// DefaultDurationProbeBudget bounds bytes read by native container header
	// probing (MKV EBML/Info/Tracks or MP4 moov).
	DefaultDurationProbeBudget = 4 << 20
)

// SourceDurationCheckEnabled reports whether src opts into open-time container
// duration verification (both DurationSec and DurationToleranceSec are finite
// and positive).
func SourceDurationCheckEnabled(src core.PlayableSource) bool {
	return isFinitePositive(src.DurationSec) && isFinitePositive(src.DurationToleranceSec)
}

func isFinitePositive(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

type metadataReaderAt interface {
	ReadMetadataAtContext(ctx context.Context, p []byte, off int64) (int, error)
}

type probeMetadataReader struct {
	bs ByteSource
}

func (r probeMetadataReader) ReadMetadataAtContext(ctx context.Context, p []byte, off int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if mr, ok := r.bs.(metadataReaderAt); ok {
		return mr.ReadMetadataAtContext(ctx, p, off)
	}
	if cr, ok := r.bs.(core.ContextReaderAt); ok {
		return cr.ReadAtContext(ctx, p, off)
	}
	return r.bs.ReadAt(p, off)
}

func (r probeMetadataReader) ReadAt(p []byte, off int64) (int, error) {
	return r.ReadMetadataAtContext(context.Background(), p, off)
}

// unwrapForProbe returns the underlying ByteSource without any sequential
// digest-verifying wrapper so seeking to an MP4 tail moov box during header
// probing never breaks sequential digest hashing.
func unwrapForProbe(bs ByteSource) ByteSource {
	for {
		switch v := bs.(type) {
		case *verifyingByteSource:
			bs = v.ByteSource
		case *renamedByteSource:
			if inner, ok := v.ByteSource.(*verifyingByteSource); ok {
				bs = inner.ByteSource
				continue
			}
			return bs
		default:
			return bs
		}
	}
}

// ProbeByteSource reads container metadata from bs within a bounded timeout
// (DefaultDurationProbeTimeout unless ctx already has a tighter deadline).
// It probes Matroska/WebM and MP4/MOV/M4V natively via transcode.ProbeHeader
// without spawning a subprocess; when the native probe returns
// transcode.ErrProbeIncomplete and probeStream is non-nil, it falls back to
// probeStream over a private loopback listener for the remaining deadline.
func ProbeByteSource(ctx context.Context, bs ByteSource, probeStream func(ctx context.Context, inputURL string) (*core.MediaInfo, error)) (*core.MediaInfo, error) {
	if bs == nil {
		return nil, transcode.ErrProbeIncomplete
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target := unwrapForProbe(bs)
	size := target.Size()
	if size <= 0 {
		return nil, transcode.ErrProbeIncomplete
	}
	pctx, cancel := context.WithTimeout(ctx, DefaultDurationProbeTimeout)
	defer cancel()

	probeCtx := core.WithReadOrigin(pctx, "kindstream.probe", "container facts")
	info, err := transcode.ProbeHeader(probeCtx, probeMetadataReader{bs: target}, size, DefaultDurationProbeBudget)
	if err == nil && info != nil {
		fillProbeDefaults(info, size, bs.Name(), bs.MIME())
		return info, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if pctx.Err() != nil {
		return nil, pctx.Err()
	}
	if !errors.Is(err, transcode.ErrProbeIncomplete) {
		return nil, err
	}
	if probeStream == nil {
		return nil, err
	}
	info, ferr := probeViaLoopback(probeCtx, target, size, bs.Name(), probeStream)
	if ferr == nil && info != nil {
		fillProbeDefaults(info, size, bs.Name(), bs.MIME())
		return info, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if pctx.Err() != nil {
		return nil, pctx.Err()
	}
	return nil, ferr
}

func fillProbeDefaults(info *core.MediaInfo, size int64, name, mime string) {
	if info == nil {
		return
	}
	if info.Size <= 0 {
		info.Size = size
	}
	if info.Container == "" {
		info.Container = containerHint(name, mime)
	}
	info.Estimated = false
}

func containerHint(name, mime string) string {
	switch strings.ToLower(path.Ext(strings.TrimSpace(name))) {
	case ".mkv":
		return "mkv"
	case ".webm":
		return "webm"
	case ".mp4", ".m4v", ".m4a", ".m4b":
		return "mp4"
	case ".mov":
		return "mov"
	case ".mp3":
		return "mp3"
	case ".flac":
		return "flac"
	case ".ogg", ".oga", ".opus":
		return "ogg"
	case ".wav":
		return "wav"
	case ".avi":
		return "avi"
	case ".ts", ".m2ts":
		return "mpegts"
	}
	base := normalizeMIME(mime)
	switch base {
	case "video/x-matroska", "audio/x-matroska":
		return "mkv"
	case "video/webm", "audio/webm":
		return "webm"
	case "video/mp4", "audio/mp4":
		return "mp4"
	case "video/quicktime":
		return "mov"
	case "audio/mpeg":
		return "mp3"
	case "audio/flac":
		return "flac"
	case "audio/ogg":
		return "ogg"
	case "audio/wav", "audio/x-wav":
		return "wav"
	case "video/mp2t":
		return "mpegts"
	}
	return ""
}

type ctxReadSeeker struct {
	ctx     context.Context
	r       probeMetadataReader
	size    int64
	off     int64
	readErr *atomic.Pointer[error]
}

func (s *ctxReadSeeker) Read(p []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	if s.off >= s.size {
		return 0, io.EOF
	}
	rem := s.size - s.off
	if int64(len(p)) > rem {
		p = p[:rem]
	}
	n, err := s.r.ReadMetadataAtContext(s.ctx, p, s.off)
	s.off += int64(n)
	if err != nil && !errors.Is(err, io.EOF) && s.ctx.Err() == nil && s.readErr != nil {
		e := err
		s.readErr.CompareAndSwap(nil, &e)
	}
	return n, err
}

func (s *ctxReadSeeker) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = s.off + offset
	case io.SeekEnd:
		next = s.size + offset
	default:
		return 0, errors.New("kindstream: invalid whence")
	}
	if next < 0 {
		return 0, errors.New("kindstream: negative seek offset")
	}
	s.off = next
	return next, nil
}

func probeViaLoopback(ctx context.Context, bs ByteSource, size int64, name string, probeStream func(ctx context.Context, inputURL string) (*core.MediaInfo, error)) (*core.MediaInfo, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", transcode.ErrProbeIncomplete, err)
	}
	var readErr atomic.Pointer[error]
	serveName := path.Base(strings.TrimSpace(name))
	if serveName == "" || serveName == "." || serveName == "/" {
		serveName = "media"
	}
	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rs := &ctxReadSeeker{
				ctx:     r.Context(),
				r:       probeMetadataReader{bs: bs},
				size:    size,
				readErr: &readErr,
			}
			http.ServeContent(w, r, serveName, time.Time{}, rs)
		}),
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		_ = srv.Close()
		_ = ln.Close()
	}()

	info, err := probeStream(ctx, "http://"+ln.Addr().String()+"/"+serveName)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if rerr := readErr.Load(); rerr != nil {
			return nil, *rerr
		}
		return nil, fmt.Errorf("%w: %v", transcode.ErrProbeIncomplete, err)
	}
	return info, nil
}

func verifySourceDuration(ctx context.Context, src core.PlayableSource, bs ByteSource, deps Deps) error {
	if !SourceDurationCheckEnabled(src) {
		return nil
	}
	pctx, cancel := context.WithTimeout(ctx, DefaultDurationProbeTimeout)
	defer cancel()

	var (
		info *core.MediaInfo
		err  error
	)
	if deps.ProbeMedia != nil {
		info, err = deps.ProbeMedia(pctx, src, bs)
	} else {
		info, err = ProbeByteSource(pctx, bs, nil)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		if errors.Is(err, transcode.ErrProbeIncomplete) {
			return nil
		}
		return err
	}
	if info == nil || info.Duration <= 0 {
		return nil
	}
	gotSec := info.Duration.Seconds()
	diff := math.Abs(gotSec - src.DurationSec)
	if diff > src.DurationToleranceSec {
		return fmt.Errorf("%w: got %.1fs, want %.1fs (±%.1fs): %w: %w",
			ErrDurationMismatch, gotSec, src.DurationSec, src.DurationToleranceSec,
			core.ErrWrongContent, ErrPluginAnswer)
	}
	return nil
}
