package transcode

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// Real TCP backpressure, including retirement while the encoder's output
// producer and serving writer are both outstanding. No recorder can prove it.
func TestHTTPStallRetiresSocket(t *testing.T) {
	for _, retire := range []bool{false, true} {
		t.Run(fmt.Sprint(retire), func(t *testing.T) {
			fr := newFakeRunner()
			m := newTestManager(t, fr, config.Transcode{})
			m.opts.stallTimeout = 250 * time.Millisecond
			m.opts.httpWriteTimeout = 250 * time.Millisecond
			m.runner = retirementRunner{Runner: fr, start: func(c Command) (Process, error) {
				p := &fakeProc{done: make(chan struct{}), kill: make(chan struct{})}
				go func() {
					defer close(p.done)
					b := make([]byte, 32*1024)
					for {
						if _, err := c.Stdout.Write(b); err != nil {
							return
						}
					}
				}()
				return p, nil
			}}
			input := "http://127.0.0.1/source"
			hs, proof, err := m.StartHLSOwned(context.Background(), core.HLSRequest{SessionID: "socket", InputURL: input, InputLease: newTestEncoderInput(input, false), Info: httpInfo(), Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, SubtitleStream: -1, Container: "mkv"}, Protocol: core.ProtocolHTTP})
			if err != nil {
				t.Fatal(err)
			}
			stream := hs.(*httpSession)
			served := make(chan struct{})
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(served); stream.ServeHTTP(w, r) }))
			srv.Config.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
				_ = c.(*net.TCPConn).SetWriteBuffer(1024)
				return ctx
			}
			srv.Start()
			defer srv.Close()
			c, err := net.Dial("tcp", srv.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			_ = c.(*net.TCPConn).SetReadBuffer(1024)
			fmt.Fprint(c, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
			response, err := http.ReadResponse(bufio.NewReader(c), nil)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 200 {
				t.Fatal(response.Status)
			}
			if retire {
				time.Sleep(50 * time.Millisecond)
				assertEncoderPending(t, proof)
				_ = stream.RetireEncoder()
			}
			select {
			case <-served:
			case <-time.After(2 * time.Second):
				t.Fatal("nonreading socket retained serving goroutine")
			}
			_ = stream.RetireEncoder()
			encoderSignal(t, proof.EncoderDone())
		})
	}
}

// The response lasts several deadline budgets, and idle gaps exceed one
// budget. A deadline must neither cap total playback nor survive a Write.
func TestHTTPStallRollingSocketDeadline(t *testing.T) {
	finished := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fw := flushWriter{ResponseWriter: w, timeout: 50 * time.Millisecond}
		for i := 0; i < 4; i++ {
			if _, err := fw.Write([]byte("media\n")); err != nil {
				finished <- err
				return
			}
			time.Sleep(80 * time.Millisecond)
		}
		finished <- nil
	}))
	defer srv.Close()
	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b := make([]byte, 24)
	for i := 0; i < 4; i++ {
		if _, err := io.ReadFull(res.Body, b[i*6:(i+1)*6]); err != nil {
			t.Fatal(err)
		}
		time.Sleep(80 * time.Millisecond) // a slow reader, over multiple write budgets
	}
	if _, err := res.Body.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("stream end: %v", err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if len(b) != 24 {
		t.Fatalf("truncated playback: %q", b)
	}
}

type deadlineFlushWriter struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
	mu        sync.Mutex
}

func (w *deadlineFlushWriter) SetWriteDeadline(d time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deadlines = append(w.deadlines, d)
	return nil
}
func (w *deadlineFlushWriter) FlushError() error { return io.ErrClosedPipe }
func TestHTTPStallFlushFailureAndClear(t *testing.T) {
	w := &deadlineFlushWriter{ResponseRecorder: httptest.NewRecorder()}
	fw := httpWriteTracker{writer: flushWriter{ResponseWriter: w, timeout: time.Second}}
	if _, err := fw.Write([]byte("x")); err != io.ErrClosedPipe || !fw.failed {
		t.Fatalf("flush failure lost: %v", err)
	}
	if len(w.deadlines) != 3 || !w.deadlines[2].IsZero() {
		t.Fatalf("deadline not cleared: %v", w.deadlines)
	}
}

func TestHTTPStallDisconnectDuringStartup(t *testing.T) {
	fr := newFakeRunner()
	m := newTestManager(t, fr, config.Transcode{})
	started := make(chan struct{})
	m.runner = retirementRunner{Runner: fr, start: func(c Command) (Process, error) {
		p := &fakeProc{done: make(chan struct{}), kill: make(chan struct{})}
		go func() { <-p.kill; close(p.done) }()
		close(started)
		return p, nil
	}}
	input := "http://127.0.0.1/source"
	hs, proof, err := m.StartHLSOwned(context.Background(), core.HLSRequest{SessionID: "startup-disconnect", InputURL: input, InputLease: newTestEncoderInput(input, false), Info: httpInfo(), Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, SubtitleStream: -1, Container: "mkv"}, Protocol: core.ProtocolHTTP})
	if err != nil {
		t.Fatal(err)
	}
	stream := hs.(*httpSession)
	served := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(served); stream.ServeHTTP(w, r) }))
	defer srv.Close()
	c, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprint(c, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
	encoderSignal(t, started)
	c.Close()
	encoderSignal(t, served)
	stream.RetireEncoder()
	encoderSignal(t, proof.EncoderDone())
}
