//go:build linux

package sockerr

import (
	"time"

	"golang.org/x/sys/unix"
)

func setTCPDeadPeerTimeout(fd uintptr, timeout time.Duration) {
	ms := max(1000, int(timeout/time.Millisecond))
	_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, ms)
}
