package sockerr

import (
	"net"
	"runtime"
	"testing"
	"time"
)

func TestClassifyTCPWindowLinuxChronometers(t *testing.T) {
	t.Run("rwnd-limited client pacing", func(t *testing.T) {
		got := ClassifyTCPWindow(TCPWindowStats{
			HasStart:        true,
			Start:           TCPDeliveryInfo{ChronoKnown: true, BusyTimeUs: 100_000, RwndLimitedUs: 10_000},
			HasEnd:          true,
			End:             TCPDeliveryInfo{ChronoKnown: true, BusyTimeUs: 2_100_000, RwndLimitedUs: 1_510_000},
			MaxBacklogBytes: 4 << 20,
		})
		if got != TCPStallRwnd {
			t.Fatalf("got %v, want %v", got, TCPStallRwnd)
		}
	})

	t.Run("congestion-limited including BBR where cwnd exceeds open rwnd", func(t *testing.T) {
		got := ClassifyTCPWindow(TCPWindowStats{
			HasStart:           true,
			Start:              TCPDeliveryInfo{ChronoKnown: true, BusyTimeUs: 100_000, RwndLimitedUs: 10_000},
			HasEnd:             true,
			End:                TCPDeliveryInfo{ChronoKnown: true, BusyTimeUs: 2_100_000, RwndLimitedUs: 10_000},
			MaxBacklogBytes:    512 << 10,
			WindowSamples:      3,
			RwndLimitedSamples: 3, // e.g. BBR cwnd=512KiB > rwnd=256KiB while rwnd was never actually limiting
		})
		if got != TCPStallCongestion {
			t.Fatalf("got %v, want %v", got, TCPStallCongestion)
		}
	})

	t.Run("keep-alive delta ignores historical rwnd time", func(t *testing.T) {
		// Previous 10 minutes on the same HTTP/1.1 connection were rwnd-limited,
		// but the current window after a seek had 0 new rwnd-limited microseconds.
		got := ClassifyTCPWindow(TCPWindowStats{
			HasStart:        true,
			Start:           TCPDeliveryInfo{ChronoKnown: true, BusyTimeUs: 600_000_000, RwndLimitedUs: 550_000_000},
			HasEnd:          true,
			End:             TCPDeliveryInfo{ChronoKnown: true, BusyTimeUs: 602_000_000, RwndLimitedUs: 550_000_000},
			MaxBacklogBytes: 256 << 10,
		})
		if got != TCPStallCongestion {
			t.Fatalf("got %v, want %v", got, TCPStallCongestion)
		}
	})

	t.Run("first window starting from zero chronometers", func(t *testing.T) {
		got := ClassifyTCPWindow(TCPWindowStats{
			HasStart:        true,
			Start:           TCPDeliveryInfo{}, // all counters 0 before first write on fresh socket
			HasEnd:          true,
			End:             TCPDeliveryInfo{ChronoKnown: true, BusyTimeUs: 1_800_000, RwndLimitedUs: 1_200_000},
			MaxBacklogBytes: 2 << 20,
		})
		if got != TCPStallRwnd {
			t.Fatalf("got %v, want %v", got, TCPStallRwnd)
		}
	})

	t.Run("app-limited slow source or encoder", func(t *testing.T) {
		got := ClassifyTCPWindow(TCPWindowStats{
			HasStart:        true,
			Start:           TCPDeliveryInfo{ChronoKnown: true, BusyTimeUs: 0, SndbufLimitedUs: 0},
			HasEnd:          true,
			End:             TCPDeliveryInfo{ChronoKnown: true, BusyTimeUs: 1_000_000, SndbufLimitedUs: 600_000},
			MaxBacklogBytes: 128 << 10,
		})
		if got != TCPStallAppLimited {
			t.Fatalf("got %v, want %v", got, TCPStallAppLimited)
		}
	})

	t.Run("zero peer window overrides chronometers", func(t *testing.T) {
		got := ClassifyTCPWindow(TCPWindowStats{
			HasStart:        true,
			Start:           TCPDeliveryInfo{ChronoKnown: true, BusyTimeUs: 0},
			HasEnd:          true,
			End:             TCPDeliveryInfo{ChronoKnown: true, BusyTimeUs: 2_000_000},
			MaxBacklogBytes: 512 << 10,
			ZeroWindowSeen:  true,
		})
		if got != TCPStallRwnd {
			t.Fatalf("got %v, want %v", got, TCPStallRwnd)
		}
	})
}

func TestClassifyTCPWindowSnapshotFallback(t *testing.T) {
	t.Run("rwnd smaller than cwnd is rwnd-limited", func(t *testing.T) {
		var tr TCPDeliveryTracker
		tr.recordSample(TCPDeliveryInfo{
			BacklogBytes: 256 << 10,
			WindowKnown:  true,
			MSSBytes:     1448,
			CwndBytes:    256 << 10,
			RwndBytes:    64 << 10,
		})
		if got := ClassifyTCPWindow(tr.stats); got != TCPStallRwnd {
			t.Fatalf("got %v, want %v", got, TCPStallRwnd)
		}
	})

	t.Run("repeated wide-open rwnd above cwnd with backlog is congestion", func(t *testing.T) {
		var tr TCPDeliveryTracker
		for i := 0; i < 2; i++ {
			tr.recordSample(TCPDeliveryInfo{
				BacklogBytes: 256 << 10,
				WindowKnown:  true,
				MSSBytes:     1448,
				CwndBytes:    128 << 10,
				RwndBytes:    512 << 10,
			})
		}
		if got := ClassifyTCPWindow(tr.stats); got != TCPStallCongestion {
			t.Fatalf("got %v, want %v", got, TCPStallCongestion)
		}
	})

	t.Run("inconclusive when backlog below threshold or unavailable", func(t *testing.T) {
		var tr TCPDeliveryTracker
		tr.recordSample(TCPDeliveryInfo{
			BacklogBytes: 4 << 10,
			WindowKnown:  true,
			MSSBytes:     1448,
			CwndBytes:    64 << 10,
			RwndBytes:    512 << 10,
		})
		if got := ClassifyTCPWindow(tr.stats); got != TCPStallUnknown {
			t.Fatalf("got %v, want %v", got, TCPStallUnknown)
		}
	})
}

func TestTCPDeliveryLiveSocket(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("TCP_INFO telemetry only supported on darwin and linux")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-accepted
	defer server.Close()

	// Shrink socket buffers and stop reading on client so the send queue fills
	// and the peer receive window closes.
	_ = client.(*net.TCPConn).SetReadBuffer(16 << 10)
	_ = server.(*net.TCPConn).SetWriteBuffer(16 << 10)

	buf := make([]byte, 32<<10)
	_ = server.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
	for {
		if _, err := server.Write(buf); err != nil {
			break
		}
	}

	// Only the platform contract is asserted here: whether the kernel has
	// closed the window yet depends on scheduling, so the classification is
	// covered by the chronometer and snapshot tests above.
	if _, ok := TCPDeliveryConn(server); !ok {
		t.Fatalf("TCPDeliveryConn returned ok=false on %s", runtime.GOOS)
	}
}
