//go:build !linux && !darwin

package sockerr

func tcpDeliveryInfo(uintptr) (TCPDeliveryInfo, bool) {
	return TCPDeliveryInfo{}, false
}
