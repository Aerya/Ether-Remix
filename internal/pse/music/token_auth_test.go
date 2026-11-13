package music

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSubsonicTokenValidationAndThrottle(t *testing.T) {
	calls := 0
	s := NewServer(Options{TokenAuth: func(_ context.Context, user, token, salt string) (int64, error) {
		calls++
		if user == "listener" && token == "valid" && salt == "salt" {
			return 7, nil
		}
		return 0, errors.New("invalid")
	}})
	request := func(values url.Values) string {
		values.Set("f", "json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/rest/ping.view?"+values.Encode(), nil))
		return w.Body.String()
	}
	if body := request(url.Values{"u": {"listener"}, "t": {"valid"}}); !strings.Contains(body, `"code":10`) || calls != 0 {
		t.Fatal(body, calls)
	}
	for i := 0; i < 10; i++ {
		request(url.Values{"u": {"listener"}, "t": {"bad"}, "s": {"salt"}})
	}
	if calls != 10 {
		t.Fatal(calls)
	}
	if body := request(url.Values{"u": {"listener"}, "t": {"valid"}, "s": {"salt"}}); !strings.Contains(body, `"code":40`) || calls != 10 {
		t.Fatal("throttle bypass", body, calls)
	}
}

func TestSubsonicTokenCatalogueStreamAndViewerIsolation(t *testing.T) {
	h := newHarness(t, "LOSSLESS")
	h.srv.o.TokenAuth = func(_ context.Context, user, token, salt string) (int64, error) {
		if token != "valid" || salt != "salt" {
			return 0, errors.New("invalid")
		}
		if user == "listener" {
			return 7, nil
		}
		if user == "other" {
			return 8, nil
		}
		return 0, errors.New("invalid")
	}
	p := url.Values{"u": {"listener"}, "t": {"valid"}, "s": {"salt"}, "f": {"json"}, "id": {"tr-100"}}
	if r := h.raw("star.view", p, nil); !strings.Contains(r.Body.String(), `"status":"ok"`) {
		t.Fatal(r.Body.String())
	}
	if r := h.raw("getStarred2.view", p, nil); !strings.Contains(r.Body.String(), "One More Time") {
		t.Fatal(r.Body.String())
	}
	if r := h.raw("stream.view", p, nil); r.Code != 200 || r.Body.String() != string(audio) {
		t.Fatal("token stream failed", r.Code)
	}
	p.Set("u", "other")
	if r := h.raw("getStarred2.view", p, nil); strings.Contains(r.Body.String(), "One More Time") || !strings.Contains(r.Body.String(), `"status":"ok"`) {
		t.Fatal("favourites leaked", r.Body.String())
	}
	p.Del("f")
	if r := h.raw("ping.view", p, nil); !strings.Contains(r.Body.String(), `status="ok"`) {
		t.Fatal("XML", r.Body.String())
	}
}
