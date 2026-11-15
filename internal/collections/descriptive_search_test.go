package collections_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent"
	"github.com/braedonsaunders/ether/internal/agent/builtin"
	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/llm"
	"github.com/braedonsaunders/ether/internal/store"
)

type describeChat struct {
	calls    atomic.Int32
	block    chan struct{}
	fail     atomic.Bool
	genre    string
	deadline chan time.Duration
}

func (c *describeChat) Enabled() bool { return true }
func (c *describeChat) Chat(ctx context.Context, _ []llm.Message, _ bool) (string, error) {
	c.calls.Add(1)
	if c.deadline != nil {
		deadline, ok := ctx.Deadline()
		if ok {
			c.deadline <- time.Until(deadline)
		} else {
			c.deadline <- 0
		}
	}
	if c.block != nil {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-c.block:
		}
	}
	if c.fail.Load() {
		return "", errors.New("offline")
	}
	b, _ := json.Marshal(map[string]any{"spec": map[string]any{"name": "Generated picks", "mediaType": "mixed", "sources": []map[string]any{{"kind": "catalog"}}, "match": "all", "rules": []map[string]any{{"field": "genre", "op": "in", "value": []string{c.genre}}, {"field": "votes", "op": "gte", "value": 20}}, "sort": "popularity", "sortDesc": true, "limit": 200}})
	return string(b), nil
}

// Turn a scripted model reply into a real tool call; every draft is executed
// by the same built-in collection_draft tool used by the console.
type draftReplyModel struct{ chat collections.Chatter }

func (m draftReplyModel) Enabled() bool                { return m.chat.Enabled() }
func (draftReplyModel) ProviderID() string             { return "fake" }
func (draftReplyModel) ModelName() string              { return "fake-describe" }
func (draftReplyModel) ToolsSupported() (bool, string) { return true, "" }
func (m draftReplyModel) StreamTools(ctx context.Context, req llm.ToolRequest) (<-chan llm.Event, error) {
	out := make(chan llm.Event, 2)
	if req.Messages[len(req.Messages)-1].Role == "tool" {
		out <- llm.Event{Kind: llm.EventTextDelta, Text: "Here are the picks."}
	} else {
		msgs := []llm.Message{{Role: "system", Content: req.System}}
		for _, msg := range req.Messages {
			msgs = append(msgs, llm.Message{Role: msg.Role, Content: msg.Content})
		}
		raw, err := m.chat.Chat(ctx, msgs, true)
		if err != nil {
			return nil, err
		}
		raw = strings.TrimSuffix(strings.TrimPrefix(raw, "```json\n"), "\n```")
		out <- llm.Event{Kind: llm.EventToolCall, ToolCallID: "draft", ToolName: "collection_draft", Input: json.RawMessage(raw)}
	}
	out <- llm.Event{Kind: llm.EventDone}
	close(out)
	return out, nil
}
func setDescribeModel(t *testing.T, db *sql.DB, cat *catalog.Catalog, svc *collections.Service, chat collections.Chatter) {
	t.Helper()
	setDescribeAgentModel(t, db, cat, svc, draftReplyModel{chat}, nil)
}
func setDescribeAgentModel(t *testing.T, db *sql.DB, cat *catalog.Catalog, svc *collections.Service, model agent.Model, logger *slog.Logger) {
	t.Helper()
	catalogue := tools.NewCatalogue()
	if err := catalogue.Register(builtin.All(builtin.Deps{
		Catalog: cat, Collections: svc,
		Status: &struct{ builtin.StatusService }{}, Playback: &struct{ builtin.PlaybackService }{},
		Resolver: &struct{ builtin.ResolverService }{}, Logs: &struct{ builtin.LogService }{},
		LiveTV: &struct{ builtin.LiveTVService }{}, Sources: &struct{ builtin.SourceService }{},
		Profiles: &struct{ builtin.ProfileService }{}, Frontends: &struct{ builtin.FrontendService }{},
		Server:    &struct{ builtin.ServerService }{},
		OwnerUser: func(context.Context) (*core.User, error) { return &core.User{ID: 1, IsOwner: true}, nil },
		Routes:    func() []builtin.RouteInfo { return nil },
	})...); err != nil {
		t.Fatal(err)
	}
	svc.SetDescribeAgent(agent.New(agent.Options{Model: model, Catalogue: catalogue, Store: agent.NewStore(db), Logger: logger}))
}

func TestDescriptiveSearch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat, err := catalog.New(db, nil, config.Catalog{Language: "en-US", Region: "US"}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	for i, g := range []string{"Comedy", "Romance", "Family", "Animation", "Horror", "Thriller", "Mystery", "Drama", "Adventure", "War", "Fantasy", "Science Fiction", "Action", "Music"} {
		title := core.Title{Key: core.MovieKey(int64(i + 1)), IDs: core.IDs{TMDB: int64(i + 1)}, Type: core.TypeMovie, Title: g + " fixture", Genres: []string{g}, Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Year: 2000, VoteCount: 100, Popularity: 100, Rating: 8, ContentRating: "PG"}
		if err := cat.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
	}
	var warnings bytes.Buffer
	svc, err := collections.New(db, cat, slog.New(slog.NewJSONHandler(&warnings, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err != nil {
		t.Fatal(err)
	}
	if svc.DescriptiveSearchAvailable() {
		t.Fatal("advertised without AI")
	}
	disabled, err := svc.DescribeSearch(ctx, nil, "cozy cute girls night in", true)
	if err != nil || disabled.State != "unavailable" {
		t.Fatalf("disabled: %+v %v", disabled, err)
	}
	cases := []struct{ q, g string }{{"cozy cute girls night in", "Romance"}, {"scary creepy night", "Horror"}, {"rainy night", "Mystery"}, {"epic adventure", "Adventure"}, {"mind bending", "Science Fiction"}, {"date night", "Romance"}, {"family movie night", "Family"}, {"background easy watching", "Comedy"}, {"tearjerker", "Drama"}, {"adrenaline", "Action"}, {"80s sci-fi under 2 hours", "Science Fiction"}}
	for _, tc := range cases {
		t.Run(tc.q, func(t *testing.T) {
			chat := &describeChat{genre: tc.g, block: make(chan struct{})}
			setDescribeModel(t, db, cat, svc, chat)
			pending, e := svc.DescribeSearch(ctx, &core.User{ID: 7}, tc.q, false)
			if e != nil || pending.State != "pending" {
				t.Fatalf("pending: %+v %v", pending, e)
			}
			close(chat.block)
			ready, e := svc.DescribeSearch(ctx, &core.User{ID: 7}, tc.q, true)
			if e != nil || ready.State != "ready" || len(ready.Items) == 0 {
				t.Fatalf("ready: %+v %v", ready, e)
			}
			if ready.Items[0].Genres[0] != tc.g {
				t.Fatalf("wrong genre: %+v", ready.Items)
			}
			_, e = svc.DescribeSearch(ctx, &core.User{ID: 7}, "  "+strings.ToUpper(tc.q)+"  ", true)
			if e != nil || chat.calls.Load() != 1 {
				t.Fatalf("cache/model bound: model %d err %v", chat.calls.Load(), e)
			}
			c, items, ok, e := svc.VibeCollection(ctx, &core.User{ID: 7}, tc.q)
			if e != nil || !ok || !c.Key.IsEphemeral() || len(items) == 0 {
				t.Fatalf("protocol bridge %+v %v %v", c, ok, e)
			}
			if _, e := svc.Get(ctx, &core.User{ID: 8}, c.Key); !errors.Is(e, core.ErrNotFound) {
				t.Fatalf("other viewer: %v", e)
			}
			t.Logf("%s -> %s", tc.q, ready.Items[0].Title)
		})
	}
	// Admission and request cancellation must not shorten the shared job's
	// model deadline or create a queue of blocked goroutines.
	blocked := &describeChat{genre: "Comedy", block: make(chan struct{}), deadline: make(chan time.Duration, 2)}
	setDescribeModel(t, db, cat, svc, blocked)
	for _, query := range []string{"bounded job one", "bounded job two"} {
		request, cancelRequest := context.WithCancel(ctx)
		pending, e := svc.DescribeSearch(request, nil, query, false)
		cancelRequest()
		if e != nil || pending.State != "pending" || pending.RetryAfterSeconds <= 0 {
			t.Fatalf("admission: %+v %v", pending, e)
		}
		select {
		case remaining := <-blocked.deadline:
			if remaining < 115*time.Second || remaining > 2*time.Minute {
				t.Fatalf("shared draft deadline: %v", remaining)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	busy, e := svc.DescribeSearch(ctx, nil, "bounded job three", false)
	if e != nil || busy.State != "busy" || busy.RetryAfterSeconds <= 0 || blocked.calls.Load() != 2 {
		t.Fatalf("bounded jobs: %+v %v", busy, e)
	}
	close(blocked.block)
	for _, query := range []string{"bounded job one", "bounded job two"} {
		ready, e := svc.DescribeSearch(ctx, nil, query, true)
		if e != nil || ready.State != "ready" {
			t.Fatalf("shared job completion: %+v %v", ready, e)
		}
	}

	failed := &describeChat{genre: "Comedy"}
	failed.fail.Store(true)
	setDescribeModel(t, db, cat, svc, failed)
	for i := 0; i < 2; i++ {
		result, e := svc.DescribeSearch(ctx, nil, "unavailable model query", true)
		if e == nil || result.State != "failed" || result.RetryAfterSeconds < 1 || result.RetryAfterSeconds > 60 {
			t.Fatalf("failure: %+v %v", result, e)
		}
	}
	if failed.calls.Load() != 1 {
		t.Fatal("failed model was retried without backoff")
	}
	log := warnings.String()
	if strings.Count(log, `"msg":"descriptive search failed"`) != 1 || strings.Contains(log, "unavailable model query") || strings.Contains(log, "offline") || !strings.Contains(log, `"query_hash":`) || !strings.Contains(log, `"stage":"draft"`) || !strings.Contains(log, `"error_class":`) || !strings.Contains(log, `"duration":`) {
		t.Fatalf("failure logging must be once, classified and sanitized: %s", log)
	}

}

// Fake agent-model fixture, not a live or recorded response. Exercises the
// console tool loop, collection_draft, Preview and explicit personal sorting.
type relevanceReply struct {
	calls int
	msgs  []llm.Message
}

func (*relevanceReply) Enabled() bool { return true }
func (r *relevanceReply) Chat(_ context.Context, msgs []llm.Message, _ bool) (string, error) {
	r.calls++
	r.msgs = msgs
	return "```json\n" + `{"spec":{"name":"A cozy night in","mediaType":"mixed","sources":[{"kind":"catalog"}],"match":"all","rules":[{"field":"genre","op":"in","value":["Comedy","Romance","Family"]},{"field":"keyword","op":"in","value":["friendship","romantic comedy"]}],"exclude":[{"field":"genre","op":"in","value":["Horror","Thriller","Crime","War"]}],"sort":"personal","sortDesc":true,"limit":200}}` + "\n```", nil
}

type relevanceScorer struct {
	calls int
	seen  []core.Title
}

func (s *relevanceScorer) ScoreTitles(_ context.Context, _ *core.User, titles []core.Title) (map[core.RatingKey]float64, error) {
	s.calls++
	s.seen = append([]core.Title(nil), titles...)
	// Even an arbitrarily high score for a nonmatching title cannot add it.
	return map[core.RatingKey]float64{core.ShowKey(1): 1, core.MovieKey(2): 2, core.ShowKey(3): 1e9, core.MovieKey(4): 1e9, core.MovieKey(5): 1e9}, nil
}

func TestDescriptiveRelevance(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat, err := catalog.New(db, nil, config.Catalog{Language: "en-US", Region: "US"}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	fixtures := []core.Title{
		{Key: core.ShowKey(1), Type: core.TypeShow, Title: "Gilmore Girls", Genres: []string{"Comedy", "Drama"}, Keywords: []string{"friendship"}, Popularity: 100},
		{Key: core.MovieKey(2), Type: core.TypeMovie, Title: "Comfort rom-com", Genres: []string{"Romance", "Comedy"}, Keywords: []string{"romantic comedy"}, Popularity: 50},
		{Key: core.ShowKey(3), Type: core.TypeShow, Title: "House", Genres: []string{"Drama", "Mystery"}, Keywords: []string{"friendship"}, Popularity: 1000},
		{Key: core.MovieKey(4), Type: core.TypeMovie, Title: "Dark comedy", Genres: []string{"Comedy", "Horror"}, Keywords: []string{"friendship"}, Popularity: 1000},
		{Key: core.MovieKey(5), Type: core.TypeMovie, Title: "Unrelated comedy", Genres: []string{"Comedy"}, Keywords: []string{"politics"}, Popularity: 1000},
	}
	for _, title := range fixtures {
		title.IDs.TMDB = title.Key.TMDB()
		title.ContentRating = "PG"
		title.VoteCount = 100
		title.Released = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := cat.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
	}
	var logs bytes.Buffer
	svc, err := collections.New(db, cat, slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatal(err)
	}
	chat, scorer := &relevanceReply{}, &relevanceScorer{}
	setDescribeModel(t, db, cat, svc, chat)
	svc.SetScorer(scorer)
	viewer := &core.User{ID: 7}
	started := time.Now()
	got, err := svc.DescribeSearch(ctx, viewer, "cozy cute girls night in", true)
	cold := time.Since(started)
	if err != nil || got.State != "ready" {
		t.Fatalf("%+v: %v", got, err)
	}
	if len(got.Items) != 2 || got.Items[0].Key != core.MovieKey(2) || got.Items[1].Key != core.ShowKey(1) {
		t.Fatalf("rules and taste: %+v", got.Items)
	}
	if len(scorer.seen) != 2 {
		t.Fatalf("taste saw nonmatches: %+v", scorer.seen)
	}
	if !strings.Contains(chat.msgs[0].Content, "Describe a collection") || !strings.Contains(chat.msgs[1].Content, "cozy cute girls night in") {
		t.Fatal("headless turn must use the console route guidance and tools")
	}
	started = time.Now()
	cached, err := svc.DescribeSearch(ctx, viewer, "  COZY cute girls night IN  ", true)
	warm := time.Since(started)
	if err != nil || len(cached.Items) != 2 || chat.calls != 1 || scorer.calls != 1 {
		t.Fatalf("cache repeated work: model=%d scorer=%d err=%v", chat.calls, scorer.calls, err)
	}
	specJSON, _ := json.Marshal(got.Spec)
	t.Logf("fake reply spec: %s", specJSON)
	t.Logf("cold=%s cached=%s stages=%s", cold, warm, logs.String())
}

// This fake pauses between drafts so partial publication is observed before
// completion, rather than inferred from a completed cached result.
type progressiveDescribeModel struct {
	step     int
	refining chan struct{}
	resume   chan struct{}
}

func (*progressiveDescribeModel) Enabled() bool                  { return true }
func (*progressiveDescribeModel) ProviderID() string             { return "fake" }
func (*progressiveDescribeModel) ModelName() string              { return "progressive-describe" }
func (*progressiveDescribeModel) ToolsSupported() (bool, string) { return true, "" }
func (m *progressiveDescribeModel) StreamTools(ctx context.Context, _ llm.ToolRequest) (<-chan llm.Event, error) {
	m.step++
	if m.step == 2 {
		close(m.refining)
		select {
		case <-m.resume:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	out := make(chan llm.Event, 2)
	if m.step <= 2 {
		genre := "Romance"
		if m.step == 2 {
			genre = "Comedy"
		}
		raw := `{"spec":{"name":"` + genre + ` picks","mediaType":"movie","sources":[{"kind":"catalog"}],"rules":[{"field":"genre","op":"in","value":["` + genre + `"]}],"sort":"popularity","limit":200}}`
		out <- llm.Event{Kind: llm.EventToolCall, ToolCallID: genre, ToolName: "collection_draft", Input: json.RawMessage(raw)}
	} else {
		out <- llm.Event{Kind: llm.EventTextDelta, Text: "Here are the picks."}
	}
	close(out)
	return out, nil
}
func TestDescriptivePartialPublication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat, err := catalog.New(db, nil, config.Catalog{Language: "en-US", Region: "US"}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	for idx, genre := range []string{"Romance", "Comedy"} {
		title := core.Title{Key: core.MovieKey(int64(idx + 1)), IDs: core.IDs{TMDB: int64(idx + 1)}, Type: core.TypeMovie, Title: genre, Genres: []string{genre}, ContentRating: "PG", Year: 2000, Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Popularity: 100}
		if err := cat.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc, err := collections.New(db, cat, logger)
	if err != nil {
		t.Fatal(err)
	}
	model := &progressiveDescribeModel{refining: make(chan struct{}), resume: make(chan struct{})}
	defer func() {
		select {
		case <-model.resume:
		default:
			close(model.resume)
		}
	}()
	setDescribeAgentModel(t, db, cat, svc, model, logger)
	started := time.Now()
	if _, err := svc.DescribeSearch(ctx, nil, "cozy night in", false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-model.refining:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A waiting protocol job must also see progress when a nonwaiting
	// descriptive request started the shared job first.
	observed := make(chan struct{})
	completed := make(chan core.DescriptiveResult, 1)
	go func() {
		observedCtx := core.WithDescribeProgress(ctx, func(_ *core.CollectionSpec, _ []core.Title) { close(observed) })
		final, _ := svc.DescribeSearch(observedCtx, nil, "cozy night in", true)
		completed <- final
	}()
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	partial, err := svc.DescribeSearch(ctx, nil, " COZY night IN ", false)
	first := time.Since(started)
	if err != nil || partial.State != "partial" || len(partial.Items) != 1 || partial.Items[0].Title != "Romance" || partial.RetryAfterSeconds != 2 {
		t.Fatalf("first draft not visible: %+v %v", partial, err)
	}
	// Reads return copies; a client must not be able to mutate a shared job.
	partial.Items[0].Title = "Changed"
	partial.Spec.Name = "Changed"
	close(model.resume)
	var final core.DescriptiveResult
	select {
	case final = <-completed:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err != nil || final.State != "ready" || len(final.Items) != 1 || final.Items[0].Title != "Comedy" || final.Spec.Name != "Comedy picks" {
		t.Fatalf("final did not replace partial: %+v %v", final, err)
	}
	t.Logf("fake first=%s complete=%s stages=%s", first, time.Since(started), logs.String())
}
