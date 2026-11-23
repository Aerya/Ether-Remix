//go:build darwin

package sockerr

import "golang.org/x/sys/unix"

func tcpDeliveryInfo(fd uintptr) (TCPDeliveryInfo, bool) {
	var out TCPDeliveryInfo
	var ok bool

	info, err := unix.GetsockoptTCPConnectionInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_CONNECTION_INFO)
	if err == nil && info != nil {
		ok = true
		out.BacklogBytes = int(info.Snd_sbbytes)
		out.MSSBytes = info.Maxseg
		// On macOS/XNU, both tcpi_snd_cwnd and tcpi_snd_wnd are in bytes.
		out.CwndBytes = uint64(info.Snd_cwnd)
		if (info.Snd_wnd > 0 || info.Rcv_wnd > 0) && out.CwndBytes > 0 {
			out.WindowKnown = true
			out.RwndBytes = uint64(info.Snd_wnd)
		}
	}
	if !ok {
		if q, qok := tcpSendBacklog(fd); qok {
			out.BacklogBytes = q
			ok = true
		}
	}
	return out, ok
}
