package sockerr

import (
	"context"
	"net"
	"time"
)

type connContextKey struct{}

// WithConn stores the accepted network connection on an HTTP request context so
// long-lived streams (SSE, WebSocket) can arm TCP keepalive and retransmit
// timeouts that surface a silent dead peer in seconds instead of minutes.
func WithConn(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, connContextKey{}, c)
}

// ConnFromContext returns the innermost underlying net.Conn stored by WithConn,
// unwrapping TLS and listener peek wrappers.
func ConnFromContext(ctx context.Context) net.Conn {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(connContextKey{}).(net.Conn)
	return unwrapConn(c)
}

func unwrapConn(c net.Conn) net.Conn {
	for c != nil {
		u, ok := c.(interface{ NetConn() net.Conn })
		if !ok {
			return c
		}
		next := u.NetConn()
		if next == nil || next == c {
			return c
		}
		c = next
	}
	return nil
}

// EnableFastDeadPeer configures aggressive TCP keepalive probes and a bounded
// unacknowledged-retransmit timeout on the request's underlying TCP socket, if
// available. Non-TCP or in-memory test connections are left untouched.
func EnableFastDeadPeer(ctx context.Context, timeout time.Duration) {
	EnableFastDeadPeerConn(ConnFromContext(ctx), timeout)
}

// EnableFastDeadPeerConn applies fast dead-peer detection to c when c unwraps
// to a *net.TCPConn.
func EnableFastDeadPeerConn(c net.Conn, timeout time.Duration) {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	tcp, ok := unwrapConn(c).(*net.TCPConn)
	if !ok || tcp == nil {
		return
	}
	idle := max(time.Second, timeout/2)
	interval := max(time.Second, timeout/4)
	_ = tcp.SetKeepAliveConfig(net.KeepAliveConfig{
		Enable:   true,
		Idle:     idle,
		Interval: interval,
		Count:    3,
	})
	if raw, err := tcp.SyscallConn(); err == nil {
		_ = raw.Control(func(fd uintptr) {
			setTCPDeadPeerTimeout(fd, timeout)
		})
	}
}

// TCPSendBacklog returns the number of unsent bytes currently queued in the
// kernel TCP send buffer for the request's connection, when supported by the
// host OS. A non-zero backlog while writing indicates real network
// backpressure rather than an idle socket.
func TCPSendBacklog(ctx context.Context) (int, bool) {
	return TCPSendBacklogConn(ConnFromContext(ctx))
}

// TCPSendBacklogConn returns the unsent bytes queued in c's kernel TCP send
// buffer when c unwraps to a *net.TCPConn and the platform exposes it.
func TCPSendBacklogConn(c net.Conn) (int, bool) {
	tcp, ok := unwrapConn(c).(*net.TCPConn)
	if !ok || tcp == nil {
		return 0, false
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		return 0, false
	}
	var queued int
	var supported bool
	if err := raw.Control(func(fd uintptr) {
		queued, supported = tcpSendBacklog(fd)
	}); err != nil {
		return 0, false
	}
	return queued, supported
}
