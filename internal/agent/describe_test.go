package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/llm"
)

func TestHeadlessDescribeUsesConsoleAgent(t *testing.T) {
	model := &fakeModel{scripts: [][]llm.Event{
		append(callEv("first", "collection_draft", `{"title":"Initial"}`), callEv("forbidden", "release_block", `{"title":"Must not run"}`)...),
		callEv("last", "collection_draft", `{"title":"Comfort picks"}`),
		{{Kind: llm.EventTextDelta, Text: "Here are the picks."}},
	}}
	fx := newFixture(t, model, Options{})
	tool := tools.Tool{Name: "collection_draft", Title: "Draft", Description: "Draft the collection", ReadOnly: true, Audience: tools.AudienceOwner, Owner: "builtin",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`),
		Execute: func(ctx context.Context, p tools.Principal, in map[string]any) (tools.Result, error) {
			if !p.IsOwner || core.CatalogViewerID(ctx) != 0 {
				t.Error("agent tools inherited requesting viewer")
			}
			return tools.Result{Data: map[string]any{"spec": core.CollectionSpec{Name: in["title"].(string), MediaType: "mixed"}}}, nil
		},
	}
	if err := fx.svc.catalogue.Register(tool); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithCatalogViewer(context.Background(), &core.User{ID: 99, MaxContentRating: "PG"})
	spec, err := fx.svc.DescribeCollection(ctx, "cozy cute girls night in")
	if err != nil || spec == nil || spec.Name != "Comfort picks" {
		t.Fatalf("last draft: %+v %v", spec, err)
	}
	threads, err := fx.store.ListThreads(context.Background(), 50)
	if err != nil || len(threads) != 0 {
		t.Fatalf("headless history remained: %+v %v", threads, err)
	}
	var messages, approvals int
	if err := fx.store.db.QueryRow(`SELECT COUNT(*) FROM agent_messages`).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err := fx.store.db.QueryRow(`SELECT COUNT(*) FROM agent_approvals`).Scan(&approvals); err != nil {
		t.Fatal(err)
	}
	if messages != 0 || approvals != 0 || len(*fx.applied) != 0 {
		t.Fatalf("headless persistence/write: messages=%d approvals=%d applied=%v", messages, approvals, *fx.applied)
	}
	for _, req := range model.requests {
		for _, def := range req.Tools {
			if def.Name != "collection_draft" && def.Name != "catalog_search" && def.Name != "title_details" {
				t.Fatalf("unexpected tool %s", def.Name)
			}
		}
	}
	// Compare with an ordinary console turn using the same live service. The
	// system prompt and offered tools must be identical, not search variants.
	_, _, err = fx.svc.RunTurn(context.Background(), fx.p, "", "cozy cute girls night in", TurnContext{Route: DescribeCollectionRoute}, func(Chunk) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	first, last := model.requests[0], model.requests[len(model.requests)-1]
	if first.System != last.System || !reflect.DeepEqual(first.Tools, last.Tools) {
		t.Fatal("headless prompt/tools diverged from console")
	}
}
