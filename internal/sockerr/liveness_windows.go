//go:build windows

package sockerr

import (
	"time"

	"golang.org/x/sys/windows"
)

const tcpMaxRT = 5 // TCP_MAXRT

func setTCPDeadPeerTimeout(fd uintptr, timeout time.Duration) {
	secs := max(1, int((timeout+time.Second-1)/time.Second))
	_ = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_TCP, tcpMaxRT, secs)
}
