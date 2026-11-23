package mediabrowser

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/safety"
	"github.com/braedonsaunders/ether/internal/store"
)

type bookLimitPolicy struct{ *safety.Service }

func (bookLimitPolicy) SetMaxRating(context.Context, *frontend.Viewer, string) error { return nil }

type bookLimitKindStreams struct {
	openFn func(context.Context, *core.Viewer, core.RatingKey, []string) (*frontend.KindMedia, error)
}

func (k bookLimitKindStreams) OpenKind(ctx context.Context, v *core.Viewer, key core.RatingKey, accept []string) (*frontend.KindMedia, error) {
	return k.openFn(ctx, v, key, accept)
}
func (k bookLimitKindStreams) KindLeaves(context.Context, *core.Viewer, core.RatingKey) ([]frontend.KindLeaf, error) {
	return nil, nil
}

func TestBookDownloadEnforcesDownloadLimits(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc, err := safety.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	friend := &frontend.Viewer{ID: 7, Username: "reader"}
	if err := svc.Put(ctx, friend.ID, safety.Policy{
		DownloadLimitUnit:   "titles",
		DownloadLimitAmount: 1,
		DownloadLimitPeriod: "day",
	}); err != nil {
		t.Fatal(err)
	}

	book1 := core.PluginKey(101)
	book2 := core.PluginKey(102)
	canRead := true
	items := map[core.RatingKey]frontend.ItemDetail{
		book1: {Title: core.Title{Key: book1, Type: core.TypeMovie, Title: "Book One", MediaClass: core.MediaClassBook, MBItemType: "Book"}, CanRead: &canRead},
		book2: {Title: core.Title{Key: book2, Type: core.TypeMovie, Title: "Book Two", MediaClass: core.MediaClassBook, MBItemType: "Book"}, CanRead: &canRead},
	}
	pdfBytes := []byte("%PDF-1.7\n1 0 obj<<>>endobj\n")

	host := frontend.Host{
		Identity: frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) {
			return friend, nil
		}},
		Experience: frontendtest.Experience{ItemFunc: func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
			d, ok := items[req.Key]
			if !ok {
				return frontend.ItemDetail{}, frontend.ErrNotFound
			}
			return d, nil
		}},
		KindStreams: bookLimitKindStreams{openFn: func(_ context.Context, _ *core.Viewer, key core.RatingKey, _ []string) (*frontend.KindMedia, error) {
			return &frontend.KindMedia{
				Name:     key.String() + ".pdf",
				Size:     int64(len(pdfBytes)),
				ReaderAt: bytes.NewReader(pdfBytes),
			}, nil
		}},
		ViewerPolicy: bookLimitPolicy{svc},
	}

	jf, err := (Jellyfin{MachineID: "mb-dl-limit"}).Mount(ctx, host)
	if err != nil {
		t.Fatal(err)
	}

	// First book download succeeds.
	rec1 := httptest.NewRecorder()
	jf.Handler().ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/Items/"+ids.RatingKeyGUID(book1)+"/Download?api_key=grant", nil))
	if rec1.Code != http.StatusOK {
		t.Fatalf("first book download = %d %s", rec1.Code, rec1.Body.String())
	}

	// Second distinct book hits the daily download limit (429 + clear reset wording).
	rec2 := httptest.NewRecorder()
	jf.Handler().ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/Items/"+ids.RatingKeyGUID(book2)+"/Download?api_key=grant", nil))
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second book download = %d %s, want 429", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), "You've reached your download limit for today") || !strings.Contains(rec2.Body.String(), "resets") {
		t.Fatalf("429 message = %q", rec2.Body.String())
	}

	// Inline /File reading is not a download and stays allowed.
	recInline := httptest.NewRecorder()
	jf.Handler().ServeHTTP(recInline, httptest.NewRequest(http.MethodGet, "/Items/"+ids.RatingKeyGUID(book2)+"/File?api_key=grant", nil))
	if recInline.Code != http.StatusOK {
		t.Fatalf("inline book /File = %d %s, want 200", recInline.Code, recInline.Body.String())
	}
}
