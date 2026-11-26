package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/conform/openapi"
)

func TestRun(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "secret" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer s.Close()
	target := target{URL: s.URL, Headers: map[string]string{"X-Test": "secret"}}
	rows, err := run(config{Samples: 12, Concurrency: []int{1, 10}, Scenarios: []scenario{{Name: "home", Reference: target, Ether: target}}})
	if err != nil || len(rows) != 2 {
		t.Fatalf("%v %v", rows, err)
	}
	for _, r := range rows {
		if r.Reference.Errors != 0 || r.Ether.Errors != 0 || r.Reference.P95 < r.Reference.P50 {
			t.Fatalf("bad measurement: %+v", r)
		}
	}
}
func TestFailuresAreInconclusive(t *testing.T) {
	if winner(measurement{Errors: 1}, measurement{}) != "inconclusive" {
		t.Fatal("failed requests must not win")
	}
	if percentile([]float64{1, 2, 3, 4}, 95) != 4 {
		t.Fatal("wrong percentile")
	}
}

func TestMeasuresFirstByteSeparatelyFromBody(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows timer resolution is too coarse to separate first-byte from body time on a local server")
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(25 * time.Millisecond)
		_, _ = w.Write([]byte("payload"))
	}))
	defer s.Close()
	m := measure(s.Client(), target{URL: s.URL}, 3, 1)
	if m.Errors != 0 || m.TTFBP50 <= 0 || m.P50-m.TTFBP50 < 15 {
		t.Fatalf("expected distinct first-byte and body timing: %+v", m)
	}
}

func TestSchemaValidationChecksEachResponseAndBlocksWinner(t *testing.T) {
	spec := []byte(`{"openapi":"3.0.3","paths":{"/Items/{id}":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"type":"object","required":["Id"],"properties":{"Id":{"type":"string"}}}}}}}}}}}`)
	doc, err := openapi.Parse(spec)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	m := measureWithSchema(srv.Client(), target{URL: srv.URL, SpecPath: "/Items/123"}, 4, 2, doc)
	if m.Errors != 0 || m.SchemaErrors != 4 {
		t.Fatalf("missing required field was not counted: %+v", m)
	}
	if winner(measurement{}, m) != "inconclusive" {
		t.Fatal("invalid response must not win latency")
	}
}

func TestSchemaUndocumentedOperationIsUncovered(t *testing.T) {
	doc, err := openapi.Parse([]byte(`{"openapi":"3.0.3","paths":{"/known":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"type":"object"}}}}}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer s.Close()
	m := measureWithSchema(s.Client(), target{URL: s.URL, SpecPath: "/undocumented"}, 3, 1, doc)
	if m.SchemaUncovered != 3 || m.SchemaErrors != 0 {
		t.Fatalf("undocumented operation misclassified: %+v", m)
	}
	if winner(measurement{}, m) != "inconclusive" {
		t.Fatal("uncovered schema must not win")
	}
}

type scheduledTransport func(*http.Request) (*http.Response, error)

func (f scheduledTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenLoopAccountsForQueueAndTimeouts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		endpoint := target{URL: "http://example.invalid"}
		client := &http.Client{Transport: scheduledTransport(func(r *http.Request) (*http.Response, error) {
			time.Sleep(20 * time.Millisecond)
			httptrace.ContextClientTrace(r.Context()).GotFirstResponseByte()
			const body = "ok"
			return &http.Response{StatusCode: http.StatusOK, ContentLength: int64(len(body)), Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		m := measureScheduled(client, endpoint, 6, 1, nil, 1000)
		if m.Errors != 0 || m.HTTPSuccesses != 6 || m.QueueP95 <= 0 || m.AllScheduledP95 <= m.P95 {
			t.Fatalf("offered load/queue lost: %+v", m)
		}
		client.Transport = scheduledTransport(func(*http.Request) (*http.Response, error) {
			return nil, context.DeadlineExceeded
		})
		m = measureScheduled(client, endpoint, 2, 1, nil, 1000)
		if m.Timeouts != 2 || m.Errors != 2 || m.ValidSuccesses != 0 {
			t.Fatalf("timeouts omitted: %+v", m)
		}
	})
}

func TestRepetitionsAlternateServerOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	makeServer := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			_, _ = w.Write([]byte("ok"))
		}))
	}
	a, b := makeServer("reference"), makeServer("ether")
	defer a.Close()
	defer b.Close()
	rows, err := run(config{Samples: 1, Concurrency: []int{1}, Repetitions: 2, Scenarios: []scenario{{Name: "views", Reference: target{URL: a.URL}, Ether: target{URL: b.URL}}}})
	if err != nil || len(rows) != 2 || strings.Join(order, ",") != "reference,ether,ether,reference" || rows[1].Order != "ether_first" {
		t.Fatalf("order %v rows %+v err %v", order, rows, err)
	}
}

func TestQuantileUncertaintyRequiresTailSamples(t *testing.T) {
	values := make([]float64, 2000)
	for i := range values {
		values[i] = float64(i + 1)
	}
	if quantileCI(values[:100], .99) != nil {
		t.Fatal("p99 interval claimed with too few tail observations")
	}
	ci := quantileCI(values, .99)
	if ci == nil || ci.Lower > 1980 || ci.Upper < 1980 || ci.Lower >= ci.Upper {
		t.Fatalf("bad order-statistic interval: %+v", ci)
	}
}
