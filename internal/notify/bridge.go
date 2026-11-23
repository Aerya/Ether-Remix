package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
)

// canonicalToLegacy maps canonical events.Bus event types (docs/design/flows.md §3.2)
// back to legacy notify.Event kinds so existing channel rules keep working unchanged.
var canonicalToLegacy = map[string]string{
	"system.server.started":      "server.started",
	"system.server.stopping":     "server.stopping",
	"system.server.crashed":      "server.crash",
	"provider.service.outage":    "provider.outage",
	"provider.service.recovered": "provider.recovered",
	"indexer.service.outage":     "indexer.outage",
	"indexer.service.recovered":  "indexer.recovered",
	"indexer.budget.exhausted":   "indexer.budget",
	"cache.budget.full":          "cache.full",
	"cache.budget.recovered":     "cache.recovered",
	"playback.session.started":   "playback.start",
	"playback.session.stopped":   "playback.stop",
	"playback.session.failed":    "playback.error",
	"playback.source.failover":   "playback.failover",
	"auth.certificate.renewed":   "cert.renewed",
	"system.update.available":    "update.available",
	"system.update.installed":    "update.installed",
	"system.update.rolled_back":  "update.rolledback",
	"system.backup.failed":       "backup.failed",
	"viewer.account.joined":      "user.joined",
	"library.title.available":    "title.available",
	"flows.digest.scheduled":     "newsletter.weekly",
}

// CanonicalToLegacyKind returns the legacy notify.Event kind for a canonical
// events.Bus event type, if one exists.
func CanonicalToLegacyKind(canonical string) (string, bool) {
	k, ok := canonicalToLegacy[canonical]
	return k, ok
}

// LegacyToCanonicalTypes returns all canonical event types that map to a legacy
// notify.Event kind (sorted deterministically).
func LegacyToCanonicalTypes(legacyKind string) []string {
	var out []string
	for canonical, leg := range canonicalToLegacy {
		if leg == legacyKind {
			out = append(out, canonical)
		}
	}
	sort.Strings(out)
	return out
}

// BridgeEventTypes returns the sorted list of canonical event types observed by
// the events.Bus -> notify bridge.
func BridgeEventTypes() []string {
	out := make([]string, 0, len(canonicalToLegacy))
	for canonical := range canonicalToLegacy {
		out = append(out, canonical)
	}
	sort.Strings(out)
	return out
}

// BridgeOptions configures an events.Bus -> notify.Service bridge.
type BridgeOptions struct {
	// Name overrides the subscription name (defaults to "notify-bridge").
	Name string
	// BufferSize overrides the subscriber queue depth (defaults to bus default).
	BufferSize int
	// EmitTimeout bounds each svc.Emit call (defaults to 5s).
	EmitTimeout time.Duration
	// SkipLegacyKinds skips bridging specific legacy kinds (e.g. if an older
	// emitter still calls notify.Service directly during staged rollout).
	SkipLegacyKinds map[string]bool
	// Logger logs bridge errors; defaults to service logger or slog.Default.
	Logger *slog.Logger
}

// BusBridge subscribes asynchronously to an events.Bus and forwards canonical
// events to legacy notify.Service channel rules without blocking publishers.
type BusBridge struct {
	sub      *events.Subscription
	bridged  atomic.Int64
	skipped  atomic.Int64
	errCount atomic.Int64
}

// BridgeStats summarizes bridge activity for tests and observability.
type BridgeStats struct {
	Bridged int64
	Skipped int64
	Errors  int64
}

// Stats returns the current bridge counters.
func (b *BusBridge) Stats() BridgeStats {
	if b == nil {
		return BridgeStats{}
	}
	return BridgeStats{
		Bridged: b.bridged.Load(),
		Skipped: b.skipped.Load(),
		Errors:  b.errCount.Load(),
	}
}

// Close stops the bridge subscription cleanly.
func (b *BusBridge) Close() {
	if b != nil && b.sub != nil {
		b.sub.Unsubscribe()
	}
}

// StartBusBridge wires bus -> svc asynchronously. Every channel that still has
// matching legacy channel.Events receives the notification identically; channels
// with no matching legacy rules are ignored without side effects.
func StartBusBridge(ctx context.Context, svc *Service, bus *events.Bus, opts BridgeOptions) (*BusBridge, error) {
	if svc == nil {
		return nil, errors.New("notify: nil service")
	}
	if bus == nil {
		return nil, errors.New("notify: nil event bus")
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "notify-bridge"
	}
	timeout := opts.EmitTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	logger := opts.Logger
	if logger == nil {
		if svc.opts.Logger != nil {
			logger = svc.opts.Logger
		} else {
			logger = slog.Default()
		}
	}
	skip := make(map[string]bool, len(opts.SkipLegacyKinds))
	for k, v := range opts.SkipLegacyKinds {
		if v {
			skip[k] = true
		}
	}

	b := &BusBridge{}
	b.sub = bus.SubscribeFunc(ctx, events.SubscriptionOptions{
		Name:          name,
		Patterns:      BridgeEventTypes(),
		Buffer:        opts.BufferSize,
		IncludeDryRun: false,
	}, func(hctx context.Context, env events.Envelope) {
		ev, ok := MapEnvelopeToLegacyEvent(env)
		if !ok || skip[ev.Kind] {
			b.skipped.Add(1)
			return
		}
		emitCtx, cancel := context.WithTimeout(hctx, timeout)
		defer cancel()
		if err := svc.Emit(emitCtx, ev); err != nil {
			b.errCount.Add(1)
			logger.Warn("notify bridge: emit failed", "event", env.Type, "legacyKind", ev.Kind, "err", err)
			return
		}
		b.bridged.Add(1)
	})
	return b, nil
}

// MapEnvelopeToLegacyEvent converts a canonical events.Envelope into the
// corresponding legacy notify.Event, returning ok=false when the event type has
// no legacy notification counterpart or lacks required fields (e.g. newsletter HTML).
func MapEnvelopeToLegacyEvent(env events.Envelope) (Event, bool) {
	legacyKind, ok := CanonicalToLegacyKind(env.Type)
	if !ok {
		return Event{}, false
	}
	var p map[string]any
	if len(env.Payload) > 0 {
		_ = json.Unmarshal(env.Payload, &p)
	}
	if p == nil {
		p = map[string]any{}
	}

	ev := Event{
		Kind:      legacyKind,
		At:        env.OccurredAt,
		HTML:      payloadStr(p, "html"),
		PosterURL: payloadStr(p, "posterUrl"),
		LinkURL:   payloadStr(p, "linkUrl"),
	}
	if legacyKind == "newsletter.weekly" && strings.TrimSpace(ev.HTML) == "" {
		// Weekly newsletter delivery requires rendered HTML; skip bare schedule ticks.
		return Event{}, false
	}

	// Honor explicit title/message overrides when provided by an emitter.
	explicitTitle := payloadStr(p, "title")
	explicitMessage := payloadStr(p, "message")

	title, msg := formatLegacyEvent(env.Type, p)
	if explicitTitle != "" {
		title = explicitTitle
	}
	if explicitMessage != "" {
		msg = explicitMessage
	}
	ev.Title = clip(title, 500)
	ev.Message = clip(msg, 8000)
	return ev, true
}

func formatLegacyEvent(eventType string, p map[string]any) (string, string) {
	switch eventType {
	case "system.server.started":
		ver := payloadStr(p, "version")
		if ver != "" {
			return "Ether started", fmt.Sprintf("Server version %s is running.", ver)
		}
		return "Ether started", "Ether server is running."

	case "system.server.stopping":
		reason := payloadStr(p, "reason")
		if reason != "" {
			return "Ether stopping", fmt.Sprintf("Server is stopping (%s).", reason)
		}
		return "Ether stopping", "Ether server is shutting down."

	case "system.server.crashed":
		reason := payloadStr(p, "exitReason")
		if reason == "" {
			reason = "Unexpected server exit."
		}
		return "Ether crashed", reason

	case "provider.service.outage":
		name := firstNonEmpty(payloadStr(p, "providerName"), payloadStr(p, "providerId"), "provider")
		errMsg := payloadStr(p, "error")
		if errMsg != "" {
			return "Provider unavailable: " + name, errMsg
		}
		return "Provider unavailable: " + name, fmt.Sprintf("Provider %s is unreachable.", name)

	case "provider.service.recovered":
		name := firstNonEmpty(payloadStr(p, "providerName"), payloadStr(p, "providerId"), "provider")
		return "Provider recovered: " + name, fmt.Sprintf("Provider %s is healthy again.", name)

	case "indexer.service.outage":
		name := firstNonEmpty(payloadStr(p, "indexerName"), payloadStr(p, "indexerId"), "indexer")
		errMsg := payloadStr(p, "error")
		if errMsg != "" {
			return "Indexer failing: " + name, errMsg
		}
		return "Indexer failing: " + name, fmt.Sprintf("Indexer %s is failing requests.", name)

	case "indexer.service.recovered":
		name := firstNonEmpty(payloadStr(p, "indexerName"), payloadStr(p, "indexerId"), "indexer")
		return "Indexer recovered: " + name, fmt.Sprintf("Indexer %s is responding normally again.", name)

	case "indexer.budget.exhausted":
		name := firstNonEmpty(payloadStr(p, "indexerName"), payloadStr(p, "indexerId"), "indexer")
		metric := firstNonEmpty(payloadStr(p, "metric"), "quota")
		return "Indexer budget exhausted: " + name, fmt.Sprintf("Indexer %s reached its %s limit.", name, metric)

	case "cache.budget.full":
		kind := firstNonEmpty(payloadStr(p, "cacheKind"), "cache")
		return "Cache full", fmt.Sprintf("Cache (%s) reached its storage budget.", kind)

	case "cache.budget.recovered":
		kind := firstNonEmpty(payloadStr(p, "cacheKind"), "cache")
		return "Cache below budget", fmt.Sprintf("Cache (%s) dropped back below its storage budget.", kind)

	case "playback.session.started":
		return "Playback started", joinNonEmpty(": ",
			firstNonEmpty(payloadStr(p, "viewer", "username"), payloadStr(p, "username")),
			firstNonEmpty(payloadStr(p, "titleName"), payloadStr(p, "titleKey"), "stream"))

	case "playback.session.stopped":
		return "Playback stopped", joinNonEmpty(": ",
			firstNonEmpty(payloadStr(p, "viewer", "username"), payloadStr(p, "username")),
			firstNonEmpty(payloadStr(p, "titleName"), payloadStr(p, "titleKey"), "stream"))

	case "playback.session.failed":
		errMsg := payloadStr(p, "error")
		subject := joinNonEmpty(": ",
			firstNonEmpty(payloadStr(p, "viewer", "username"), payloadStr(p, "username")),
			firstNonEmpty(payloadStr(p, "titleName"), payloadStr(p, "titleKey")))
		return "Playback error", joinNonEmpty(" — ", subject, firstNonEmpty(errMsg, "Playback failed."))

	case "playback.source.failover":
		title := firstNonEmpty(payloadStr(p, "titleName"), payloadStr(p, "titleKey"), "stream")
		reason := payloadStr(p, "reason")
		if reason != "" {
			return "Playback failover", fmt.Sprintf("%s switched release (%s).", title, reason)
		}
		return "Playback failover", fmt.Sprintf("%s switched to a fallback release.", title)

	case "auth.certificate.renewed":
		issuer := payloadStr(p, "issuer")
		if issuer != "" {
			return "TLS certificate renewed", fmt.Sprintf("Renewed TLS certificate from %s.", issuer)
		}
		return "TLS certificate renewed", "TLS certificate was renewed."

	case "system.update.available":
		nv := payloadStr(p, "newVersion")
		if nv != "" {
			return "Ether update available", fmt.Sprintf("Version %s is available.", nv)
		}
		return "Ether update available", "A new version of Ether is available."

	case "system.update.installed":
		tv := payloadStr(p, "toVersion")
		if tv != "" {
			return "Ether update installed", fmt.Sprintf("Updated to version %s.", tv)
		}
		return "Ether update installed", "An Ether update was installed."

	case "system.update.rolled_back":
		rv := payloadStr(p, "restoredVersion")
		reason := payloadStr(p, "reason")
		return "Ether update rolled back", joinNonEmpty(" — ", firstNonEmpty(rv, "Restored previous version"), reason)

	case "system.backup.failed":
		kind := firstNonEmpty(payloadStr(p, "kind"), "scheduled")
		errMsg := firstNonEmpty(payloadStr(p, "error"), "Backup failed.")
		return "Backup failed (" + kind + ")", errMsg

	case "viewer.account.joined":
		user := firstNonEmpty(payloadStr(p, "username"), payloadStr(p, "viewerId"), "A viewer")
		return "Viewer joined", fmt.Sprintf("%s joined your server.", user)

	case "library.title.available":
		title := firstNonEmpty(payloadStr(p, "titleName"), payloadStr(p, "titleKey"), "Title")
		return "Title available", title

	case "flows.digest.scheduled":
		return firstNonEmpty(payloadStr(p, "subject"), "Weekly newsletter"), firstNonEmpty(payloadStr(p, "text"), "Your weekly digest is ready.")
	}
	return eventType, ""
}

func payloadStr(m map[string]any, path ...string) string {
	var cur any = m
	for _, seg := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = mm[seg]
		if !ok {
			return ""
		}
	}
	switch v := cur.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return ""
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func joinNonEmpty(sep string, vals ...string) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, sep)
}
