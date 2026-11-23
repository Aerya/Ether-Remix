package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/pluginitems"
	"github.com/braedonsaunders/ether/internal/plugins"
)

type syncConsoleFakeCaller struct {
	mu    sync.Mutex
	items []core.KindItem
}

func (f *syncConsoleFakeCaller) Call(_ context.Context, _ string, req plugins.Request) (plugins.Response, error) {
	if strings.HasSuffix(req.Path, "/changes") {
		return plugins.Response{Status: http.StatusNotFound}, nil
	}
	f.mu.Lock()
	page := core.KindPage{Items: append([]core.KindItem(nil), f.items...)}
	f.mu.Unlock()
	body, _ := json.Marshal(page)
	return plugins.Response{Status: http.StatusOK, Body: body}, nil
}

func TestLibrariesConsolePluginSyncFeedbackAndDeleteGuardChoice(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := context.Background()

	caller := &syncConsoleFakeCaller{
		items: []core.KindItem{
			{ID: "b1", Level: "book", Title: "Book 1", ETag: "1"},
			{ID: "b2", Level: "book", Title: "Book 2", ETag: "1"},
			{ID: "b3", Level: "book", Title: "Book 3", ETag: "1"},
			{ID: "b4", Level: "book", Title: "Book 4", ETag: "1"},
		},
	}

	lib, err := a.libraries.UpsertPluginLibrary(ctx, "ether.books", "books", libraries.PluginLibraryDecl{
		Name: "Books",
	})
	if err != nil {
		t.Fatalf("UpsertPluginLibrary: %v", err)
	}

	c := &librariesConsole{app: a}
	itemsSvc, _, err := c.services()
	if err != nil {
		t.Fatalf("services: %v", err)
	}
	itemsSvc.SetCaller(caller)

	decl := plugins.MediaKindMeta{
		Key:     "books",
		Label:   "Books",
		Library: plugins.KindLibraryDecl{Name: "Books"},
		Levels:  []plugins.KindLevel{{Key: "book", Label: "Books", Role: "book", Playable: true}},
		Sync:    plugins.KindSyncDecl{Mode: plugins.KindSyncPull},
	}
	if _, err := itemsSvc.UpsertKind(ctx, "ether.books", lib.ID, decl, false); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	h := c.handler()
	postSync := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/"+strconv.FormatInt(int64(lib.ID), 10)+"/sync", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// 1. Initial manual sync populates 4 books and returns 200 with outcome.
	w := postSync(`{"fullWalk":false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("initial sync status = %d body=%s", w.Code, w.Body.String())
	}
	var initResp struct {
		Upserted int                   `json:"upserted"`
		Sync     pluginitems.SyncState `json:"sync"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &initResp); err != nil {
		t.Fatal(err)
	}
	if initResp.Upserted != 4 || initResp.Sync.LastOutcome == nil {
		t.Fatalf("initial sync resp = %+v", initResp)
	}

	// 2. Walk shrinks to 1 of 4 -> 50% delete guard trips -> 409 with structured deleteGuard.
	caller.mu.Lock()
	caller.items = []core.KindItem{
		{ID: "b1", Level: "book", Title: "Book 1", ETag: "1"},
	}
	caller.mu.Unlock()

	w = postSync(`{"fullWalk":true}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("delete guard sync status = %d body=%s, want 409", w.Code, w.Body.String())
	}
	var guardResp struct {
		Code        string                      `json:"code"`
		Error       string                      `json:"error"`
		DeleteGuard pluginitems.SyncDeleteGuard `json:"deleteGuard"`
		Sync        pluginitems.SyncState       `json:"sync"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &guardResp); err != nil {
		t.Fatal(err)
	}
	if guardResp.Code != "delete_guard" || guardResp.DeleteGuard.Missing != 3 || guardResp.DeleteGuard.Total != 4 {
		t.Fatalf("guardResp = %+v", guardResp)
	}
	if !strings.Contains(guardResp.Error, "3 of 4 items were not returned") {
		t.Fatalf("expected plain-language guard message, got %q", guardResp.Error)
	}

	// 3. Owner chooses Keep existing items -> 200, guard cleared, 4 items retained.
	w = postSync(`{"keepExisting":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("keepExisting status = %d body=%s", w.Code, w.Body.String())
	}
	st, err := itemsSvc.SyncStateOf(ctx, "ether.books", "books")
	if err != nil || st.DeleteGuard != nil || st.LastError != "" {
		t.Fatalf("SyncState after keepExisting = %+v err=%v", st, err)
	}

	// 4. Owner explicitly chooses Remove the 3 missing items (force: true, fullWalk: true).
	w = postSync(`{"force":true,"fullWalk":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("force sync status = %d body=%s", w.Code, w.Body.String())
	}
	var forceResp struct {
		Deleted int                   `json:"deleted"`
		Sync    pluginitems.SyncState `json:"sync"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &forceResp); err != nil {
		t.Fatal(err)
	}
	if forceResp.Deleted != 3 {
		t.Fatalf("force Deleted = %d, want 3", forceResp.Deleted)
	}
}
