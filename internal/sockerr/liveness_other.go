//go:build !darwin && !linux && !windows

package sockerr

import "time"

func setTCPDeadPeerTimeout(uintptr, time.Duration) {}
