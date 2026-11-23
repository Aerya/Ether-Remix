//go:build linux

package sockerr

import "golang.org/x/sys/unix"

func tcpSendBacklog(fd uintptr) (int, bool) {
	n, err := unix.IoctlGetInt(int(fd), unix.TIOCOUTQ)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
