package builtin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/agent"
	"github.com/braedonsaunders/ether/internal/agent/builtin"
	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/clientreg"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/store"
)

// realQualityEnv backs QualityRulesService with a real on-disk config.Store,
// canonical admin.ValidateConfig + library/viewer ref validation, and real
// resolver.ExplainQuality.
type realQualityEnv struct {
	cfgDir         string
	cfg            *config.Store
	libraries      map[int64]builtin.QualityLibraryRef
	viewers        map[int64]builtin.QualityViewerRef
	remoteDisabled map[int64]bool
	seenClients    map[string]core.ClientCaps
}

func newRealQualityEnv(t *testing.T) (*realQualityEnv, *tools.Catalogue, *agent.Service, *agent.Store) {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	env := &realQualityEnv{
		cfgDir: dir,
		cfg:    cfg,
		libraries: map[int64]builtin.QualityLibraryRef{
			1: {ID: 1, Name: "Movies", MediaType: "movie"},
			2: {ID: 2, Name: "TV Shows", MediaType: "show"},
			3: {ID: 3, Name: "Anime", MediaType: "show"},
			4: {ID: 4, Name: "Plugin Shelf", MediaType: "movie", ChosenByPlugin: true},
		},
		viewers: map[int64]builtin.QualityViewerRef{
			1: {ID: 1, Name: "Owner"},
			2: {ID: 2, Name: "Kids"},
		},
		remoteDisabled: map[int64]bool{2: true},
		seenClients: map[string]core.ClientCaps{
			"seen-living-room": {
				ClientID: "seen-living-room",
				Product:  "Plex for Apple TV",
				Platform: "tvOS",
				Device:   "Apple TV",
				HDR10:    true, DolbyVision: true, MaxHeight: 2160,
			},
			"seen-phone": {
				ClientID: "seen-phone",
				Product:  "Plex for iOS",
				Platform: "iOS",
				Device:   "iPhone",
				HDR10:    true, DolbyVision: true, MaxHeight: 2160,
			},
		},
	}

	_, d := newFakeEnv()
	d.QualityRules = env
	cat := tools.NewCatalogue()
	if err := cat.Register(builtin.All(d)...); err != nil {
		t.Fatal(err)
	}

	db := store.OpenTest(t)
	agentStore := agent.NewStore(db)
	agentSvc := agent.New(agent.Options{
		Catalogue: cat,
		Store:     agentStore,
	})
	return env, cat, agentSvc, agentStore
}

func (e *realQualityEnv) QualityRules(ctx context.Context, includeDevices bool) (builtin.QualityRulesSnapshot, error) {
	cfg := e.cfg.Get()
	profiles := make([]string, 0, len(cfg.Quality.Profiles))
	for _, p := range cfg.Quality.Profiles {
		profiles = append(profiles, p.Name)
	}
	libs := make([]builtin.QualityLibraryRef, 0, len(e.libraries))
	for _, id := range []int64{1, 2, 3, 4} {
		libs = append(libs, e.libraries[id])
	}
	viewers := make([]builtin.QualityViewerRef, 0, len(e.viewers))
	for _, id := range []int64{1, 2} {
		viewers = append(viewers, e.viewers[id])
	}
	snap := builtin.QualityRulesSnapshot{
		Default:           cfg.Quality.Default,
		DefaultAway:       cfg.Quality.DefaultAway,
		DefaultLimits:     cfg.Quality.DefaultLimits,
		DefaultAwayLimits: cfg.Quality.DefaultAwayLimits,
		Profiles:          profiles,
		Rules:             cfg.Quality.Rules,
		Libraries:         libs,
		Viewers:           viewers,
	}
	if includeDevices {
		apps := make([]builtin.QualityDeviceAppRef, 0, len(clientreg.Apps()))
		for _, a := range clientreg.Apps() {
			apps = append(apps, builtin.QualityDeviceAppRef{
				ID:       clientreg.AppID(a),
				Name:     a.Name,
				Protocol: a.Protocol,
			})
		}
		clients := make([]builtin.QualityDeviceClientRef, 0, len(e.seenClients))
		for id, c := range e.seenClients {
			ident := clientreg.Identity{Product: c.Product, Platform: c.Platform, Device: c.Device}
			clients = append(clients, builtin.QualityDeviceClientRef{
				ID:    id,
				Name:  c.Device,
				Group: clientreg.Class(ident),
				Type:  clientreg.DeviceType(ident),
				App:   clientreg.AppFor("", ident),
			})
		}
		snap.Devices = &builtin.QualityDevicesRef{
			Groups:  clientreg.Classes(),
			Types:   clientreg.DeviceTypes(),
			Apps:    apps,
			Clients: clients,
		}
	}
	return snap, nil
}

func (e *realQualityEnv) ExplainQuality(ctx context.Context, req builtin.QualityExplainRequest) (resolver.QualityAnswer, error) {
	caps := core.ClientCaps{Remote: req.Remote, HDR10: true, DolbyVision: true}
	switch {
	case req.ClientID == "" || req.ClientID == "any":
		if req.Client != nil {
			caps.Product = req.Client.Product
			caps.Platform = req.Client.Platform
			caps.Device = req.Client.Device
		}
	default:
		if seen, ok := e.seenClients[req.ClientID]; ok {
			caps = seen
			caps.Remote = req.Remote
		} else if dev, ok := resolver.ParseSummaryDevice(req.ClientID); ok && dev.Field == "client" {
			seen, ok := e.seenClients[dev.Value]
			if !ok {
				return resolver.QualityAnswer{}, tools.Userf("no such device or app %q", req.ClientID)
			}
			caps = seen
			caps.Remote = req.Remote
		} else {
			return resolver.QualityAnswer{}, tools.Userf("no such device or app %q", req.ClientID)
		}
	}

	var u *core.User
	if req.ViewerID > 0 {
		v, ok := e.viewers[req.ViewerID]
		if !ok {
			return resolver.QualityAnswer{}, tools.Userf("no such viewer %d", req.ViewerID)
		}
		u = &core.User{ID: v.ID, Username: v.Name, Title: v.Name}
	}

	var inLib resolver.Membership
	var chosenByPlugin string
	kind := req.Kind
	if req.LibraryID > 0 {
		lib, ok := e.libraries[req.LibraryID]
		if !ok {
			return resolver.QualityAnswer{}, tools.Userf("no such library %d", req.LibraryID)
		}
		inLib = func(id core.LibraryID) bool { return int64(id) == lib.ID }
		if lib.ChosenByPlugin {
			chosenByPlugin = "Demo Plugin"
		}
		if kind == "" {
			switch lib.MediaType {
			case "movie":
				kind = config.KindMovie
			case "show":
				kind = config.KindEpisode
			}
		}
	}

	cfg := e.cfg.Get()
	builtinCaps := caps
	q := resolver.QualityQuestion{
		Quality:        cfg.Quality,
		Caps:           caps,
		Builtin:        &builtinCaps,
		User:           u,
		Kind:           kind,
		InLibrary:      inLib,
		ChosenByPlugin: chosenByPlugin,
		RemoteDisabled: u != nil && e.remoteDisabled[u.ID],
	}
	return resolver.ExplainQuality(q), nil
}

func (e *realQualityEnv) validateConfig(c config.Config) error {
	errs := admin.ValidateConfig(c)
	for i, r := range c.Quality.Rules {
		for _, id := range r.Libraries {
			if _, ok := e.libraries[id]; !ok {
				errs = append(errs, admin.FieldError{
					Field:   fmt.Sprintf("quality.rules[%d].libraries", i),
					Message: fmt.Sprintf("names a library (%d) that does not exist", id),
				})
				break
			}
		}
		for _, id := range r.Viewers {
			if _, ok := e.viewers[id]; !ok {
				errs = append(errs, admin.FieldError{
					Field:   fmt.Sprintf("quality.rules[%d].viewers", i),
					Message: fmt.Sprintf("names a viewer (%d) that does not exist", id),
				})
				break
			}
		}
	}
	if len(errs) > 0 {
		msgs := make([]string, 0, len(errs))
		for _, fe := range errs {
			msgs = append(msgs, fe.Field+" "+fe.Message)
		}
		return tools.Userf("%s", strings.Join(msgs, "; "))
	}
	return nil
}

func (e *realQualityEnv) ValidateQuality(ctx context.Context, mutate func(*config.Quality) error) (config.Quality, error) {
	next := e.cfg.Get()
	if err := mutate(&next.Quality); err != nil {
		return config.Quality{}, err
	}
	config.AssignRuleIDs(&next.Quality)
	if err := e.validateConfig(next); err != nil {
		return config.Quality{}, err
	}
	return next.Quality, nil
}

func (e *realQualityEnv) UpdateQuality(ctx context.Context, mutate func(*config.Quality) error) (config.Quality, error) {
	if _, err := e.ValidateQuality(ctx, mutate); err != nil {
		return config.Quality{}, err
	}
	if err := e.cfg.Update(func(c *config.Config) error {
		if err := mutate(&c.Quality); err != nil {
			return err
		}
		config.AssignRuleIDs(&c.Quality)
		return nil
	}); err != nil {
		return config.Quality{}, err
	}
	return e.cfg.Get().Quality, nil
}

// proposeAndApprove runs the exact production approval flow:
//  1. Validate caller input against the tool's JSON schema.
//  2. Execute the tool to get a Proposal without mutating config.
//  3. Persist the approval into SQLite via agent.Store (JSON roundtrip).
//  4. Approve via agent.Service.DecideApproval (which re-validates Proposal.Input
//     against InputSchema, re-executes the tool, and runs Proposal.Apply).
func proposeAndApprove(t *testing.T, cat *tools.Catalogue, svc *agent.Service, st *agent.Store, name string, input map[string]any) agent.Approval {
	t.Helper()
	ctx := context.Background()
	p := tools.Principal{ViewerID: 1, IsOwner: true, Surface: "console"}
	tl, ok := cat.Lookup(ctx, p, name)
	if !ok {
		t.Fatalf("tool %q not found", name)
	}
	if err := agent.ValidateToolInput(tl.InputSchema, input); err != nil {
		t.Fatalf("input failed schema validation for %s: %v", name, err)
	}
	res, err := tl.Execute(ctx, p, input)
	if err != nil {
		t.Fatalf("Execute(%s) failed: %v", name, err)
	}
	if res.Proposal == nil {
		t.Fatalf("Execute(%s) returned nil Proposal (data=%+v)", name, res.Data)
	}

	// Ensure Proposal.Input itself roundtrips JSON and passes InputSchema.
	rawIn, err := json.Marshal(res.Proposal.Input)
	if err != nil {
		t.Fatalf("marshal Proposal.Input: %v", err)
	}
	var roundtripped map[string]any
	if err := json.Unmarshal(rawIn, &roundtripped); err != nil {
		t.Fatalf("unmarshal Proposal.Input: %v", err)
	}
	if err := agent.ValidateToolInput(tl.InputSchema, roundtripped); err != nil {
		t.Fatalf("Proposal.Input failed InputSchema validation for %s: %v (input=%s)", name, err, string(rawIn))
	}

	th, err := st.CreateThread(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := st.AppendMessage(ctx, th.ID, "assistant", json.RawMessage(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	rawDetails, _ := json.Marshal(res.Proposal.Details)
	appr, err := st.CreateApproval(ctx, agent.Approval{
		ThreadID:    th.ID,
		MessageID:   msg.ID,
		ToolName:    name,
		Category:    res.Proposal.Category,
		Description: res.Proposal.Description,
		Details:     rawDetails,
		Input:       res.Proposal.Input,
	})
	if err != nil {
		t.Fatal(err)
	}
	decided, err := svc.DecideApproval(ctx, p, appr.ID, "approved", "looks good")
	if err != nil {
		t.Fatalf("DecideApproval(%s) failed: %v", name, err)
	}
	if decided.Status != "approved" {
		t.Fatalf("DecideApproval(%s) status = %q (failure=%q)", name, decided.Status, decided.FailureReason)
	}
	return decided
}

func TestQualityRulesLifecycleAndReplay(t *testing.T) {
	ctx := context.Background()
	p := tools.Principal{ViewerID: 1, IsOwner: true, Surface: "console"}
	env, cat, svc, st := newRealQualityEnv(t)

	// 1. Initial list with includeDevices=true exposes defaults, profiles, libraries, viewers and devices.
	listRes, err := callTool(t, cat, ctx, p, "quality_rules_list", map[string]any{"includeDevices": true})
	if err != nil {
		t.Fatal(err)
	}
	if listRes.Data["default"] == "" {
		t.Fatalf("expected non-empty default profile, got %+v", listRes.Data)
	}
	devs, ok := listRes.Data["devices"].(*builtin.QualityDevicesRef)
	if !ok || devs == nil || len(devs.Groups) != 3 || len(devs.Types) == 0 || len(devs.Apps) == 0 || len(devs.Clients) != 2 {
		t.Fatalf("expected populated devices ref, got %+v", listRes.Data["devices"])
	}

	// 2. Add rule A (limits-only away bitrate cap on phones) and approve through real DecideApproval replay.
	beforeRules := len(env.cfg.Get().Quality.Rules)
	apprA := proposeAndApprove(t, cat, svc, st, "quality_rule_add", map[string]any{
		"name":           "Phones away cap",
		"devices":        []any{map[string]any{"group": clientreg.ClassMobile}},
		"where":          config.WhereAway,
		"maxBitrateKbps": float64(8000),
		"stereoAudio":    true,
	})
	ruleAMap := apprA.Result["rule"].(map[string]any)
	ruleAID, _ := ruleAMap["id"].(string)
	if !strings.HasPrefix(ruleAID, "r_") {
		t.Fatalf("expected server-assigned r_ id, got %q", ruleAID)
	}
	if len(env.cfg.Get().Quality.Rules) != beforeRules+1 {
		t.Fatalf("expected %d rules after add, got %d", beforeRules+1, len(env.cfg.Get().Quality.Rules))
	}

	// 3. Add rule B (Anime 1080p) at position 1 (highest priority).
	apprB := proposeAndApprove(t, cat, svc, st, "quality_rule_add", map[string]any{
		"name":      "Anime 1080p",
		"position":  float64(1),
		"libraries": []any{float64(3)},
		"profile":   "1080p",
		"maxHeight": float64(1080),
	})
	ruleBID := apprB.Result["rule"].(map[string]any)["id"].(string)
	if !strings.HasPrefix(ruleBID, "r_") || ruleBID == ruleAID {
		t.Fatalf("expected distinct server-assigned id, got %q vs %q", ruleBID, ruleAID)
	}

	// 4. Add rule C (Movies 4K) at the end, then move C to position 1.
	apprC := proposeAndApprove(t, cat, svc, st, "quality_rule_add", map[string]any{
		"name":      "Movies 4K",
		"libraries": []any{float64(1)},
		"profile":   "4K",
	})
	ruleCID := apprC.Result["rule"].(map[string]any)["id"].(string)

	proposeAndApprove(t, cat, svc, st, "quality_rule_move", map[string]any{
		"ruleId":   ruleCID,
		"position": float64(1),
	})

	// Order should now be [C, B, A].
	rules := env.cfg.Get().Quality.Rules
	if len(rules) != 3 || rules[0].ID != ruleCID || rules[1].ID != ruleBID || rules[2].ID != ruleAID {
		t.Fatalf("unexpected rule order after move: %+v", rules)
	}

	// Moving C to its current position (1) is a no-op with no proposal.
	noopMove, err := callTool(t, cat, ctx, p, "quality_rule_move", map[string]any{
		"ruleId":   ruleCID,
		"position": float64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if noopMove.Proposal != nil || noopMove.Data["changed"] != false {
		t.Fatalf("expected no-op move without proposal, got %+v", noopMove)
	}

	// 5. Edit rule A by its stable ID (now at position 3 after moves):
	// change maxBitrateKbps to 6000 and add a profile, preserving devices/where/stereoAudio.
	proposeAndApprove(t, cat, svc, st, "quality_rule_edit", map[string]any{
		"ruleId":         ruleAID,
		"profile":        "720p",
		"maxBitrateKbps": float64(6000),
	})
	rules = env.cfg.Get().Quality.Rules
	if rules[2].ID != ruleAID || rules[2].Profile != "720p" || rules[2].MaxBitrateKbps != 6000 || !rules[2].StereoAudio || rules[2].Where != config.WhereAway || len(rules[2].Devices) != 1 {
		t.Fatalf("partial edit did not preserve untouched fields: %+v", rules[2])
	}

	// Clear profile ("") and stereoAudio (false) on rule A, leaving maxBitrateKbps=6000 so it remains valid.
	proposeAndApprove(t, cat, svc, st, "quality_rule_edit", map[string]any{
		"ruleId":      ruleAID,
		"profile":     "",
		"stereoAudio": false,
	})
	rules = env.cfg.Get().Quality.Rules
	if rules[2].Profile != "" || rules[2].StereoAudio || rules[2].MaxBitrateKbps != 6000 {
		t.Fatalf("clearing fields failed: %+v", rules[2])
	}

	// Clear libraries ([]) on a rule with exceptLibraries=true auto-clears exceptLibraries.
	proposeAndApprove(t, cat, svc, st, "quality_rule_edit", map[string]any{
		"ruleId":          ruleBID,
		"libraries":       []any{float64(3)},
		"exceptLibraries": true,
	})
	if !env.cfg.Get().Quality.Rules[1].ExceptLibraries {
		t.Fatal("expected exceptLibraries=true")
	}
	proposeAndApprove(t, cat, svc, st, "quality_rule_edit", map[string]any{
		"ruleId":    ruleBID,
		"libraries": []any{},
	})
	if len(env.cfg.Get().Quality.Rules[1].Libraries) != 0 || env.cfg.Get().Quality.Rules[1].ExceptLibraries {
		t.Fatalf("expected libraries and exceptLibraries cleared, got %+v", env.cfg.Get().Quality.Rules[1])
	}

	// Restore rule B back to Anime library for explain checks below.
	proposeAndApprove(t, cat, svc, st, "quality_rule_edit", map[string]any{
		"ruleId":    ruleBID,
		"libraries": []any{float64(3)},
	})

	// 6. Set defaults (default and defaultAway), then clear defaultAway back to same-as-home.
	proposeAndApprove(t, cat, svc, st, "quality_defaults_set", map[string]any{
		"default":     "1080p",
		"defaultAway": "720p",
	})
	if q := env.cfg.Get().Quality; q.Default != "1080p" || q.DefaultAway != "720p" {
		t.Fatalf("unexpected defaults after update: default=%q away=%q", q.Default, q.DefaultAway)
	}

	proposeAndApprove(t, cat, svc, st, "quality_defaults_set", map[string]any{
		"defaultLimits":     map[string]any{"maxHeight": float64(720), "stereoAudio": true},
		"defaultAwayLimits": map[string]any{"maxBitrateKbps": float64(3000)},
	})
	if q := env.cfg.Get().Quality; q.DefaultLimits != (config.QualityLimits{MaxHeight: 720, StereoAudio: true}) ||
		q.DefaultAwayLimits != (config.QualityLimits{MaxBitrateKbps: 3000}) {
		t.Fatalf("unexpected defaults after limits update: %+v", q)
	}
	proposeAndApprove(t, cat, svc, st, "quality_defaults_set", map[string]any{
		"defaultLimits":     map[string]any{},
		"defaultAwayLimits": map[string]any{},
	})
	if q := env.cfg.Get().Quality; q.DefaultLimits.HasLimits() || q.DefaultAwayLimits.HasLimits() {
		t.Fatalf("limits were not cleared: %+v", q)
	}

	// Verify everything persisted cleanly to disk via config.Open.
	reopened, err := config.Open(env.cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	persisted := reopened.Get().Quality
	if persisted.Default != "1080p" || persisted.DefaultAway != "720p" || len(persisted.Rules) != 3 {
		t.Fatalf("persisted config mismatch: %+v", persisted)
	}
	if persisted.Rules[0].ID != ruleCID || persisted.Rules[1].ID != ruleBID || persisted.Rules[2].ID != ruleAID {
		t.Fatalf("persisted rule IDs/order mismatch: %+v", persisted.Rules)
	}

	// 7. Real resolver.ExplainQuality checks via quality_explain.
	// 7a. Phone away watching Anime: rule B picks 1080p + 1080p ceiling, AND rule A adds 6000 kbps cap!
	expPhoneAnime, err := callTool(t, cat, ctx, p, "quality_explain", map[string]any{
		"clientId":  "seen-phone",
		"remote":    true,
		"libraryId": float64(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if expPhoneAnime.Data["profile"] != "1080p" || expPhoneAnime.Data["profileSource"] != string(resolver.ProfileFromRule) {
		t.Fatalf("expected profile 1080p from rule, got %+v", expPhoneAnime.Data)
	}
	if expPhoneAnime.Data["profileRuleId"] != ruleBID {
		t.Fatalf("expected profileRuleId %q, got %+v", ruleBID, expPhoneAnime.Data)
	}
	if expPhoneAnime.Data["bitrateKbps"] != float64(6000) {
		t.Fatalf("expected accumulated bitrateKbps 6000 from rule A, got %+v", expPhoneAnime.Data)
	}
	if expPhoneAnime.Data["maxHeight"] != float64(1080) {
		t.Fatalf("expected maxHeight 1080, got %+v", expPhoneAnime.Data)
	}
	limitIDs, _ := expPhoneAnime.Data["limitRuleIds"].([]any)
	if len(limitIDs) != 2 || limitIDs[0] != ruleBID || limitIDs[1] != ruleAID {
		t.Fatalf("expected limitRuleIds [%s %s], got %+v", ruleBID, ruleAID, expPhoneAnime.Data["limitRuleIds"])
	}

	// 7b. Phone away watching TV Shows (no profile rule matches): falls back to defaultAway (720p) + rule A's 6000 kbps cap.
	expAwayDefault, err := callTool(t, cat, ctx, p, "quality_explain", map[string]any{
		"clientId":  "client:seen-phone",
		"remote":    true,
		"libraryId": float64(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if expAwayDefault.Data["profile"] != "720p" || expAwayDefault.Data["profileSource"] != string(resolver.ProfileFromDefaultAway) {
		t.Fatalf("expected defaultAway 720p, got %+v", expAwayDefault.Data)
	}
	if expAwayDefault.Data["bitrateKbps"] != float64(6000) {
		t.Fatalf("expected 6000 kbps cap, got %+v", expAwayDefault.Data)
	}

	// 7c. Viewer with remote disabled watching away -> blocked=true.
	expBlocked, err := callTool(t, cat, ctx, p, "quality_explain", map[string]any{
		"viewerId":  float64(2),
		"clientId":  "seen-phone",
		"remote":    true,
		"libraryId": float64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if expBlocked.Data["blocked"] != true {
		t.Fatalf("expected blocked=true, got %+v", expBlocked.Data)
	}

	// 7d. Plugin library -> profileSource="plugin" and profile layer names Demo Plugin.
	expPlugin, err := callTool(t, cat, ctx, p, "quality_explain", map[string]any{
		"clientId":  "seen-living-room",
		"libraryId": float64(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	if expPlugin.Data["profileSource"] != string(resolver.ProfileFromPlugin) {
		t.Fatalf("expected profileSource=plugin, got %+v", expPlugin.Data)
	}
	layers, _ := expPlugin.Data["layers"].([]any)
	if len(layers) == 0 || !strings.Contains(fmt.Sprint(layers[0].(map[string]any)["reason"]), "Demo Plugin") {
		t.Fatalf("expected plugin layer mentioning Demo Plugin, got %+v", layers)
	}

	// 7e. Clearing defaultAway ("") makes away plays use home default (1080p).
	proposeAndApprove(t, cat, svc, st, "quality_defaults_set", map[string]any{
		"defaultAway": "",
	})
	expAwayHome, err := callTool(t, cat, ctx, p, "quality_explain", map[string]any{
		"clientId":  "seen-phone",
		"remote":    true,
		"libraryId": float64(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if expAwayHome.Data["profile"] != "1080p" || expAwayHome.Data["profileSource"] != string(resolver.ProfileFromDefault) {
		t.Fatalf("expected home default 1080p after clearing defaultAway, got %+v", expAwayHome.Data)
	}
}

func TestQualityRulesValidationAndSchemaRejection(t *testing.T) {
	ctx := context.Background()
	p := tools.Principal{ViewerID: 1, IsOwner: true, Surface: "console"}
	env, cat, svc, st := newRealQualityEnv(t)

	// Seed one valid rule for edit/move validation tests.
	appr := proposeAndApprove(t, cat, svc, st, "quality_rule_add", map[string]any{
		"name":    "Seed",
		"profile": "4K",
	})
	seedID := appr.Result["rule"].(map[string]any)["id"].(string)

	// Validation errors must be surfaced BEFORE proposing (no proposal, no mutation).
	invalidCases := []struct {
		tool  string
		input map[string]any
		want  string
	}{
		{
			tool:  "quality_rule_add",
			input: map[string]any{"name": "No effect"},
			want:  "profile or at least one limit",
		},
		{
			tool:  "quality_rule_add",
			input: map[string]any{"profile": "DoesNotExist"},
			want:  "does not exist",
		},
		{
			tool:  "quality_rule_add",
			input: map[string]any{"profile": "4K", "libraries": []any{float64(999)}},
			want:  "does not exist",
		},
		{
			tool:  "quality_rule_add",
			input: map[string]any{"profile": "4K", "viewers": []any{float64(999)}},
			want:  "does not exist",
		},
		{
			tool:  "quality_rule_add",
			input: map[string]any{"profile": "4K", "exceptLibraries": true},
			want:  "libraries to leave out",
		},
		{
			tool:  "quality_rule_add",
			input: map[string]any{"profile": "4K", "devices": []any{map[string]any{"group": "tv", "type": "Apple TV"}}},
			want:  "exactly one",
		},
		{
			tool:  "quality_rule_add",
			input: map[string]any{"maxHeight": float64(900)},
			want:  "480, 720, 1080 or 2160",
		},
		{
			tool:  "quality_rule_add",
			input: map[string]any{"profile": "4K", "position": float64(5)},
			want:  "position must be between 1 and 2",
		},
		{
			tool:  "quality_rule_edit",
			input: map[string]any{"ruleId": "r_missing", "profile": "1080p"},
			want:  "no quality rule",
		},
		{
			tool:  "quality_rule_edit",
			input: map[string]any{"ruleId": seedID, "profile": ""},
			want:  "profile or at least one limit",
		},
		{
			tool:  "quality_rule_edit",
			input: map[string]any{"ruleId": seedID, "libraries": []any{}, "exceptLibraries": true},
			want:  "libraries to leave out",
		},
		{
			tool:  "quality_rule_move",
			input: map[string]any{"ruleId": "r_missing", "position": float64(1)},
			want:  "no quality rule",
		},
		{
			tool:  "quality_rule_move",
			input: map[string]any{"ruleId": seedID, "position": float64(2)},
			want:  "position must be between 1 and 1",
		},
		{
			tool:  "quality_defaults_set",
			input: map[string]any{},
			want:  "default",
		},
		{
			tool:  "quality_defaults_set",
			input: map[string]any{"default": "GhostProfile"},
			want:  "does not exist",
		},
		{
			tool:  "quality_defaults_set",
			input: map[string]any{"default": ""},
			want:  "default",
		},
		{
			tool:  "quality_explain",
			input: map[string]any{"clientId": "unknown-client"},
			want:  "no such device",
		},
		{
			tool:  "quality_explain",
			input: map[string]any{"clientId": "seen-phone", "client": map[string]any{"product": "Plex for iOS"}},
			want:  "not both",
		},
	}

	for _, tc := range invalidCases {
		res, err := callTool(t, cat, ctx, p, tc.tool, tc.input)
		if err == nil {
			t.Errorf("%s(%v): expected error containing %q, got result %+v", tc.tool, tc.input, tc.want, res)
			continue
		}
		if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
			t.Errorf("%s(%v): error %q does not contain %q", tc.tool, tc.input, err.Error(), tc.want)
		}
		if _, isUser := err.(*tools.UserError); !isUser {
			t.Errorf("%s(%v): expected *tools.UserError so caller sees canonical message, got %T (%v)", tc.tool, tc.input, err, err)
		}
	}

	// Verify none of the invalid calls mutated config.
	if len(env.cfg.Get().Quality.Rules) != 1 || env.cfg.Get().Quality.Rules[0].ID != seedID {
		t.Fatalf("invalid calls mutated rules: %+v", env.cfg.Get().Quality.Rules)
	}

	// Legacy `media` argument must be rejected by InputSchema across all quality tools.
	for _, name := range []string{
		"quality_profiles", "quality_order_set", "format_scores_set", "rule_add",
		"quality_rules_list", "quality_explain", "quality_rule_add", "quality_rule_edit", "quality_rule_move", "quality_defaults_set",
	} {
		tl, ok := cat.Lookup(ctx, p, name)
		if !ok {
			t.Fatalf("missing tool %q", name)
		}
		if err := agent.ValidateToolInput(tl.InputSchema, map[string]any{"media": "tv"}); err == nil {
			t.Errorf("tool %s accepted legacy media argument", name)
		}
	}
}
