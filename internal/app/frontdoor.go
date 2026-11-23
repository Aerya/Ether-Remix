package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/sockerr"
	"github.com/braedonsaunders/ether/internal/startup"
)

// frontDoor is the server's port for the whole life of Serve: it is bound
// as soon as the data directory is locked, before the database opens, and
// answers through a startup.Gate, so the console can show what a starting
// server is doing (waiting for PostgreSQL, migrating, restoring). Each App
// opens the gate when it is ready and closes it to drain before stopping;
// a cluster transition or a retried start keeps the same listener.
//
// When server.consolePort is configured, frontDoor also manages an optional
// dedicated listener for the owner console (/ether), its API, and the startup
// recovery page. If binding the dedicated console port fails, frontDoor falls
// back to the main port and logs it so the owner is never locked out.
type frontDoor struct {
	tracker *startup.Tracker
	gate    *startup.Gate
	log     *slog.Logger

	ln   net.Listener
	srv  *http.Server
	errc chan error
	// port is the port bound; moved is set when the default port was busy
	// and a fallback was taken (the App saves it once settings are open).
	port  int
	moved bool
	// tls serves TLS connections once a ready App has its certificates.
	tls atomic.Pointer[func() *tls.Config]
	// retry starts a failed start again at once (a recovery restore).
	retry chan struct{}
	// recovery serves the failed-start recovery API; replaced per start.
	recovery atomic.Pointer[http.Handler]
	// proxies are the trusted reverse-proxy CIDRs for the console's network
	// policy: from the settings on disk until a server is ready, then from
	// its live settings.
	proxies atomic.Pointer[func() []string]
	remote  atomic.Pointer[func() bool]

	// Optional dedicated owner console listener state.
	consoleMu       sync.Mutex
	consoleLn       net.Listener
	consoleSrv      *http.Server
	consoleGate     *startup.Gate
	consoleApp      http.Handler
	consolePort     int
	consoleWanted   int
	consoleBindAddr string
	consoleFallback bool
	consoleBindErr  string
	consoleOnMain   atomic.Bool
	listenConsole   func(network, address string) (net.Listener, error)
}

func newFrontDoor(tracker *startup.Tracker, log *slog.Logger) *frontDoor {
	f := &frontDoor{
		tracker: tracker,
		log:     log,
		errc:    make(chan error, 1),
		retry:   make(chan struct{}, 1),
	}
	f.consoleOnMain.Store(true)
	f.gate = &startup.Gate{
		Tracker:        f.tracker,
		Shell:          admin.Shell(),
		Console:        func(h http.Handler) http.Handler { return lanOnly(h, f.trustedProxies, f.remoteConsole) },
		DisableConsole: func() bool { return !f.consoleOnMain.Load() },
		Recovery: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if h := f.recovery.Load(); h != nil {
				(*h).ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
		}),
	}
	return f
}

func (f *frontDoor) newConsoleGate() *startup.Gate {
	return &startup.Gate{
		Tracker:      f.tracker,
		Shell:        admin.Shell(),
		Console:      func(h http.Handler) http.Handler { return lanOnly(h, f.trustedProxies, f.remoteConsole) },
		RedirectRoot: true,
		Recovery: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if h := f.recovery.Load(); h != nil {
				(*h).ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
		}),
	}
}

func (f *frontDoor) bound() bool { return f.ln != nil }

func (f *frontDoor) trustedProxies() []string {
	if fn := f.proxies.Load(); fn != nil {
		return (*fn)()
	}
	return nil
}

func (f *frontDoor) remoteConsole() bool {
	if fn := f.remote.Load(); fn != nil {
		return (*fn)()
	}
	return false
}

// setRemote sets where the remote-console opt-in comes from.
func (f *frontDoor) setRemote(fn func() bool) { f.remote.Store(&fn) }

// setProxies sets where the trusted proxy CIDRs come from.
func (f *frontDoor) setProxies(fn func() []string) { f.proxies.Store(&fn) }

// bind listens on the configured port (or a fallback, see bindPort) and
// starts serving the gate, plus the optional dedicated console port.
func (f *frontDoor) bind(server config.Server, envPort bool) error {
	ln, moved, err := bindPort(server, envPort)
	if err != nil {
		return err
	}
	cidrs, remote := server.TrustedProxyCIDRs, server.AllowRemoteConsole
	f.setProxies(func() []string { return cidrs })
	f.setRemote(func() bool { return remote })
	f.serve(ln)
	f.moved = moved
	f.reconcileConsole(server)
	return nil
}

// serve starts answering on ln.
func (f *frontDoor) serve(ln net.Listener) {
	f.ln = ln
	f.port = ln.Addr().(*net.TCPAddr).Port
	dl := newDualListener(ln, func() *tls.Config {
		if fn := f.tls.Load(); fn != nil {
			return (*fn)()
		}
		return nil // no certificates until a server is ready
	})
	f.srv = &http.Server{
		Handler:           f.gate,
		ConnContext:       sockerr.WithConn,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(f.log.Handler(), slog.LevelDebug),
	}
	go func() { f.errc <- f.srv.Serve(dl) }()
}

func (f *frontDoor) consoleAddr() net.Addr {
	f.consoleMu.Lock()
	defer f.consoleMu.Unlock()
	if f.consoleLn == nil {
		return nil
	}
	return f.consoleLn.Addr()
}

func (f *frontDoor) activeConsoleAddr() net.Addr {
	if addr := f.consoleAddr(); addr != nil {
		return addr
	}
	if f.ln != nil {
		return f.ln.Addr()
	}
	return nil
}

func (f *frontDoor) activeConsolePort() int {
	f.consoleMu.Lock()
	p := f.consolePort
	f.consoleMu.Unlock()
	if p > 0 {
		return p
	}
	return f.port
}

func (f *frontDoor) consoleStatus() admin.ConsoleListenerStatus {
	f.consoleMu.Lock()
	defer f.consoleMu.Unlock()
	return admin.ConsoleListenerStatus{
		ConfiguredPort: f.consoleWanted,
		ActivePort:     f.consolePort,
		MainPort:       f.port,
		OnMainPort:     f.consoleOnMain.Load(),
		Fallback:       f.consoleFallback,
		Error:          f.consoleBindErr,
	}
}

// reconcileConsole binds, updates, or shuts down the optional dedicated owner
// console listener according to server. If binding fails, Ether falls back to
// the main port and logs a warning so the owner is never locked out.
func (f *frontDoor) reconcileConsole(server config.Server) {
	if envCP, err := envConsolePort(); err == nil && envCP > 0 {
		server.ConsolePort = envCP
	}
	var oldSrv *http.Server
	defer func() {
		if oldSrv != nil {
			go func(s *http.Server) {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = s.Shutdown(ctx)
			}(oldSrv)
		}
	}()

	f.consoleMu.Lock()
	defer f.consoleMu.Unlock()

	if server.ConsolePort <= 0 {
		oldSrv = f.consoleSrv
		f.consoleLn = nil
		f.consoleSrv = nil
		f.consoleGate = nil
		f.consolePort = 0
		f.consoleWanted = 0
		f.consoleBindAddr = ""
		f.consoleFallback = false
		f.consoleBindErr = ""
		f.consoleOnMain.Store(true)
		return
	}

	if server.ConsolePort == f.port {
		oldSrv = f.consoleSrv
		f.consoleLn = nil
		f.consoleSrv = nil
		f.consoleGate = nil
		f.consolePort = 0
		f.consoleWanted = server.ConsolePort
		f.consoleBindAddr = ""
		f.consoleFallback = true
		f.consoleBindErr = "dedicated console port matches main server port"
		f.consoleOnMain.Store(true)
		if f.log != nil {
			f.log.Warn("dedicated console port matches main server port; falling back to main port so the console stays reachable",
				"consolePort", server.ConsolePort, "mainPort", f.port)
		}
		return
	}

	if f.consoleLn != nil && f.consoleWanted == server.ConsolePort && f.consoleBindAddr == server.BindAddress {
		f.consoleOnMain.Store(!server.DisableConsoleOnMainPort)
		return
	}

	addr := net.JoinHostPort(server.BindAddress, strconv.Itoa(server.ConsolePort))
	listen := f.listenConsole
	if listen == nil {
		listen = net.Listen
	}
	ln, err := listen("tcp", addr)
	if err != nil {
		oldSrv = f.consoleSrv
		f.consoleLn = nil
		f.consoleSrv = nil
		f.consoleGate = nil
		f.consolePort = 0
		f.consoleWanted = server.ConsolePort
		f.consoleBindAddr = ""
		f.consoleFallback = true
		f.consoleBindErr = err.Error()
		// Never lock the owner out: keep /ether reachable on the main port.
		f.consoleOnMain.Store(true)
		if f.log != nil {
			f.log.Warn("dedicated console port failed to bind; falling back to main port so the console stays reachable",
				"consolePort", server.ConsolePort, "mainPort", f.port, "err", err)
		}
		return
	}

	oldSrv = f.consoleSrv
	gate := f.newConsoleGate()
	if f.consoleApp != nil {
		gate.Open(f.consoleApp)
	}
	dl := newDualListener(ln, func() *tls.Config {
		if fn := f.tls.Load(); fn != nil {
			return (*fn)()
		}
		return nil
	})
	var errLog *slog.Logger
	if f.log != nil {
		errLog = f.log
	} else {
		errLog = slog.Default()
	}
	srv := &http.Server{
		Handler:           gate,
		ConnContext:       sockerr.WithConn,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(errLog.Handler(), slog.LevelDebug),
	}
	f.consoleLn = ln
	f.consoleSrv = srv
	f.consoleGate = gate
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		f.consolePort = tcp.Port
	} else if _, ps, perr := net.SplitHostPort(ln.Addr().String()); perr == nil {
		f.consolePort, _ = strconv.Atoi(ps)
	}
	f.consoleWanted = server.ConsolePort
	f.consoleBindAddr = server.BindAddress
	f.consoleFallback = false
	f.consoleBindErr = ""
	f.consoleOnMain.Store(!server.DisableConsoleOnMainPort)

	go func(s *http.Server, l net.Listener) {
		serveErr := s.Serve(l)
		if serveErr == nil || errors.Is(serveErr, http.ErrServerClosed) {
			return
		}
		f.consoleMu.Lock()
		if f.consoleSrv == s {
			f.consoleLn = nil
			f.consoleSrv = nil
			f.consoleGate = nil
			f.consolePort = 0
			f.consoleFallback = true
			f.consoleBindErr = serveErr.Error()
			f.consoleOnMain.Store(true)
		}
		f.consoleMu.Unlock()
		if f.log != nil {
			f.log.Warn("dedicated console listener stopped unexpectedly; falling back to main port so the console stays reachable",
				"consolePort", server.ConsolePort, "mainPort", f.port, "err", serveErr)
		}
	}(srv, dl)
}

// open opens both the main gate and (when bound) the dedicated console gate.
func (f *frontDoor) open(mainHandler, consoleHandler http.Handler) {
	f.consoleMu.Lock()
	f.consoleApp = consoleHandler
	cg := f.consoleGate
	f.consoleMu.Unlock()
	if cg != nil && consoleHandler != nil {
		cg.Open(consoleHandler)
	}
	f.gate.Open(mainHandler)
}

// closeGates drains both the main gate and (when bound) the console gate.
func (f *frontDoor) closeGates(ctx context.Context) {
	f.consoleMu.Lock()
	f.consoleApp = nil
	cg := f.consoleGate
	f.consoleMu.Unlock()
	if cg != nil {
		cg.Close(ctx)
	}
	f.gate.Close(ctx)
}

// setTLS serves TLS with the ready server's certificates.
func (f *frontDoor) setTLS(fn func() *tls.Config) { f.tls.Store(&fn) }

// close stops listening. In-flight requests were drained by Gate.Close.
func (f *frontDoor) close() {
	if f.srv != nil {
		_ = f.srv.Close()
	}
	f.consoleMu.Lock()
	cs := f.consoleSrv
	f.consoleSrv = nil
	f.consoleLn = nil
	f.consoleGate = nil
	f.consoleMu.Unlock()
	if cs != nil {
		_ = cs.Close()
	}
}

// retryNow wakes a failed start's wait.
func (f *frontDoor) retryNow() {
	select {
	case f.retry <- struct{}{}:
	default:
	}
}

// fallbackPorts are tried, in order, when the default port 32400 is already
// taken, normally by a Plex Media Server on the same machine.
var fallbackPorts = []int{32450, 32451, 32452, 32453, 32454, 32455, 32456, 32457, 32458, 32459}

// bindPort binds the configured port. When that is the default 32400 and it
// is reserved for an installed PMS or in use, Ether moves to the first free fallback port (moved), so it
// runs next to Plex out of the box; plex.tv publishes the port, so Plex
// apps follow. A port the owner chose (or ETHER_PORT, envPort) is never
// moved.
func bindPort(server config.Server, envPort bool) (net.Listener, bool, error) {
	addr := net.JoinHostPort(server.BindAddress, strconv.Itoa(server.Port))
	var ln net.Listener
	var err error
	reserved := reservePMSPort(server.Port, server.PortExplicit, envPort, installedPMS)
	if reserved {
		err = fmt.Errorf("Plex Media Server is installed; reserving port 32400")
	} else {
		ln, err = net.Listen("tcp", addr)
	}
	if err == nil {
		return ln, false, nil
	}
	if server.Port != config.DefaultPort || (!reserved && !sockerr.IsAddrInUse(err)) || envPort || server.PortExplicit {
		return nil, false, fmt.Errorf("listen %s: %w", addr, err)
	}
	for _, port := range fallbackPorts {
		alt, aerr := net.Listen("tcp", net.JoinHostPort(server.BindAddress, strconv.Itoa(port)))
		if aerr == nil {
			return alt, true, nil
		}
	}
	return nil, false, fmt.Errorf("listen %s: %w (and ports %d-%d are also in use)", addr, err, fallbackPorts[0], fallbackPorts[len(fallbackPorts)-1])
}

// listen binds the configured port for an App run without Serve's front
// door (tests), saving a fallback port like Serve does.
func (a *App) listen(cfg config.Config) (net.Listener, error) {
	ln, moved, err := bindPort(cfg.Server, os.Getenv("ETHER_PORT") != "")
	if err != nil {
		return nil, err
	}
	if moved {
		if err := a.saveMovedPort(ln.Addr().(*net.TCPAddr).Port); err != nil {
			ln.Close()
			return nil, err
		}
	}
	return ln, nil
}

// saveMovedPort records the fallback port the server moved to.
func (a *App) saveMovedPort(port int) error {
	if err := a.cfg.Update(func(c *config.Config) error { c.Server.Port = port; return nil }); err != nil {
		return fmt.Errorf("save port %d: %w", port, err)
	}
	a.log.Warn("port 32400 is reserved for Plex Media Server or already in use; Ether moved to another port and saved it",
		"port", port, "ui", fmt.Sprintf("http://localhost:%d/ether/", port))
	return nil
}

// envPort is the ETHER_PORT override, or 0.
func envPort() (int, error) {
	v := strings.TrimSpace(os.Getenv("ETHER_PORT"))
	if v == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(v)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("ETHER_PORT=%q: want a port between 1 and 65535", v)
	}
	return port, nil
}

// envConsolePort is the optional ETHER_CONSOLE_PORT override, or 0.
func envConsolePort() (int, error) {
	v := strings.TrimSpace(os.Getenv("ETHER_CONSOLE_PORT"))
	if v == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(v)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("ETHER_CONSOLE_PORT=%q: want a port between 1 and 65535", v)
	}
	return port, nil
}
