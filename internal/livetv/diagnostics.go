package livetv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// TuneInputFailure retains a safe per-input startup diagnosis.
type TuneInputFailure struct {
	Input          string `json:"input"`
	Code           string `json:"code"`
	Stage          string `json:"stage"`
	UpstreamStatus int    `json:"upstream_status,omitempty"`
}

func inputFailure(id string, err error) TuneInputFailure {
	d := Diagnose("source_start", err)
	return TuneInputFailure{Input: id, Code: d.Code, Stage: d.Stage, UpstreamStatus: d.UpstreamStatus}
}

// TuneError retains machine-readable evidence without provider URLs, bodies or
// headers. Unwrap preserves cancellation, availability and stream-limit handling.
type TuneError struct {
	Inputs         []TuneInputFailure
	Code           string
	Stage          string
	UpstreamStatus int
	RetryAfter     time.Duration
	cause          error
}

func (e *TuneError) Unwrap() error { return e.cause }
func (e *TuneError) Error() string {
	if e.UpstreamStatus > 0 {
		return fmt.Sprintf("The channel source isn't responding (HTTP %d from the provider).", e.UpstreamStatus)
	}
	switch e.Code {
	case "provider_busy":
		return "All connections allowed by this Live TV provider are in use."
	case "provider_paced":
		return "This Live TV source has reached its configured stream-start limit."
	case "source_cooldown":
		return "This Live TV source is cooling down after a failed connection."
	case "provider_auth":
		return "The Live TV provider rejected the stream credentials or access."
	case "provider_channel_missing":
		return "The Live TV provider could not find this channel."
	case "provider_rate_limited":
		return "The Live TV provider is limiting stream requests."
	case "provider_http":
		return "The Live TV provider returned an unsuccessful stream response."
	case "timeout":
		return "The channel source isn't responding (the provider took too long)."
	case "cancelled":
		return "The Live TV request was cancelled."
	case "dns":
		return "The Live TV provider address could not be resolved."
	case "network":
		return "The connection to the Live TV provider failed."
	case "stream_limit":
		return "The viewer has reached their simultaneous stream limit."
	case "playback_capacity":
		return "The server has reached its concurrent transcode capacity."
	case "forbidden":
		return "The viewer is not allowed to play this live channel."
	case "not_found":
		return "This live channel or session is no longer available."
	case "no_video":
		return "The Live TV source did not provide a usable video stream."
	case "unsupported":
		return "The Live TV source returned an unsupported stream format."
	default:
		return "The live channel could not be started."
	}
}

var providerHTTPFailure = regexp.MustCompile(`(?:HTTP error |Server returned )([1-5][0-9]{2})\b`)

// Diagnose never copies an upstream error's text: net/url and ffmpeg errors
// commonly include credentials in path components, not just query parameters.
func Diagnose(stage string, err error) *TuneError {
	var existing *TuneError
	if errors.As(err, &existing) {
		return existing
	}
	e := &TuneError{Code: "source_unavailable", Stage: stage, cause: err}
	var dns *net.DNSError
	var network net.Error
	switch {
	case errors.Is(err, context.Canceled):
		e.Code = "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		e.Code, e.RetryAfter = "timeout", time.Second
	case errors.Is(err, ErrProviderBusy):
		e.Code, e.RetryAfter = "provider_busy", 5*time.Second
	case errors.Is(err, ErrProviderPaced):
		e.Code = "provider_paced"
	case errors.Is(err, core.ErrStreamLimit):
		e.Code, e.RetryAfter = "stream_limit", 5*time.Second
	case errors.Is(err, transcode.ErrTooManyTranscodes):
		e.Code, e.RetryAfter = "playback_capacity", 5*time.Second
	case errors.Is(err, core.ErrUnauthorized):
		e.Code = "forbidden"
	case errors.Is(err, core.ErrNotFound):
		e.Code = "not_found"
	case errors.As(err, &dns):
		e.Code = "dns"
	case errors.As(err, &network):
		e.Code = "network"
		if network.Timeout() {
			e.Code = "timeout"
		}
	case errors.Is(err, errRelayUnsupported):
		e.Code = "unsupported"
	default:
		// ffmpeg reports transport errors as text. Extract only a known status
		// number or classification; never retain its URLs, headers or stderr.
		if err != nil {
			if match := providerHTTPFailure.FindStringSubmatch(err.Error()); len(match) == 2 {
				status, _ := strconv.Atoi(match[1])
				e = providerResponseError(status)
				e.Stage, e.cause = stage, err
			} else {
				text := strings.ToLower(err.Error())
				if strings.Contains(text, "timed out") {
					e.Code = "timeout"
				} else if strings.Contains(text, "connection refused") {
					e.Code = "network"
				}
			}
		}
	}
	return e
}

func providerResponseError(status int) *TuneError {
	e := &TuneError{Code: "provider_http", Stage: "upstream_headers", UpstreamStatus: status, cause: core.ErrNotAvailable}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		e.Code = "provider_auth"
	case http.StatusNotFound, http.StatusGone:
		e.Code = "provider_channel_missing"
	case http.StatusTooManyRequests:
		e.Code = "provider_rate_limited"
	}
	return e
}

func (e *TuneError) LogAttrs() []any {
	return []any{"failure_code", e.Code, "stage", e.Stage, "upstream_status", e.UpstreamStatus, "retry_after", e.RetryAfter, "inputs", e.Inputs}
}

func (s *Service) logger() *slog.Logger {
	if s.o.Logger != nil {
		return s.o.Logger
	}
	return slog.Default().With("component", "livetv")
}

func (s *Service) sessionLog(ss *session) *slog.Logger {
	return s.logger().With("session", ss.info.ID, "channel", ss.info.Channel.ID,
		"source", ss.info.Channel.SourceID, "client", ss.caps.ClientID, "product", ss.caps.Product)
}

// observeMedia forwards streaming writes without buffering. It records the
// first successful media response separately from a request or a ready encoder.
// Repeated failures are bounded to one warning per session per five seconds.
type mediaObservation struct {
	http.ResponseWriter
	s           *Service
	ss          *session
	started     time.Time
	status      int
	bytes       int
	credited    int
	writeFailed bool
	continuous  bool
}

func (w *mediaObservation) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *mediaObservation) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *mediaObservation) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	if w.bytes == 0 && n > 0 && err == nil {
		w.ss.mu.Lock()
		first := !w.ss.firstMediaByte
		w.ss.firstMediaByte = true
		started := w.ss.tuneStartedAt
		w.ss.mu.Unlock()
		if first && !started.IsZero() {
			w.s.sessionLog(w.ss).Debug("live first media byte delivered", "sinceTune", time.Since(started), "requestElapsed", time.Since(w.started))
		}
	}
	w.bytes += n
	w.writeFailed = w.writeFailed || err != nil
	if n > 0 && err == nil && w.status >= 200 && w.status < 300 {
		var firstContinuous bool
		var snap core.PlaySession
		obsNow := w.s.clockNow()
		w.ss.mu.Lock()
		w.ss.bytesServed += int64(n)
		w.credited += n
		if w.continuous {
			w.ss.last = obsNow
			w.ss.mediaStarted = true
			if !w.ss.firstMediaDelivered {
				w.ss.firstMediaDelivered = true
				w.ss.firstDeliveredAt = obsNow
				firstContinuous = true
				snap = w.s.sessionSnapshotLocked(w.ss, obsNow)
			}
		}
		w.ss.mu.Unlock()
		if firstContinuous {
			w.s.sessionLog(w.ss).Info("live first media response delivered", "status", w.status, "bytes", w.bytes, "elapsed", time.Since(w.started), "write_failed", w.writeFailed)
			w.s.observeSession(&snap, false, "")
		}
	}
	return n, err
}
func (w *mediaObservation) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (s *Service) observeMedia(w http.ResponseWriter, ss *session) *mediaObservation {
	return &mediaObservation{ResponseWriter: w, s: s, ss: ss, started: time.Now()}
}
func (s *Service) observeContinuousMedia(w http.ResponseWriter, ss *session) *mediaObservation {
	return &mediaObservation{ResponseWriter: w, s: s, ss: ss, started: time.Now(), continuous: true}
}
func (w *mediaObservation) done() {
	ss := w.ss
	success := w.status >= 200 && w.status < 300 && w.bytes > 0 && !w.writeFailed
	failed := w.status >= 400 || w.writeFailed
	now := time.Now()
	var snap core.PlaySession
	ss.mu.Lock()
	if rem := w.bytes - w.credited; rem > 0 {
		ss.bytesServed += int64(rem)
		w.credited = w.bytes
	}
	first := success && !ss.firstMediaDelivered
	if first {
		obsNow := w.s.clockNow()
		ss.firstMediaDelivered = true
		ss.firstDeliveredAt = obsNow
		snap = w.s.sessionSnapshotLocked(ss, obsNow)
	}
	warn := failed && (ss.lastMediaWarning.IsZero() || now.Sub(ss.lastMediaWarning) >= 5*time.Second)
	if warn {
		ss.lastMediaWarning = now
	}
	ss.mu.Unlock()
	attrs := []any{"status", w.status, "bytes", w.bytes, "elapsed", time.Since(w.started), "write_failed", w.writeFailed}
	if first {
		w.s.sessionLog(ss).Info("live first media response delivered", attrs...)
		w.s.observeSession(&snap, false, "")
	}
	if warn {
		w.s.sessionLog(ss).Warn("live media response failed", attrs...)
	}
}
