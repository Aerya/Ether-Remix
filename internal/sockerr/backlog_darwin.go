//go:build darwin

package sockerr

import "golang.org/x/sys/unix"

func tcpSendBacklog(fd uintptr) (int, bool) {
	n, err := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_NWRITE)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
