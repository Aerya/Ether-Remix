package builtin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/agent"
	"github.com/braedonsaunders/ether/internal/agent/builtin"
	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/core"
)

// ambiguousProfiles fakes a server with a same-named profile for both movie
// and TV, which is how every real Ether install is set up (4K/1080p/720p
// exist for each media) and is exactly the shape that exposed the approval-
// replay bug below.
type ambiguousProfiles struct {
	*fakeEnv
	ruleMedia, scoreMedia string
}

func (f *ambiguousProfiles) Profiles(context.Context) ([]builtin.ProfileInfo, error) {
	return []builtin.ProfileInfo{
		{Name: "4K", Media: "movie"},
		{Name: "4K", Media: "tv"},
	}, nil
}

func (f *ambiguousProfiles) AddRule(_ context.Context, _, media string, rule core.ReleaseRule) error {
	f.writes++
	f.ruleMedia = media
	f.rules["4K"] = append(f.rules["4K"], rule)
	return nil
}

func (f *ambiguousProfiles) SetFormatScores(_ context.Context, _, media string, scores map[string]int) error {
	f.writes++
	f.scoreMedia = media
	return nil
}

func ambiguousTools(t *testing.T) (*ambiguousProfiles, *tools.Catalogue, tools.Principal) {
	t.Helper()
	f, d := buildDeps()
	profiles := &ambiguousProfiles{fakeEnv: f}
	d.Profiles = profiles
	cat := tools.NewCatalogue()
	if err := cat.Register(builtin.All(d)...); err != nil {
		t.Fatal(err)
	}
	return profiles, cat, tools.Principal{ViewerID: 1, IsOwner: true, Surface: "console"}
}

// replay simulates what internal/agent.Service.applyApproval actually does:
// the Proposal.Input stored in the approvals table is round-tripped through
// JSON, re-validated against the tool's schema, and used to call Execute
// again from scratch (the original closure/variables are gone - only the
// stored Input map survives an approval that waits for owner review).
func replay(t *testing.T, cat *tools.Catalogue, ctx context.Context, p tools.Principal, name string, storedInput map[string]any) (tools.Result, error) {
	t.Helper()
	tl, ok := cat.Lookup(ctx, p, name)
	if !ok {
		t.Fatalf("tool %q not visible", name)
	}
	b, err := json.Marshal(storedInput)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded map[string]any
	if err := json.Unmarshal(b, &reloaded); err != nil {
		t.Fatal(err)
	}
	if err := agent.ValidateToolInput(tl.InputSchema, reloaded); err != nil {
		return tools.Result{}, err
	}
	return tl.Execute(ctx, p, reloaded)
}

func TestRuleAddApprovalSurvivesReplayOnAmbiguousProfile(t *testing.T) {
	f, cat, p := ambiguousTools(t)
	input := map[string]any{
		"name": "Prefer English audio", "action": "prefer", "profile": "4K", "media": "tv",
		"conditions": []any{map[string]any{"field": "language", "operator": "equals", "value": "english"}},
	}
	res, err := callTool(t, cat, context.Background(), p, "rule_add", input)
	if err != nil {
		t.Fatal(err)
	}
	if res.Proposal == nil || f.writes != 0 {
		t.Fatal("must propose without writing")
	}
	// Omitting "match" (optional, left unset by the caller) must not leave an
	// invalid "" in the stored input - it has no "" member in its enum.
	if _, ok := res.Proposal.Input["match"]; ok {
		t.Fatalf("unset match must be omitted from stored input, got %v", res.Proposal.Input["match"])
	}
	if res.Proposal.Input["media"] != "tv" {
		t.Fatalf("media must be stored for replay to re-resolve the ambiguous profile, got %v", res.Proposal.Input["media"])
	}
	replayed, err := replay(t, cat, context.Background(), p, "rule_add", res.Proposal.Input)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if _, err := replayed.Proposal.Apply(context.Background()); err != nil {
		t.Fatalf("apply after replay: %v", err)
	}
	if f.writes != 1 || f.ruleMedia != "tv" || len(f.rules["4K"]) != 1 {
		t.Fatalf("applied: writes=%d media=%q rules=%v", f.writes, f.ruleMedia, f.rules["4K"])
	}
}

func TestFormatScoresSetApprovalSurvivesReplayOnAmbiguousProfile(t *testing.T) {
	f, cat, p := ambiguousTools(t)
	input := map[string]any{"profile": "4K", "media": "movie", "scores": map[string]any{"ether:viewer-language": 20000.0}}
	res, err := callTool(t, cat, context.Background(), p, "format_scores_set", input)
	if err != nil {
		t.Fatal(err)
	}
	if res.Proposal == nil || f.writes != 0 {
		t.Fatal("must propose without writing")
	}
	if res.Proposal.Input["media"] != "movie" {
		t.Fatalf("media must be stored for replay to re-resolve the ambiguous profile, got %v", res.Proposal.Input["media"])
	}
	replayed, err := replay(t, cat, context.Background(), p, "format_scores_set", res.Proposal.Input)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if _, err := replayed.Proposal.Apply(context.Background()); err != nil {
		t.Fatalf("apply after replay: %v", err)
	}
	if f.writes != 1 || f.scoreMedia != "movie" {
		t.Fatalf("applied: writes=%d media=%q", f.writes, f.scoreMedia)
	}
}
