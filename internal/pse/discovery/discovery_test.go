package discovery

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/core"
	"net/http/httptest"
	"testing"
)

type fakeCollections struct {
	specs []core.CollectionSpec
	fail  int
}

func (f *fakeCollections) ListSpecs(context.Context) ([]core.CollectionSpec, error) {
	return f.specs, nil
}
func (f *fakeCollections) CreateSpec(_ context.Context, s core.CollectionSpec) (*core.CollectionSpec, error) {
	if f.fail > 0 && len(f.specs)+1 == f.fail {
		return nil, fmt.Errorf("failure")
	}
	s.ID = fmt.Sprint(len(f.specs) + 1)
	f.specs = append(f.specs, s)
	return &s, nil
}
func TestRetryRestoreAndPreserve(t *testing.T) {
	c := &fakeCollections{fail: 2}
	enabled := false
	s := New(c, func() bool { return enabled }, func(v bool) error { enabled = v; return nil })
	req := Request{Cards: []string{"a24", "action"}, Region: "FR"}
	if _, err := s.Apply(context.Background(), req); err == nil || enabled {
		t.Fatal("failed import enabled home", err)
	}
	c.fail = 0
	c.specs[0].Name = "My edited A24"
	for range 2 {
		if _, err := s.Apply(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.specs) != 4 || c.specs[0].Name != "My edited A24" || !enabled {
		t.Fatal(c.specs, enabled)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("DELETE", "/", nil))
	if w.Code != 200 || enabled || len(c.specs) != 4 {
		t.Fatal("restore changed collections", w.Code)
	}
	if _, e := s.Apply(context.Background(), Request{Cards: []string{"bad"}, Region: "FR"}); e == nil {
		t.Fatal("unknown card accepted")
	}
}
func TestAllPresetSpecsValidate(t *testing.T) {
	for n, c := range Cards {
		for _, typ := range []string{"movie", "show"} {
			sp := Spec(c, n, typ, "FR")
			collections.Normalize(&sp)
			if err := collections.Validate(&sp); err != nil {
				t.Fatalf("%s %s: %v", c.ID, typ, err)
			}
		}
	}
}
