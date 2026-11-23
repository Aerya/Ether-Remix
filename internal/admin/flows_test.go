package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/flows/actions"
	"github.com/braedonsaunders/ether/internal/notify"
)

// fakeFlowStore is an in-memory flows.Store for API tests.
type fakeFlowStore struct {
	mu     sync.Mutex
	flows  map[string]*flows.Flow
	hashes map[string]string // token hash -> flow ID
	runs   map[string]*flows.Run
	steps  map[string][]flows.RunStep
}

func newFakeFlowStore() *fakeFlowStore {
	return &fakeFlowStore{
		flows:  map[string]*flows.Flow{},
		hashes: map[string]string{},
		runs:   map[string]*flows.Run{},
		steps:  map[string][]flows.RunStep{},
	}
}

func (s *fakeFlowStore) ListFlows(ctx context.Context) ([]flows.Flow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]flows.Flow, 0, len(s.flows))
	for _, f := range s.flows {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *fakeFlowStore) GetFlow(ctx context.Context, id string) (*flows.Flow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.flows[id]
	if !ok {
		return nil, flows.ErrNotFound
	}
	cp := *f
	return &cp, nil
}

func (s *fakeFlowStore) SaveFlow(ctx context.Context, f flows.Flow) (*flows.Flow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Unix(1_700_000_000, 0).UTC()
	if existing, ok := s.flows[f.ID]; ok {
		f.Version = existing.Version + 1
		f.CreatedAt = existing.CreatedAt
	} else {
		f.Version = 1
		f.CreatedAt = now
	}
	f.UpdatedAt = now
	cp := f
	s.flows[f.ID] = &cp
	out := cp
	return &out, nil
}

func (s *fakeFlowStore) DeleteFlow(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.flows[id]; !ok {
		return flows.ErrNotFound
	}
	delete(s.flows, id)
	for h, fid := range s.hashes {
		if fid == id {
			delete(s.hashes, h)
		}
	}
	return nil
}

func (s *fakeFlowStore) SetFlowEnabled(ctx context.Context, id string, enabled bool, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.flows[id]
	if !ok {
		return flows.ErrNotFound
	}
	f.Enabled = enabled
	if enabled {
		f.DisabledReason = ""
		f.ConsecutiveFailures = 0
	} else {
		f.DisabledReason = reason
	}
	return nil
}

func (s *fakeFlowStore) SetWebhookTokenHash(ctx context.Context, flowID, tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.flows[flowID]; !ok {
		return flows.ErrNotFound
	}
	for h, fid := range s.hashes {
		if fid == flowID {
			delete(s.hashes, h)
		}
	}
	s.hashes[tokenHash] = flowID
	return nil
}

func (s *fakeFlowStore) FindFlowByWebhookHash(ctx context.Context, tokenHash string) (*flows.Flow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.hashes[tokenHash]
	if !ok {
		return nil, flows.ErrNotFound
	}
	f := s.flows[id]
	cp := *f
	return &cp, nil
}

func (s *fakeFlowStore) CreateRun(ctx context.Context, run flows.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := run
	s.runs[run.ID] = &cp
	return nil
}

func (s *fakeFlowStore) UpdateRun(ctx context.Context, run flows.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := run
	s.runs[run.ID] = &cp
	return nil
}

func (s *fakeFlowStore) GetRun(ctx context.Context, runID string) (*flows.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return nil, flows.ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (s *fakeFlowStore) ListRuns(ctx context.Context, filter flows.RunFilter) ([]flows.Run, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []flows.Run
	for _, r := range s.runs {
		if filter.FlowID != "" && r.FlowID != filter.FlowID {
			continue
		}
		if filter.Status != "" && r.Status != filter.Status {
			continue
		}
		if filter.EventType != "" && r.EventType != filter.EventType {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	offset := 0
	if filter.Cursor != "" {
		offset, _ = strconv.Atoi(filter.Cursor)
	}
	if offset > len(out) {
		offset = len(out)
	}
	out = out[offset:]
	next := ""
	if filter.Limit > 0 && len(out) > filter.Limit {
		next = strconv.Itoa(offset + filter.Limit)
		out = out[:filter.Limit]
	}
	return out, next, nil
}

func (s *fakeFlowStore) RecordStep(ctx context.Context, step flows.RunStep) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps[step.RunID] = append(s.steps[step.RunID], step)
	return int64(len(s.steps[step.RunID])), nil
}

func (s *fakeFlowStore) UpdateStep(ctx context.Context, step flows.RunStep) error { return nil }

func (s *fakeFlowStore) ListRunSteps(ctx context.Context, runID string) ([]flows.RunStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]flows.RunStep(nil), s.steps[runID]...), nil
}

func (s *fakeFlowStore) ClaimDueRuns(ctx context.Context, owner string, now time.Time, lease time.Duration, limit int) ([]flows.Run, error) {
	return nil, nil
}
func (s *fakeFlowStore) CreateWaiter(ctx context.Context, w flows.Waiter) error { return nil }
func (s *fakeFlowStore) MatchWaiters(ctx context.Context, eventType, correlationKey string, now time.Time) ([]flows.Waiter, error) {
	return nil, nil
}
func (s *fakeFlowStore) ExpireWaiters(ctx context.Context, now time.Time, limit int) ([]flows.Waiter, error) {
	return nil, nil
}
func (s *fakeFlowStore) RecordWindowEvent(ctx context.Context, flowID, nodeID, groupKey string, at time.Time, window time.Duration, resetAt int) (int, bool, error) {
	return 1, false, nil
}
func (s *fakeFlowStore) CheckDedupe(ctx context.Context, flowID, nodeID, dedupeKey string, at time.Time, cooldown time.Duration, maxPerWindow int) (bool, int, error) {
	return true, 1, nil
}
func (s *fakeFlowStore) PruneHistory(ctx context.Context, olderThan time.Time, maxRuns int) (int64, error) {
	return 0, nil
}

// fakeFlowRuntime records trigger calls and creates runs in the store.
type fakeFlowRuntime struct {
	store       *fakeFlowStore
	seq         int
	webhookGot  []map[string]any
	manualErr   error
	lastTestReq flows.TestRunRequest
}

func (rt *fakeFlowRuntime) Start(ctx context.Context) error { return nil }

func (rt *fakeFlowRuntime) newRun(flowID string, kind flows.NodeKind) *flows.Run {
	rt.seq++
	run := flows.Run{
		ID: fmt.Sprintf("run_%d", rt.seq), FlowID: flowID, TriggerKind: kind,
		Status: flows.RunRunning, StartedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
	_ = rt.store.CreateRun(context.Background(), run)
	return &run
}

func (rt *fakeFlowRuntime) TriggerManual(ctx context.Context, flowID string, inputs map[string]any) (*flows.Run, error) {
	if rt.manualErr != nil {
		return nil, rt.manualErr
	}
	f, err := rt.store.GetFlow(ctx, flowID)
	if err != nil {
		return nil, err
	}
	hasManual := false
	for _, n := range f.Graph.Nodes {
		if n.Kind == flows.NodeTriggerManual {
			hasManual = true
		}
	}
	if !hasManual {
		return nil, flows.ErrNoManualTrigger
	}
	return rt.newRun(flowID, flows.NodeTriggerManual), nil
}

func (rt *fakeFlowRuntime) TriggerWebhook(ctx context.Context, token string, payload map[string]any, headers map[string]string) (*flows.Run, error) {
	f, err := rt.store.FindFlowByWebhookHash(ctx, HashWebhookToken(token))
	if err != nil {
		return nil, flows.ErrInvalidWebhook
	}
	rt.webhookGot = append(rt.webhookGot, payload)
	return rt.newRun(f.ID, flows.NodeTriggerWebhook), nil
}

func (rt *fakeFlowRuntime) TestRun(ctx context.Context, req flows.TestRunRequest) (*flows.TestRunResult, error) {
	rt.lastTestReq = req
	run := flows.Run{ID: "run_test", FlowID: req.FlowID, Status: flows.RunCompleted, DryRun: req.DryRun}
	return &flows.TestRunResult{Run: run, Steps: []flows.RunStep{
		{RunID: run.ID, Seq: 1, NodeID: "trigger_1", Status: flows.StepCompleted},
	}}, nil
}

func (rt *fakeFlowRuntime) CancelRun(ctx context.Context, runID string) error {
	run, err := rt.store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status.Terminal() {
		return flows.ErrRunNotActive
	}
	run.Status = flows.RunCancelled
	return rt.store.UpdateRun(ctx, *run)
}

func (rt *fakeFlowRuntime) SubscribedEvents(ctx context.Context) ([]string, error) {
	return nil, nil
}

func manualGraph() flows.Graph {
	return flows.Graph{
		SchemaVersion: flows.SchemaVersion,
		Nodes: []flows.Node{
			{ID: "trigger_1", Kind: flows.NodeTriggerManual, Label: "Manual"},
			{ID: "act_1", Kind: flows.NodeAction, Label: "Set var", Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
				ActionType: "flow.set_variable", Params: map[string]any{"key": "x", "value": "1"},
			})},
		},
		Edges: []flows.Edge{{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "act_1"}},
	}
}

func webhookGraph(secretHeader string) flows.Graph {
	return flows.Graph{
		SchemaVersion: flows.SchemaVersion,
		Nodes: []flows.Node{
			{ID: "trigger_1", Kind: flows.NodeTriggerWebhook, Label: "Hook", Config: flows.MustMarshalConfig(flows.WebhookTriggerConfig{
				SecretHeader: secretHeader, AllowedMethods: []string{"POST"},
			})},
			{ID: "act_1", Kind: flows.NodeAction, Label: "Set var", Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
				ActionType: "flow.set_variable", Params: map[string]any{"key": "x", "value": "1"},
			})},
		},
		Edges: []flows.Edge{{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "act_1"}},
	}
}

type flowsTestEnv struct {
	api   *FlowsAPI
	store *fakeFlowStore
	rt    *fakeFlowRuntime
	hub   *StreamHub
}

func newFlowsTestEnv(t *testing.T, mutate func(*FlowsDeps)) *flowsTestEnv {
	t.Helper()
	store := newFakeFlowStore()
	rt := &fakeFlowRuntime{store: store}
	hub := NewStreamHub()
	now := time.Unix(1_700_000_000, 0).UTC()
	deps := FlowsDeps{
		Store: store, Runtime: rt, StreamHub: hub,
		Now: func() time.Time { return now },
	}
	if mutate != nil {
		mutate(&deps)
	}
	return &flowsTestEnv{api: NewFlowsAPI(deps), store: store, rt: rt, hub: hub}
}

func (e *flowsTestEnv) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body == nil {
		rdr = bytes.NewReader(nil)
	} else if raw, ok := body.(string); ok {
		rdr = bytes.NewReader([]byte(raw))
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.api.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return out
}

func (e *flowsTestEnv) createFlow(t *testing.T, name string, g flows.Graph) flows.Flow {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/", map[string]any{"name": name, "graph": g})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create flow: status %d body %s", rec.Code, rec.Body.String())
	}
	var wrap struct {
		Flow flows.Flow `json:"flow"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	return wrap.Flow
}

func TestFlowsCRUDLifecycle(t *testing.T) {
	env := newFlowsTestEnv(t, nil)
	ch, cancel := env.hub.Subscribe(0)
	defer cancel()

	f := env.createFlow(t, "Nightly check", manualGraph())
	if f.ID == "" || !f.Enabled || f.Version != 1 {
		t.Fatalf("created flow = %+v", f)
	}
	select {
	case ev := <-ch:
		if ev.Type != StreamFlowsChanged || !strings.Contains(string(ev.Data), `"action":"created"`) {
			t.Fatalf("stream event = %s %s", ev.Type, ev.Data)
		}
	default:
		t.Fatal("expected flows.changed stream event after create")
	}

	rec := env.do(t, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	list := decodeBody(t, rec)
	flowsList, _ := list["flows"].([]any)
	if len(flowsList) != 1 {
		t.Fatalf("list flows = %v", list)
	}

	rec = env.do(t, http.MethodPut, "/"+f.ID, map[string]any{"name": "Renamed", "graph": manualGraph()})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	var updated struct {
		Flow flows.Flow `json:"flow"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.Flow.Name != "Renamed" || updated.Flow.Version != 2 {
		t.Fatalf("updated flow = %+v", updated.Flow)
	}

	rec = env.do(t, http.MethodPost, "/"+f.ID+"/disable", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body.String())
	}
	got, _ := env.store.GetFlow(context.Background(), f.ID)
	if got.Enabled {
		t.Fatal("flow still enabled after disable")
	}
	rec = env.do(t, http.MethodPost, "/"+f.ID+"/enable", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body.String())
	}
	got, _ = env.store.GetFlow(context.Background(), f.ID)
	if !got.Enabled || got.DisabledReason != "" {
		t.Fatalf("flow after enable = %+v", got)
	}

	rec = env.do(t, http.MethodDelete, "/"+f.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	rec = env.do(t, http.MethodGet, "/"+f.ID, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted: %d %s", rec.Code, rec.Body.String())
	}
}

func TestFlowsCreateRejectsInvalidGraph(t *testing.T) {
	env := newFlowsTestEnv(t, nil)
	cyclic := flows.Graph{
		SchemaVersion: flows.SchemaVersion,
		Nodes: []flows.Node{
			{ID: "trigger_1", Kind: flows.NodeTriggerManual},
			{ID: "a", Kind: flows.NodeCondition, Config: flows.MustMarshalConfig(flows.ConditionNodeConfig{})},
		},
		Edges: []flows.Edge{
			{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "a"},
			{ID: "e2", Source: "a", SourceHandle: flows.HandleTrue, Target: "trigger_1"},
		},
	}
	rec := env.do(t, http.MethodPost, "/", map[string]any{"name": "Bad", "graph": cyclic})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create cyclic: %d %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if _, ok := body["validation"]; !ok {
		t.Fatalf("expected validation payload, got %v", body)
	}

	rec = env.do(t, http.MethodPost, "/validate", map[string]any{"graph": manualGraph()})
	if rec.Code != http.StatusOK {
		t.Fatalf("validate: %d %s", rec.Code, rec.Body.String())
	}
	var res flows.ValidationResult
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if !res.Valid {
		t.Fatalf("manual graph should validate: %+v", res)
	}
}

func TestFlowsManualAndTestRuns(t *testing.T) {
	env := newFlowsTestEnv(t, nil)
	f := env.createFlow(t, "Manual flow", manualGraph())

	rec := env.do(t, http.MethodPost, "/"+f.ID+"/run", map[string]any{"inputs": map[string]any{"x": 1}})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("manual run: %d %s", rec.Code, rec.Body.String())
	}

	// Test run defaults to dry when dryRun is omitted.
	rec = env.do(t, http.MethodPost, "/"+f.ID+"/test", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("test run: %d %s", rec.Code, rec.Body.String())
	}
	if !env.rt.lastTestReq.DryRun {
		t.Fatal("test run without dryRun must default to dry")
	}

	// Webhook-only flow has no manual trigger.
	hook := env.createFlow(t, "Hook flow", webhookGraph(""))
	rec = env.do(t, http.MethodPost, "/"+hook.ID+"/run", map[string]any{})
	if rec.Code != http.StatusConflict {
		t.Fatalf("manual run without trigger: %d %s", rec.Code, rec.Body.String())
	}

	// Runs list + detail + cancel.
	rec = env.do(t, http.MethodGet, "/runs?flowId="+f.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list runs: %d %s", rec.Code, rec.Body.String())
	}
	var runsWrap struct {
		Runs []flows.Run `json:"runs"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &runsWrap)
	if len(runsWrap.Runs) != 1 {
		t.Fatalf("runs = %+v", runsWrap.Runs)
	}
	runID := runsWrap.Runs[0].ID
	_, _ = env.store.RecordStep(context.Background(), flows.RunStep{RunID: runID, Seq: 1, NodeID: "trigger_1", Status: flows.StepCompleted})
	rec = env.do(t, http.MethodGet, "/runs/"+runID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get run: %d %s", rec.Code, rec.Body.String())
	}
	var detail struct {
		Run   flows.Run       `json:"run"`
		Steps []flows.RunStep `json:"steps"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &detail)
	if detail.Run.ID != runID || len(detail.Steps) != 1 {
		t.Fatalf("run detail = %+v", detail)
	}
	rec = env.do(t, http.MethodPost, "/runs/"+runID+"/cancel", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel run: %d %s", rec.Code, rec.Body.String())
	}
	gotRun, _ := env.store.GetRun(context.Background(), runID)
	if gotRun.Status != flows.RunCancelled {
		t.Fatalf("run status after cancel = %s", gotRun.Status)
	}
}

func TestFlowsWebhookTokenRotateAndIngress(t *testing.T) {
	env := newFlowsTestEnv(t, nil)
	f := env.createFlow(t, "Hook flow", webhookGraph(""))

	rec := env.do(t, http.MethodPost, "/"+f.ID+"/webhook-token/rotate", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	token, _ := body["token"].(string)
	if !strings.HasPrefix(token, "flwh_") || body["webhookUrl"] != "/api/flows/hooks/"+token {
		t.Fatalf("rotate body = %v", body)
	}
	// The token is never echoed again.
	rec = env.do(t, http.MethodGet, "/"+f.ID, nil)
	if strings.Contains(rec.Body.String(), token) {
		t.Fatal("GET flow leaked the webhook token")
	}

	hook := func(method, tok, payload string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/"+tok, strings.NewReader(payload))
		req.RemoteAddr = "192.0.2.10:1234"
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		env.api.WebhookHandler().ServeHTTP(rec, req)
		return rec
	}

	if rec := hook(http.MethodPost, token, `{"hello":"world"}`, nil); rec.Code != http.StatusAccepted {
		t.Fatalf("webhook: %d %s", rec.Code, rec.Body.String())
	}
	if len(env.rt.webhookGot) != 1 || env.rt.webhookGot[0]["hello"] != "world" {
		t.Fatalf("runtime payload = %v", env.rt.webhookGot)
	}
	if rec := hook(http.MethodPost, "flwh_wrong", `{}`, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", rec.Code)
	}
	if rec := hook(http.MethodGet, token, "", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method: %d", rec.Code)
	}
	big := `{"x":"` + strings.Repeat("a", int(MaxWebhookBodyBytes)) + `"}`
	if rec := hook(http.MethodPost, token, big, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized: %d", rec.Code)
	}

	// Disabled flow refuses.
	_ = env.store.SetFlowEnabled(context.Background(), f.ID, false, "off")
	if rec := hook(http.MethodPost, token, `{}`, nil); rec.Code != http.StatusConflict {
		t.Fatalf("disabled flow: %d", rec.Code)
	}
}

func TestFlowsWebhookHMACAndRateLimit(t *testing.T) {
	env := newFlowsTestEnv(t, func(d *FlowsDeps) { d.WebhookRatePerToken = 3 })
	f := env.createFlow(t, "Signed hook", webhookGraph("X-Sig"))
	token := "flwh_testtoken"
	if err := env.store.SetWebhookTokenHash(context.Background(), f.ID, HashWebhookToken(token)); err != nil {
		t.Fatal(err)
	}
	payload := `{"a":1}`
	signed := func(sig string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/"+token, strings.NewReader(payload))
		req.RemoteAddr = "192.0.2.11:1234"
		req.Header.Set("Content-Type", "application/json")
		if sig != "" {
			req.Header.Set("X-Sig", sig)
		}
		rec := httptest.NewRecorder()
		env.api.WebhookHandler().ServeHTTP(rec, req)
		return rec
	}
	if rec := signed(""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing signature: %d", rec.Code)
	}
	if rec := signed("sha256=deadbeef"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature: %d", rec.Code)
	}
	good := ComputeWebhookHMAC(token, []byte(payload))
	if rec := signed(good); rec.Code != http.StatusAccepted {
		t.Fatalf("good signature: %d %s", rec.Code, rec.Body.String())
	}
	// Per-token budget is 3/min; the attempts above already used it,
	// so further calls are limited deterministically (fixed clock).
	if rec := signed(good); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limited: %d %s", rec.Code, rec.Body.String())
	}
}

func TestFlowsCatalogues(t *testing.T) {
	reg := events.NewRegistry()
	reg.MustRegister(events.EventSchema{
		Type: "playback.session.failed", Version: 1, Domain: "playback",
		Label: "Playback failed", Owner: events.OwnerBuiltin,
		Fields: []events.FieldSchema{
			{Name: "error", Type: events.FieldString, Label: "Error"},
			{Name: "attempt", Type: events.FieldInt, Label: "Attempt"},
		},
	})
	actions := flows.NewActionRegistry()
	actions.MustRegister(flows.ActionFunc{ActionSpec: flows.ActionSpec{
		Type: "notify.send", Group: "notifications", Label: "Send notification",
		Params: []flows.ParamSchema{{Key: "channelIds", Type: events.FieldStringList, Label: "Channels", Required: true}},
	}})
	env := newFlowsTestEnv(t, func(d *FlowsDeps) { d.Events = reg; d.Actions = actions })

	rec := env.do(t, http.MethodGet, "/catalogue/events", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("event catalogue: %d %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	evs, _ := body["events"].([]any)
	if len(evs) != 1 {
		t.Fatalf("catalogue events = %v", body["events"])
	}
	first, _ := evs[0].(map[string]any)
	fields, _ := first["fields"].([]any)
	opsFound := false
	for _, fl := range fields {
		m, _ := fl.(map[string]any)
		if m["name"] == "attempt" {
			ops, _ := m["operators"].([]any)
			for _, op := range ops {
				if op == "gte" {
					opsFound = true
				}
			}
		}
	}
	if !opsFound {
		t.Fatalf("expected gte operator for int field: %v", fields)
	}
	if _, ok := body["operatorsByFieldType"]; !ok {
		t.Fatal("expected operatorsByFieldType")
	}

	rec = env.do(t, http.MethodGet, "/catalogue/actions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("action catalogue: %d %s", rec.Code, rec.Body.String())
	}
	body = decodeBody(t, rec)
	acts, _ := body["actions"].([]any)
	if len(acts) != 1 {
		t.Fatalf("catalogue actions = %v", body["actions"])
	}
}

func TestDefaultFlowTemplatesAreValid(t *testing.T) {
	templates := DefaultFlowTemplates()
	if len(templates) == 0 {
		t.Fatal("no default templates")
	}
	evReg := events.NewBuiltinRegistry()
	actReg, err := actions.NewRegistry(actions.Deps{})
	if err != nil {
		t.Fatalf("actions.NewRegistry: %v", err)
	}
	trigReg := flows.NewTriggerRegistry()
	opts := flows.ValidateOptions{
		Events:   evReg,
		Actions:  actReg,
		Triggers: trigReg,
	}

	seen := map[string]bool{}
	for _, tmpl := range templates {
		if seen[tmpl.ID] {
			t.Fatalf("duplicate template id %q", tmpl.ID)
		}
		seen[tmpl.ID] = true

		params := map[string]any{}
		for _, p := range tmpl.Params {
			if !flows.ValidPicker(p.Picker) {
				t.Fatalf("template %q param %q uses non-canonical picker %q", tmpl.ID, p.Key, p.Picker)
			}
			// Supply a sample value only for required params that have no default.
			if p.Required && p.Default == nil {
				params[p.Key] = "sample-value"
			}
		}

		flow, ferrs := InstantiateFlowTemplate(tmpl, "", "", nil, params)
		if len(ferrs) > 0 {
			t.Fatalf("template %q instantiate failed: %+v", tmpl.ID, ferrs)
		}
		if res := flows.ValidateFlow(flow, opts); !res.Valid {
			t.Fatalf("template %q instantiated flow invalid: %+v", tmpl.ID, res.Errors)
		}
	}
}

func TestFlowsFromTemplate(t *testing.T) {
	actReg, err := actions.NewRegistry(actions.Deps{})
	if err != nil {
		t.Fatalf("actions.NewRegistry: %v", err)
	}
	env := newFlowsTestEnv(t, func(d *FlowsDeps) {
		d.Events = events.NewBuiltinRegistry()
		d.Actions = actReg
	})
	rec := env.do(t, http.MethodGet, "/templates", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("templates: %d", rec.Code)
	}
	body := decodeBody(t, rec)
	tmpls, _ := body["templates"].([]any)
	if len(tmpls) == 0 {
		t.Fatal("empty template gallery")
	}

	// Required param with no default (brokerId) missing -> 400.
	rec = env.do(t, http.MethodPost, "/from-template/home-assistant-cinema-lighting", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("from-template without required brokerId: %d %s", rec.Code, rec.Body.String())
	}

	// Every notification template succeeds with omitted params (defaulting
	// channelIds to ["*"]), empty channelIds ([]), and explicit channelIds.
	notifyTemplates := []string{
		"notify-critical-alerts",
		"notify-playback-issues",
		"notify-provider-outages",
		"notify-updates",
		"notify-weekly-newsletter",
		"new-device-alert",
		"concurrent-stream-guard",
		"relieve-buffering-transcode",
		"livetv-failover-alert",
		"auto-unpin-dead-pin",
	}
	for _, id := range notifyTemplates {
		for _, tc := range []struct {
			name   string
			body   map[string]any
			expect string
		}{
			{name: "default", body: map[string]any{}, expect: `"*"`},
			{name: "empty", body: map[string]any{"params": map[string]any{"channelIds": []string{}}}, expect: `"*"`},
			{name: "explicit", body: map[string]any{"params": map[string]any{"channelIds": []string{"ch_1"}}}, expect: `"ch_1"`},
		} {
			rec = env.do(t, http.MethodPost, "/from-template/"+id, tc.body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("from-template %s (%s): %d %s", id, tc.name, rec.Code, rec.Body.String())
			}
			var wrap struct {
				Flow flows.Flow `json:"flow"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &wrap)
			if wrap.Flow.TemplateID != id || wrap.Flow.ID == "" {
				t.Fatalf("instantiated %s (%s) flow = %+v", id, tc.name, wrap.Flow)
			}
			cfg := string(notifyActionConfig(t, wrap.Flow))
			if !strings.Contains(cfg, tc.expect) {
				t.Fatalf("%s (%s): channelIds %s not substituted in %s", id, tc.name, tc.expect, cfg)
			}
		}
	}

	rec = env.do(t, http.MethodPost, "/from-template/nope", map[string]any{})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown template: %d", rec.Code)
	}
}

func notifyActionConfig(t *testing.T, f flows.Flow) []byte {
	t.Helper()
	for _, n := range f.Graph.Nodes {
		if n.Kind != flows.NodeAction {
			continue
		}
		var cfg flows.ActionNodeConfig
		if err := json.Unmarshal(n.Config, &cfg); err == nil && cfg.ActionType == "notify.send" {
			return n.Config
		}
	}
	t.Fatal("no notify.send action node")
	return nil
}

func TestFlowsCustomizeNotification(t *testing.T) {
	channels := []notify.Channel{
		{ID: "ch_1", Name: "Ops", Kind: "webhook", Enabled: true, Events: []string{"playback.error", "provider.outage"}},
		{ID: "ch_2", Name: "Watch", Kind: "webhook", Enabled: true, Events: []string{"server.crash"}},
	}
	var saved []notify.Channel
	env := newFlowsTestEnv(t, func(d *FlowsDeps) {
		d.NotifyChannels = func(ctx context.Context) ([]notify.Channel, error) { return channels, nil }
		d.SaveNotifyChannel = func(ctx context.Context, ch notify.Channel) error {
			saved = append(saved, ch)
			return nil
		}
	})

	rec := env.do(t, http.MethodPost, "/customize-notification", map[string]any{
		"channelId": "ch_1", "event": "playback.error",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("customize: %d %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	migrated, _ := body["migrated"].([]any)
	if len(migrated) != 1 || migrated[0] != "playback.error" {
		t.Fatalf("migrated = %v", body["migrated"])
	}
	if len(saved) != 1 || len(saved[0].Events) != 1 || saved[0].Events[0] != "provider.outage" {
		t.Fatalf("saved channel = %+v", saved)
	}

	// Crash-only rules stay with the out-of-process watcher.
	rec = env.do(t, http.MethodPost, "/customize-notification", map[string]any{"channelId": "ch_2"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("crash-only customize: %d %s", rec.Code, rec.Body.String())
	}

	rec = env.do(t, http.MethodPost, "/customize-notification", map[string]any{"channelId": "missing"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown channel: %d", rec.Code)
	}
}

func TestFlowsUnavailableWithoutStoreOrRuntime(t *testing.T) {
	api := NewFlowsAPI(FlowsDeps{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("list without store: %d", rec.Code)
	}
	// Catalogues and templates still answer from registries/defaults.
	for _, path := range []string{"/catalogue/events", "/catalogue/actions", "/templates"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		api.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s without deps: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}
