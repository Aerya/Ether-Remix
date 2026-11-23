//go:build darwin

package sockerr

import (
	"time"

	"golang.org/x/sys/unix"
)

func setTCPDeadPeerTimeout(fd uintptr, timeout time.Duration) {
	secs := max(1, int((timeout+time.Second-1)/time.Second))
	_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_RXT_CONNDROPTIME, secs)
}
