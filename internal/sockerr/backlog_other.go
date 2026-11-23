//go:build !linux && !darwin

package sockerr

func tcpSendBacklog(uintptr) (int, bool) { return 0, false }
