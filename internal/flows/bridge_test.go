package flows

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/cluster"
	"github.com/braedonsaunders/ether/internal/clusterbus"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/store"
)

// fakeCluster implements cluster.Cluster backed by an in-memory clusterbus.Node
// with deterministic, test-controlled leadership transitions.
type fakeCluster struct {
	self cluster.Node
	bus  clusterbus.Node

	mu        sync.Mutex
	leading   bool
	epoch     int64
	listeners map[int]func(bool)
	nextID    int
}

func newFakeCluster(nodeID string, bus clusterbus.Node, leading bool) *fakeCluster {
	var epoch int64
	if leading {
		epoch = 1
	}
	return &fakeCluster{
		self:      cluster.Node{ID: nodeID, Name: nodeID, Live: true, Leader: leading},
		bus:       bus,
		leading:   leading,
		epoch:     epoch,
		listeners: map[int]func(bool){},
	}
}

func (c *fakeCluster) Mode() string       { return cluster.ModePostgres }
func (c *fakeCluster) Self() cluster.Node { return c.self }

func (c *fakeCluster) IsLeader() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.leading
}

func (c *fakeCluster) Epoch() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epoch
}

func (c *fakeCluster) SetLeader(leading bool) {
	c.mu.Lock()
	if c.leading == leading {
		c.mu.Unlock()
		return
	}
	c.leading = leading
	if leading {
		c.epoch++
	}
	c.self.Leader = leading
	cbs := make([]func(bool), 0, len(c.listeners))
	for _, fn := range c.listeners {
		cbs = append(cbs, fn)
	}
	c.mu.Unlock()
	for _, fn := range cbs {
		fn(leading)
	}
}

func (c *fakeCluster) OnLeaderChange(fn func(bool)) func() {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	c.listeners[id] = fn
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.listeners, id)
		c.mu.Unlock()
	}
}

func (c *fakeCluster) RunAsLeader(ctx context.Context, _ string, fn func(context.Context)) {
	ch := make(chan bool, 4)
	unsub := c.OnLeaderChange(func(lead bool) {
		select {
		case ch <- lead:
		default:
			select {
			case <-ch:
			default:
			}
			ch <- lead
		}
	})
	defer unsub()

	var (
		leadCancel context.CancelFunc
		done       chan struct{}
	)
	start := func() {
		if leadCancel != nil {
			return
		}
		lctx, cancel := context.WithCancel(ctx)
		leadCancel = cancel
		done = make(chan struct{})
		go func() {
			defer close(done)
			fn(lctx)
		}()
	}
	stop := func() {
		if leadCancel == nil {
			return
		}
		leadCancel()
		<-done
		leadCancel = nil
		done = nil
	}
	defer stop()

	if c.IsLeader() {
		start()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case lead := <-ch:
			if lead {
				start()
			} else {
				stop()
			}
		}
	}
}

func (c *fakeCluster) Publish(ctx context.Context, topic string, payload []byte) error {
	return c.bus.Publish(ctx, topic, payload)
}

func (c *fakeCluster) Subscribe(topic string, fn func([]byte)) func() {
	return c.bus.Subscribe(topic, fn)
}

func (c *fakeCluster) Nodes(context.Context) ([]cluster.Node, error) {
	return []cluster.Node{c.self}, nil
}
func (c *fakeCluster) AllNodes(context.Context) ([]cluster.Node, error) {
	return []cluster.Node{c.self}, nil
}
func (c *fakeCluster) Start(context.Context) error { return nil }
func (c *fakeCluster) Close() error                { return nil }

var _ cluster.Cluster = (*fakeCluster)(nil)

func TestClusterLeaderOnlyAndFailoverHandover(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clk := newFakeClock(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	db := store.OpenTest(t)

	st := NewSQLStore(db, WithStoreNow(clk.Now))
	hub := clusterbus.NewHub()

	clA := newFakeCluster("node-A", hub.Node("node-A"), false) // starts as follower
	clB := newFakeCluster("node-B", hub.Node("node-B"), false) // starts as follower

	var executedBy []string
	var execMu sync.Mutex
	runDone := make(chan string, 4)

	makeNodeEngine := func(nodeID string, cl *fakeCluster, leaderReady chan struct{}) *Engine {
		reg := NewActionRegistry()
		_ = reg.Register(newFakeAction("act.who", true, func(_ context.Context, _ ActionContext, _ map[string]any) (ActionResult, error) {
			execMu.Lock()
			executedBy = append(executedBy, nodeID)
			execMu.Unlock()
			runDone <- nodeID
			return ActionResult{Output: map[string]any{"node": nodeID}}, nil
		}))
		return NewEngine(EngineOptions{
			Store:        st,
			DB:           db,
			Bus:          events.NewBus(events.BusOptions{}),
			Actions:      reg,
			Cluster:      cl,
			NodeID:       nodeID,
			Now:          clk.Now,
			TickInterval: time.Hour,
			OnLeaderStarted: func() {
				select {
				case leaderReady <- struct{}{}:
				default:
				}
			},
		})
	}

	readyA := make(chan struct{}, 2)
	readyB := make(chan struct{}, 2)
	engA := makeNodeEngine("node-A", clA, readyA)
	engB := makeNodeEngine("node-B", clB, readyB)

	saved, err := st.SaveFlow(ctx, Flow{
		Name:    "Leader Singleton Flow",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "tm", Kind: NodeTriggerManual, Config: mustRawJSON(t, ManualTriggerConfig{})},
				{ID: "a", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.who"})},
			},
			Edges: []Edge{{ID: "e1", Source: "tm", Target: "a"}},
		},
	})
	if err != nil {
		t.Fatalf("SaveFlow: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = engA.Start(ctx) }()
	go func() { defer wg.Done(); _ = engB.Start(ctx) }()
	<-engA.Bridge().Ready()
	<-engB.Bridge().Ready()

	// Enqueue a manual run while NEITHER node is leader: neither may execute it!
	run1, err := engA.TriggerManual(ctx, saved.ID, nil)
	if err != nil {
		t.Fatalf("TriggerManual while follower: %v", err)
	}
	if engA.IsLeading() || engB.IsLeading() {
		t.Fatalf("expected neither node leading yet")
	}
	r1Check, _ := st.GetRun(ctx, run1.ID)
	if r1Check.Status != RunPending {
		t.Fatalf("expected run1 to stay pending while no leader exists, got %s", r1Check.Status)
	}

	// Elect node-A as leader: node-A claims and executes run1.
	clA.SetLeader(true)
	<-readyA
	select {
	case who := <-runDone:
		if who != "node-A" {
			t.Fatalf("expected node-A to run run1, got %s", who)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for node-A to execute run1")
	}

	// Hand leadership from node-A to node-B; enqueue run2 on follower node-A;
	// node-B (new leader) must wake via clusterbus and execute run2!
	clA.SetLeader(false)
	clB.SetLeader(true)
	<-readyB
	if _, err := engA.TriggerManual(ctx, saved.ID, nil); err != nil {
		t.Fatalf("TriggerManual on follower node-A: %v", err)
	}
	select {
	case who := <-runDone:
		if who != "node-B" {
			t.Fatalf("expected new leader node-B to run run2, got %s", who)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for new leader node-B to execute run2")
	}

	cancel()
	wg.Wait()

	execMu.Lock()
	got := slices.Clone(executedBy)
	execMu.Unlock()
	if !slices.Equal(got, []string{"node-A", "node-B"}) {
		t.Fatalf("expected execution handover [node-A node-B], got %v", got)
	}
}

func TestClusterFollowerOutboxBridgeAndSubscriptionSync(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clk := newFakeClock(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	db := store.OpenTest(t)

	st := NewSQLStore(db, WithStoreNow(clk.Now))
	hub := clusterbus.NewHub()

	leaderBus := events.NewBus(events.BusOptions{})
	followerBus := events.NewBus(events.BusOptions{})

	clLeader := newFakeCluster("leader-1", hub.Node("leader-1"), true)
	clFollower := newFakeCluster("follower-1", hub.Node("follower-1"), false)

	actions := NewActionRegistry()
	actedCh := make(chan recordedCall, 8)
	var execCount atomic.Int32
	_ = actions.Register(newFakeAction("act.remote", false, func(_ context.Context, actx ActionContext, params map[string]any) (ActionResult, error) {
		execCount.Add(1)
		actedCh <- recordedCall{Actx: actx, Params: params}
		return ActionResult{Output: map[string]any{"ok": true}}, nil
	}))

	// Save initial flow subscribing ONLY to playback.session.failed.
	_, err := st.SaveFlow(ctx, Flow{
		Name:    "Playback Failure Flow",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "t", Kind: NodeTriggerEvent, Config: mustRawJSON(t, EventTriggerConfig{Events: []string{"playback.session.failed"}})},
				{ID: "a", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.remote", Params: map[string]any{"message": "from:{{ event.node }}:{{ payload.reason }}"}})},
			},
			Edges: []Edge{{ID: "e1", Source: "t", Target: "a"}},
		},
	})
	if err != nil {
		t.Fatalf("SaveFlow initial: %v", err)
	}

	leaderReady := make(chan struct{})
	leaderEng := NewEngine(EngineOptions{
		Store:           st,
		DB:              db,
		Bus:             leaderBus,
		Actions:         actions,
		Cluster:         clLeader,
		NodeID:          "leader-1",
		Now:             clk.Now,
		TickInterval:    time.Hour,
		OnLeaderStarted: func() { close(leaderReady) },
	})
	followerEng := NewEngine(EngineOptions{
		Store:        st,
		DB:           db,
		Bus:          followerBus,
		Actions:      actions,
		Cluster:      clFollower,
		NodeID:       "follower-1",
		Now:          clk.Now,
		TickInterval: time.Hour,
	})

	// Watch the follower bridge's own committed subscription updates so we sync
	// only after b.patterns has actually been updated under b.mu.
	subsUpdated := make(chan []string, 16)
	followerEng.Bridge().SetOnSubsUpdated(func(pats []string) {
		subsUpdated <- pats
	})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = followerEng.Start(ctx) }()
	<-followerEng.Bridge().Ready()
	go func() { defer wg.Done(); _ = leaderEng.Start(ctx) }()
	<-leaderReady

	// Wait until follower bridge has committed initial subscription pattern.
	waitForPattern := func(want string) {
		t.Helper()
		if slices.Contains(followerEng.Bridge().Patterns(), want) {
			return
		}
		deadline := time.After(5 * time.Second)
		for {
			select {
			case pats := <-subsUpdated:
				if slices.Contains(pats, want) {
					return
				}
			case <-deadline:
				t.Fatalf("timed out waiting for follower bridge to commit pattern %q (have %v)", want, followerEng.Bridge().Patterns())
			}
		}
	}
	waitForPattern("playback.session.failed")

	// 1. Publish an UNSUBSCRIBED event on the follower -> must cause ZERO events_outbox writes!
	unsubEv := newTestEvent("evt_unsub_1", "library.scan.progress", events.Correlation{}, map[string]any{"pct": 50})
	unsubEv.Node = "follower-1"
	followerBus.Publish(unsubEv)

	// 2. Publish a SUBSCRIBED event on the follower -> bridge forwards via events_outbox + flows:outbox wake,
	//    leader drains it and runs the action once!
	subEv := newTestEvent("evt_remote_fail_1", "playback.session.failed", events.Correlation{SessionID: "s9"}, map[string]any{"reason": "codec"})
	subEv.Node = "follower-1"
	followerBus.Publish(subEv)

	select {
	case call := <-actedCh:
		if call.Params["message"] != "from:follower-1:codec" {
			t.Fatalf("unexpected forwarded event message: %v", call.Params["message"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for leader to execute follower-forwarded event")
	}

	// Verify events_outbox has ONLY 1 row (for subEv, NOT unsubEv!) and that row is acknowledged.
	var totalOutbox, unackedOutbox int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events_outbox`).Scan(&totalOutbox)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events_outbox WHERE acked_at_ms = 0`).Scan(&unackedOutbox)
	if totalOutbox != 1 || unackedOutbox != 0 {
		t.Fatalf("expected 1 acked outbox row (zero for unsubscribed event), got total=%d unacked=%d", totalOutbox, unackedOutbox)
	}

	// 3. Replay the exact same event on follower -> deduplicated at outbox & run level, action still runs once!
	followerBus.Publish(subEv)
	_ = leaderEng.ProcessPending(ctx)
	if execCount.Load() != 1 {
		t.Fatalf("expected action to run once despite follower replay, got %d", execCount.Load())
	}

	// 4. Add a new flow subscribing to livetv.input.lost -> leader broadcasts updated flows:subs ->
	//    follower bridge receives it and forwards the new event type!
	_, err = st.SaveFlow(ctx, Flow{
		Name:    "LiveTV Lost Flow",
		Enabled: true,
		Graph: Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "t", Kind: NodeTriggerEvent, Config: mustRawJSON(t, EventTriggerConfig{Events: []string{"livetv.input.lost"}})},
				{ID: "a", Kind: NodeAction, Config: mustRawJSON(t, ActionNodeConfig{ActionType: "act.remote", Params: map[string]any{"message": "lost:{{ payload.channel }}"}})},
			},
			Edges: []Edge{{ID: "e1", Source: "t", Target: "a"}},
		},
	})
	if err != nil {
		t.Fatalf("SaveFlow second: %v", err)
	}

	// Wait for live flows:subs update containing livetv.input.lost to commit on follower bridge.
	waitForPattern("livetv.input.lost")

	lostEv := newTestEvent("evt_lost_1", "livetv.input.lost", events.Correlation{}, map[string]any{"channel": "HBO"})
	lostEv.Node = "follower-1"
	followerBus.Publish(lostEv)

	select {
	case call := <-actedCh:
		if call.Params["message"] != "lost:HBO" {
			t.Fatalf("unexpected dynamically subscribed event message: %v", call.Params["message"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for dynamically subscribed follower event")
	}

	// 5. Durable unsubscribed event on follower MUST still be persisted into events_outbox.
	durableEv := newTestEvent("evt_durable_1", "backup.job.failed", events.Correlation{}, map[string]any{"err": "disk full"})
	durableEv.Node = "follower-1"
	durableEv.Meta.Durable = true
	forwardedDurable := make(chan struct{}, 1)
	standaloneBus := events.NewBus(events.BusOptions{})
	standaloneBridge := NewOutboxBridge(OutboxBridgeOptions{
		DB:          db,
		Bus:         standaloneBus,
		ClusterBus:  hub.Node("follower-2"),
		NodeID:      "follower-2",
		IsLeader:    func() bool { return false },
		Now:         clk.Now,
		OnForwarded: func(ev events.Envelope) { forwardedDurable <- struct{}{} },
	})
	bCtx, bCancel := context.WithCancel(ctx)
	bDone := make(chan struct{})
	go func() { defer close(bDone); standaloneBridge.Run(bCtx) }()
	<-standaloneBridge.Ready()

	standaloneBus.Publish(durableEv)
	select {
	case <-forwardedDurable:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Meta.Durable event to be forwarded to events_outbox")
	}
	bCancel()
	<-bDone

	cancel()
	wg.Wait()
}

func TestOutboxBridgeOversizedSubsFallbackReload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Build a pattern list larger than clusterbus.MaxPayload so encodeSubsBroadcast emits Reload=true.
	var huge []string
	for i := range 400 {
		huge = append(huge, fmt.Sprintf("plugin:com.example.very.long.plugin.identifier.%03d/event.name", i))
	}
	raw := encodeSubsBroadcast("orig", "leader-1", 1, huge)
	if len(raw) > clusterbus.MaxPayload {
		t.Fatalf("encoded fallback payload exceeds clusterbus.MaxPayload: %d", len(raw))
	}
	if !strings.Contains(string(raw), `"reload":true`) {
		t.Fatalf("expected reload fallback payload, got %s", string(raw))
	}

	db := store.OpenTest(t)

	hub := clusterbus.NewHub()
	leaderCbus := hub.Node("leader-reload")
	followerCbus := hub.Node("follower-reload")

	reloaded := make(chan []string, 4)
	var loadCalls atomic.Int32
	bridge := NewOutboxBridge(OutboxBridgeOptions{
		DB:         db,
		Bus:        events.NewBus(events.BusOptions{}),
		ClusterBus: followerCbus,
		NodeID:     "follower-reload",
		IsLeader:   func() bool { return false },
		LoadSubs: func(context.Context) ([]string, error) {
			if loadCalls.Add(1) == 1 {
				return []string{"initial.event"}, nil
			}
			return []string{"reloaded.event"}, nil
		},
		OnSubsUpdated: func(pats []string) {
			reloaded <- pats
		},
	})

	done := make(chan struct{})
	go func() { defer close(done); bridge.Run(ctx) }()
	<-bridge.Ready()
	<-reloaded // initial load

	if err := leaderCbus.Publish(ctx, TopicSubscriptions, raw); err != nil {
		t.Fatalf("Publish reload fallback: %v", err)
	}
	select {
	case pats := <-reloaded:
		if !slices.Equal(pats, []string{"reloaded.event"}) {
			t.Fatalf("expected reloaded patterns, got %v", pats)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for bridge reload after oversized subs broadcast")
	}

	cancel()
	<-done
}
