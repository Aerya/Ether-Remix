//go:build linux

package sockerr

import "golang.org/x/sys/unix"

func tcpDeliveryInfo(fd uintptr) (TCPDeliveryInfo, bool) {
	var out TCPDeliveryInfo
	var ok bool

	if q, qok := tcpSendBacklog(fd); qok {
		out.BacklogBytes = q
		ok = true
	}

	info, err := unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO)
	if err != nil || info == nil {
		return out, ok
	}
	ok = true

	if out.BacklogBytes == 0 && info.Notsent_bytes > 0 {
		out.BacklogBytes = int(info.Notsent_bytes)
	}
	out.MSSBytes = info.Snd_mss
	if info.Snd_cwnd > 0 && info.Snd_mss > 0 {
		// Linux tcpi_snd_cwnd is in MSS packets, whereas tcpi_snd_wnd is in bytes.
		out.CwndBytes = uint64(info.Snd_cwnd) * uint64(info.Snd_mss)
	}
	// Linux >= 5.13 populates both tcpi_snd_wnd and tcpi_rcv_wnd. Checking
	// (Snd_wnd > 0 || Rcv_wnd > 0) distinguishes a genuine zero peer receive
	// window from older kernels that zero-fill both fields.
	if (info.Snd_wnd > 0 || info.Rcv_wnd > 0) && out.CwndBytes > 0 {
		out.WindowKnown = true
		out.RwndBytes = uint64(info.Snd_wnd)
	}
	// Linux >= 4.10 exposes cumulative microsecond chronometers.
	if info.Busy_time > 0 || info.Rwnd_limited > 0 || info.Sndbuf_limited > 0 {
		out.ChronoKnown = true
		out.BusyTimeUs = info.Busy_time
		out.RwndLimitedUs = info.Rwnd_limited
		out.SndbufLimitedUs = info.Sndbuf_limited
	}
	return out, ok
}
