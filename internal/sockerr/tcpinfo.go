package sockerr

import (
	"context"
	"net"
)

// TCPStallKind classifies why TCP writes stalled or experienced backpressure
// across a delivery observation window.
type TCPStallKind uint8

const (
	// TCPStallUnknown means TCP_INFO was unavailable (e.g. Windows, non-TCP,
	// or older kernels) or could not conclusively classify the window.
	TCPStallUnknown TCPStallKind = iota
	// TCPStallCongestion means TCP had send-queue saturation and was limited by
	// network congestion / pacing rather than peer receive window or local app.
	TCPStallCongestion
	// TCPStallRwnd means TCP was limited by the peer's advertised receive
	// window (client-paced playback / full player buffer / small client recvbuf).
	TCPStallRwnd
	// TCPStallAppLimited means TCP had cwnd and rwnd headroom and was waiting on
	// the local application (slow upstream media source or encoder, not uplink).
	TCPStallAppLimited
)

func (k TCPStallKind) String() string {
	switch k {
	case TCPStallCongestion:
		return "congestion"
	case TCPStallRwnd:
		return "rwnd"
	case TCPStallAppLimited:
		return "app_limited"
	default:
		return "unknown"
	}
}

const (
	// Minimum kernel busy time in a window for Linux TCP_INFO chronometer
	// ratios to be meaningful (avoids noise on tiny sub-50ms bursts).
	minTCPChronoBusyUs = 50_000

	// Fraction of busy time spent receiver-window-limited above which the
	// window is contaminated by client pacing and never represents uplink cap.
	rwndLimitedPct = 20

	// Fraction of busy time spent send-buffer-limited above which the window
	// was paced by the local application (source/encoder) rather than uplink.
	sndbufLimitedPct = 30

	// Minimum kernel send-queue depth confirming active send-path saturation.
	tcpBackpressureBytes = 32 << 10
)

// TCPDeliveryInfo is a point-in-time snapshot of TCP send-path state.
type TCPDeliveryInfo struct {
	// BacklogBytes is the bytes currently queued in the kernel TCP send buffer.
	BacklogBytes int

	// WindowKnown is true when both CwndBytes and RwndBytes were reported.
	WindowKnown bool
	// CwndBytes is the current congestion window in bytes.
	CwndBytes uint64
	// RwndBytes is the peer's advertised receive window (snd_wnd) in bytes.
	RwndBytes uint64
	// MSSBytes is the connection's send MSS in bytes.
	MSSBytes uint32

	// ChronoKnown is true when the OS exposes cumulative TCP_INFO chronometers
	// (Linux >= 4.10: tcpi_busy_time, tcpi_rwnd_limited, tcpi_sndbuf_limited).
	ChronoKnown bool
	// BusyTimeUs is cumulative microseconds TCP was busy sending or awaiting ACKs.
	BusyTimeUs uint64
	// RwndLimitedUs is cumulative microseconds limited by peer receive window.
	RwndLimitedUs uint64
	// SndbufLimitedUs is cumulative microseconds limited by local send buffer.
	SndbufLimitedUs uint64
}

// TCPWindowStats holds collected TCP_INFO snapshots across one delivery window.
type TCPWindowStats struct {
	HasStart           bool
	Start              TCPDeliveryInfo
	HasEnd             bool
	End                TCPDeliveryInfo
	MaxBacklogBytes    int
	WindowSamples      int
	ZeroWindowSeen     bool
	RwndLimitedSamples int
	CwndLimitedSamples int
}

// TCPDelivery returns a point-in-time TCP_INFO snapshot for the request's
// underlying TCP socket when supported by the host OS.
func TCPDelivery(ctx context.Context) (TCPDeliveryInfo, bool) {
	return TCPDeliveryConn(ConnFromContext(ctx))
}

// TCPDeliveryConn returns a point-in-time TCP_INFO snapshot when c unwraps to
// a *net.TCPConn and the platform exposes TCP delivery telemetry.
func TCPDeliveryConn(c net.Conn) (TCPDeliveryInfo, bool) {
	tcp, ok := unwrapConn(c).(*net.TCPConn)
	if !ok || tcp == nil {
		return TCPDeliveryInfo{}, false
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		return TCPDeliveryInfo{}, false
	}
	var info TCPDeliveryInfo
	var supported bool
	if err := raw.Control(func(fd uintptr) {
		info, supported = tcpDeliveryInfo(fd)
	}); err != nil {
		return TCPDeliveryInfo{}, false
	}
	return info, supported
}

func subU64(after, before uint64) uint64 {
	if after <= before {
		return 0
	}
	return after - before
}

func isSnapshotZeroWindow(info TCPDeliveryInfo) bool {
	if !info.WindowKnown {
		return false
	}
	if info.RwndBytes == 0 {
		return true
	}
	return info.MSSBytes > 0 && info.RwndBytes <= uint64(info.MSSBytes)*2
}

func isSnapshotCwndLimited(info TCPDeliveryInfo) bool {
	if !info.WindowKnown || info.CwndBytes == 0 || isSnapshotZeroWindow(info) {
		return false
	}
	// Require unambiguous peer receive-window headroom above cwnd so a
	// borderline rwnd ≈ cwnd snapshot is not misclassified as congestion.
	margin := max(info.CwndBytes/4, uint64(info.MSSBytes)*2)
	return info.RwndBytes >= info.CwndBytes+margin
}

// ClassifyTCPWindow determines whether a delivery write window was
// congestion-limited, receiver-window-limited (client-paced), app-limited, or
// unknown/inconclusive.
func ClassifyTCPWindow(st TCPWindowStats) TCPStallKind {
	// Primary path (Linux >= 4.10): authoritative kernel chronometer deltas
	// tracked continuously in tcp_write_xmit (handles both CUBIC and BBR).
	if st.HasStart && st.HasEnd && st.End.ChronoKnown {
		dBusy := subU64(st.End.BusyTimeUs, st.Start.BusyTimeUs)
		dRwnd := subU64(st.End.RwndLimitedUs, st.Start.RwndLimitedUs)
		dSndbuf := subU64(st.End.SndbufLimitedUs, st.Start.SndbufLimitedUs)
		if dBusy >= minTCPChronoBusyUs {
			if dRwnd*100 >= dBusy*rwndLimitedPct || st.ZeroWindowSeen {
				return TCPStallRwnd
			}
			if dSndbuf*100 >= dBusy*sndbufLimitedPct {
				return TCPStallAppLimited
			}
			if st.MaxBacklogBytes >= tcpBackpressureBytes {
				return TCPStallCongestion
			}
			return TCPStallUnknown
		}
	}

	// Fallback path (macOS TCP_CONNECTION_INFO or Linux without chronometers):
	// compare peer advertised rwnd against cwnd across in-window snapshots.
	if st.WindowSamples > 0 {
		if st.ZeroWindowSeen || st.RwndLimitedSamples > 0 {
			return TCPStallRwnd
		}
		if st.CwndLimitedSamples >= 2 && st.MaxBacklogBytes >= tcpBackpressureBytes {
			return TCPStallCongestion
		}
	}

	return TCPStallUnknown
}

// TCPDeliveryTracker samples TCP_INFO across one delivery window (e.g. a 2s
// direct-play meter window or one HLS segment response) and classifies why
// writes blocked, rolling the chronometer baseline forward across windows on
// keep-alive or long-lived connections.
type TCPDeliveryTracker struct {
	stats TCPWindowStats
}

// Begin captures the window baseline snapshot if none has been captured yet.
func (t *TCPDeliveryTracker) Begin(ctx context.Context) {
	if t == nil || t.stats.HasStart {
		return
	}
	if info, ok := TCPDelivery(ctx); ok {
		t.stats.HasStart = true
		t.stats.Start = info
		if info.BacklogBytes > t.stats.MaxBacklogBytes {
			t.stats.MaxBacklogBytes = info.BacklogBytes
		}
	}
}

// Sample records an in-window TCP_INFO observation.
func (t *TCPDeliveryTracker) Sample(ctx context.Context) {
	if t == nil {
		return
	}
	info, ok := TCPDelivery(ctx)
	if !ok {
		return
	}
	if !t.stats.HasStart {
		t.stats.HasStart = true
		t.stats.Start = info
	}
	t.recordSample(info)
}

func (t *TCPDeliveryTracker) recordSample(info TCPDeliveryInfo) {
	if info.BacklogBytes > t.stats.MaxBacklogBytes {
		t.stats.MaxBacklogBytes = info.BacklogBytes
	}
	if info.WindowKnown {
		t.stats.WindowSamples++
		if isSnapshotZeroWindow(info) {
			t.stats.ZeroWindowSeen = true
			t.stats.RwndLimitedSamples++
		} else if info.CwndBytes > 0 && info.RwndBytes < info.CwndBytes {
			t.stats.RwndLimitedSamples++
		} else if isSnapshotCwndLimited(info) {
			t.stats.CwndLimitedSamples++
		}
	}
}

// Finish closes the current window, returning its peak send-queue backlog and
// stall classification, and advances the baseline for the next window on the
// same TCP connection.
func (t *TCPDeliveryTracker) Finish(ctx context.Context) (maxBacklog int, kind TCPStallKind) {
	if t == nil {
		return 0, TCPStallUnknown
	}
	if info, ok := TCPDelivery(ctx); ok {
		t.stats.HasEnd = true
		t.stats.End = info
		t.recordSample(info)
	}
	maxBacklog = t.stats.MaxBacklogBytes
	kind = ClassifyTCPWindow(t.stats)

	next := TCPWindowStats{}
	if t.stats.HasEnd {
		next.HasStart = true
		next.Start = t.stats.End
	}
	t.stats = next
	return maxBacklog, kind
}
