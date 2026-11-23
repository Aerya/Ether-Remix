package frontend

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/sockerr"
)

// EnableFastDeadPeer makes the request's TCP connection notice a vanished
// client within about timeout (keepalive probes plus a bounded
// unacknowledged-retransmit time), for long-lived streams such as event feeds.
func EnableFastDeadPeer(ctx context.Context, timeout time.Duration) {
	sockerr.EnableFastDeadPeer(ctx, timeout)
}
