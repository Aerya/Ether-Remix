package notify

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestDiscordRichEmbedAndPlainFallback(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var payloads []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode discord body: %v", err)
		}
		payloads = append(payloads, body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	fixed := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	svc := New(db, Options{Client: srv.Client(), Now: func() time.Time { return fixed }})
	if err := svc.Save(ctx, Channel{
		ID:      "disc",
		Name:    "Ops Discord",
		Kind:    "discord",
		Enabled: true,
		Secrets: map[string]string{"endpoint": srv.URL},
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Plain event uses content (backwards compatible).
	res, err := svc.DeliverToChannels(ctx, []string{"disc"}, Event{
		Title:   "Plain alert",
		Message: "Simple body",
	}, DeliverOptions{BypassDigest: true})
	if err != nil || len(res) != 1 || res[0].Status != "sent" {
		t.Fatalf("plain deliver = %+v, err=%v", res, err)
	}
	if payloads[0]["content"] == nil || payloads[0]["embeds"] != nil {
		t.Fatalf("expected plain discord payload, got %#v", payloads[0])
	}

	// 2. Rich event sends embeds with color, thumbnail, fields, url, footer, timestamp, and empty allowed_mentions.
	res, err = svc.DeliverToChannels(ctx, []string{"disc"}, Event{
		Title:     "4K Transcode Stopped",
		Message:   "Remote 4K transcoding is disabled.",
		Color:     "#EF4444",
		PosterURL: "https://images.example.test/poster.jpg",
		LinkURL:   "https://ether.example.test/title/550",
		Footer:    "Ether Flows",
		Fields: []EmbedField{
			{Name: "Viewer", Value: "alex", Inline: true},
			{Name: "Resolution", Value: "4K HDR10", Inline: true},
		},
	}, DeliverOptions{BypassDigest: true})
	if err != nil || len(res) != 1 || res[0].Status != "sent" {
		t.Fatalf("rich deliver = %+v, err=%v", res, err)
	}
	embeds, ok := payloads[1]["embeds"].([]any)
	if !ok || len(embeds) != 1 {
		t.Fatalf("expected 1 embed, got %#v", payloads[1])
	}
	emb := embeds[0].(map[string]any)
	if emb["title"] != "4K Transcode Stopped" {
		t.Fatalf("embed title = %v", emb["title"])
	}
	if int(emb["color"].(float64)) != 0xEF4444 {
		t.Fatalf("embed color = %v, want %d", emb["color"], 0xEF4444)
	}
	if emb["url"] != "https://ether.example.test/title/550" {
		t.Fatalf("embed url = %v", emb["url"])
	}
	thumb := emb["thumbnail"].(map[string]any)
	if thumb["url"] != "https://images.example.test/poster.jpg" {
		t.Fatalf("embed thumbnail = %v", thumb)
	}
	fields := emb["fields"].([]any)
	if len(fields) != 2 {
		t.Fatalf("embed fields len = %d", len(fields))
	}
	footer := emb["footer"].(map[string]any)
	if footer["text"] != "Ether Flows" {
		t.Fatalf("embed footer = %v", footer)
	}
	if emb["timestamp"] != fixed.Format(time.RFC3339) {
		t.Fatalf("embed timestamp = %v", emb["timestamp"])
	}
}

func TestPushoverTransportAndPriority(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode pushover: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origEndpoint := pushoverEndpoint
	pushoverEndpoint = srv.URL
	t.Cleanup(func() { pushoverEndpoint = origEndpoint })

	svc := New(db, Options{Client: srv.Client()})
	if err := svc.Save(ctx, Channel{
		ID:       "po",
		Name:     "Phone",
		Kind:     "pushover",
		Enabled:  true,
		Settings: map[string]string{"device": "pixel", "sound": "siren"},
		Secrets:  map[string]string{"token": "app-secret", "user": "user-secret"},
	}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.DeliverToChannels(ctx, []string{"po"}, Event{
		Title:    "Critical outage",
		Message:  "Database disconnected",
		Priority: 2,
		LinkURL:  "https://ether.example.test/status",
	}, DeliverOptions{BypassDigest: true})
	if err != nil || len(res) != 1 || res[0].Status != "sent" {
		t.Fatalf("pushover deliver = %+v, err=%v", res, err)
	}
	if got["token"] != "app-secret" || got["user"] != "user-secret" || got["device"] != "pixel" || got["sound"] != "siren" {
		t.Fatalf("pushover body = %#v", got)
	}
	if int(got["priority"].(float64)) != 2 || int(got["retry"].(float64)) != 60 || int(got["expire"].(float64)) != 3600 {
		t.Fatalf("pushover emergency fields = %#v", got)
	}
}

type capturedMQTT struct {
	ClientID string
	Username string
	Password string
	Topic    string
	Payload  []byte
	QoS      byte
	Retain   bool
}

func startMockMQTTBroker(t *testing.T, connackCode byte) (addr string, recv <-chan capturedMQTT, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan capturedMQTT, 16)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleMockMQTTConn(conn, connackCode, ch)
		}
	}()
	return ln.Addr().String(), ch, func() {
		_ = ln.Close()
		wg.Wait()
	}
}

func handleMockMQTTConn(conn net.Conn, connackCode byte, out chan<- capturedMQTT) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	var capMsg capturedMQTT

	// Expect CONNECT
	pt, _, body, err := readMQTTPacket(r)
	if err != nil || pt != 0x01 {
		return
	}
	idx := 0
	readStr := func() string {
		if idx+2 > len(body) {
			return ""
		}
		n := int(binary.BigEndian.Uint16(body[idx : idx+2]))
		idx += 2
		if idx+n > len(body) {
			return ""
		}
		s := string(body[idx : idx+n])
		idx += n
		return s
	}
	_ = readStr() // "MQTT"
	if idx+4 > len(body) {
		return
	}
	idx++ // level
	flags := body[idx]
	idx += 3 // flags + keepalive
	capMsg.ClientID = readStr()
	if flags&0x80 != 0 {
		capMsg.Username = readStr()
	}
	if flags&0x40 != 0 {
		capMsg.Password = readStr()
	}
	// Send CONNACK
	_, _ = conn.Write([]byte{0x20, 0x02, 0x00, connackCode})
	if connackCode != 0x00 {
		return
	}

	// Read PUBLISH
	pt, pubFlags, pubBody, err := readMQTTPacket(r)
	if err != nil || pt != 0x03 {
		return
	}
	capMsg.QoS = (pubFlags >> 1) & 0x03
	capMsg.Retain = (pubFlags & 0x01) != 0
	if len(pubBody) < 2 {
		return
	}
	tlen := int(binary.BigEndian.Uint16(pubBody[:2]))
	if 2+tlen > len(pubBody) {
		return
	}
	capMsg.Topic = string(pubBody[2 : 2+tlen])
	pos := 2 + tlen
	var pktID uint16
	if capMsg.QoS > 0 {
		if pos+2 > len(pubBody) {
			return
		}
		pktID = binary.BigEndian.Uint16(pubBody[pos : pos+2])
		pos += 2
	}
	capMsg.Payload = append([]byte(nil), pubBody[pos:]...)

	switch capMsg.QoS {
	case 1:
		_, _ = conn.Write([]byte{0x40, 0x02, byte(pktID >> 8), byte(pktID)})
	case 2:
		_, _ = conn.Write([]byte{0x50, 0x02, byte(pktID >> 8), byte(pktID)})
		relType, _, relBody, err := readMQTTPacket(r)
		if err != nil || relType != 0x06 || len(relBody) != 2 {
			return
		}
		_, _ = conn.Write([]byte{0x70, 0x02, relBody[0], relBody[1]})
	}
	out <- capMsg
	// Drain optional DISCONNECT
	_, _ = io.ReadFull(r, make([]byte, 2))
}

func TestMQTTPublishQoS012AndChannelBroker(t *testing.T) {
	addr, recv, stop := startMockMQTTBroker(t, 0x00)
	defer stop()

	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	svc := New(db, Options{Sealer: upgradedSealer(t)})
	if err := svc.Save(ctx, Channel{
		ID:      "ha-mqtt",
		Name:    "Home Assistant MQTT",
		Kind:    "mqtt",
		Enabled: true,
		Settings: map[string]string{
			"tlsMode":  "none",
			"topic":    "ether/alerts",
			"clientId": "ether-test",
			"qos":      "1",
		},
		Secrets: map[string]string{
			"endpoint": addr,
			"username": "ha-user",
			"password": "ha-secret-password",
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Verify channel notification delivery via MQTT (QoS 1).
	res, err := svc.DeliverToChannels(ctx, []string{"ha-mqtt"}, Event{
		Title:   "Movie started",
		Message: "Dune on Apple TV",
	}, DeliverOptions{BypassDigest: true})
	if err != nil || len(res) != 1 || res[0].Status != "sent" {
		t.Fatalf("mqtt notify deliver = %+v, err=%v", res, err)
	}
	msg := <-recv
	if msg.ClientID != "ether-test" || msg.Username != "ha-user" || msg.Password != "ha-secret-password" {
		t.Fatalf("mqtt connect auth = %+v", msg)
	}
	if msg.Topic != "ether/alerts" || msg.QoS != 1 || !strings.Contains(string(msg.Payload), "Movie started") {
		t.Fatalf("mqtt notify payload = %+v (%s)", msg, string(msg.Payload))
	}

	// Verify Service.PublishMQTT for QoS 0, 1, 2 and retain=true.
	for _, qos := range []byte{0, 1, 2} {
		payload := []byte(`{"scene":"cinema_dim","qos":` + string('0'+qos) + `}`)
		if err := svc.PublishMQTT(ctx, "ha-mqtt", "home/livingroom/lights", payload, qos, true); err != nil {
			t.Fatalf("PublishMQTT qos=%d: %v", qos, err)
		}
		got := <-recv
		if got.Topic != "home/livingroom/lights" || got.QoS != qos || !got.Retain || string(got.Payload) != string(payload) {
			t.Fatalf("qos=%d got %+v (%s)", qos, got, string(got.Payload))
		}
	}

	// Verify auth refusal never leaks password.
	badAddr, _, stopBad := startMockMQTTBroker(t, 0x04)
	defer stopBad()
	err = PublishMQTT(ctx, MQTTConfig{
		Endpoint: badAddr,
		Username: "ha-user",
		Password: "super-secret-do-not-leak",
	}, MQTTMessage{Topic: "home/test", Payload: []byte("hi")})
	if err == nil || strings.Contains(err.Error(), "super-secret-do-not-leak") {
		t.Fatalf("expected redacted auth error, got %v", err)
	}
}

func TestDeliverToChannelsDigestRateLimitAndMissing(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	svc := New(db, Options{Client: srv.Client(), Now: func() time.Time { return now }})
	if err := svc.Save(ctx, Channel{
		ID:            "fast",
		Name:          "Fast",
		Kind:          "webhook",
		Enabled:       true,
		PerMinute:     1,
		DigestMinutes: 0,
		Secrets:       map[string]string{"endpoint": srv.URL},
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Save(ctx, Channel{
		ID:            "digest",
		Name:          "Digest",
		Kind:          "webhook",
		Enabled:       true,
		PerMinute:     5,
		DigestMinutes: 15,
		Secrets:       map[string]string{"endpoint": srv.URL},
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Save(ctx, Channel{
		ID:      "off",
		Name:    "Disabled",
		Kind:    "webhook",
		Enabled: false,
		Secrets: map[string]string{"endpoint": srv.URL},
	}); err != nil {
		t.Fatal(err)
	}

	// First send to fast + digest + off + missing.
	res, err := svc.DeliverToChannels(ctx, []string{"fast", "digest", "off", "missing"}, Event{
		Title:   "Alert 1",
		Message: "Body 1",
	}, DeliverOptions{BypassDigest: false})
	if err != nil {
		t.Fatalf("DeliverToChannels: %v", err)
	}
	byID := map[string]DeliverResult{}
	for _, r := range res {
		byID[r.ChannelID] = r
	}
	if byID["fast"].Status != "sent" || byID["digest"].Status != "queued" || byID["off"].Status != "skipped" || byID["missing"].Status != "skipped" {
		t.Fatalf("unexpected results: %+v", byID)
	}
	if hits != 1 {
		t.Fatalf("hits = %d, want 1", hits)
	}

	// Second send to fast within same minute hits PerMinute=1 -> queued instead of sent immediately.
	res2, err := svc.DeliverToChannels(ctx, []string{"fast"}, Event{
		Title:   "Alert 2",
		Message: "Body 2",
	}, DeliverOptions{BypassDigest: true})
	if err != nil || len(res2) != 1 || res2[0].Status != "queued" {
		t.Fatalf("rate-limited deliver = %+v, err=%v", res2, err)
	}
	if hits != 1 {
		t.Fatalf("rate-limited send should not hit server yet; hits = %d", hits)
	}
}

func TestEventsBusToNotifyBridge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	received := make(chan Event, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e Event
		if err := json.NewDecoder(r.Body).Decode(&e); err == nil {
			received <- e
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc := New(db, Options{Client: srv.Client()})
	if err := svc.Save(ctx, Channel{
		ID:      "legacy",
		Name:    "Legacy webhook",
		Kind:    "webhook",
		Enabled: true,
		Events:  []string{"playback.error", "provider.outage", "title.available"},
		Secrets: map[string]string{"endpoint": srv.URL},
	}); err != nil {
		t.Fatal(err)
	}
	// Channel with no legacy events (migrated to Flows) must not receive bridged legacy notifications.
	migratedHit := false
	migratedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		migratedHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer migratedSrv.Close()
	if err := svc.Save(ctx, Channel{
		ID:      "migrated",
		Name:    "Flows only",
		Kind:    "webhook",
		Enabled: true,
		Events:  nil,
		Secrets: map[string]string{"endpoint": migratedSrv.URL},
	}); err != nil {
		t.Fatal(err)
	}

	bus, _ := events.NewTestBus(t)
	bridge, err := StartBusBridge(ctx, svc, bus, BridgeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	emitted := make(chan Event, 16)
	unsub := svc.Subscribe(func(e Event) {
		emitted <- e
	})
	defer unsub()

	// Publish canonical events (never blocks publisher).
	bus.Publish(events.Envelope{
		Type:    "playback.session.failed",
		Payload: json.RawMessage(`{"titleName":"Blade Runner","error":"upstream 502","viewer":{"username":"alice"}}`),
	})
	bus.Publish(events.Envelope{
		Type:    "provider.service.outage",
		Payload: json.RawMessage(`{"providerName":"NewsDemon","error":"connection reset"}`),
	})
	// Dry-run event must be ignored by bridge.
	bus.Publish(events.Envelope{
		Type:    "library.title.available",
		Meta:    events.EnvelopeMeta{DryRun: true},
		Payload: json.RawMessage(`{"titleName":"Dry Run Movie"}`),
	})
	// Real title available.
	bus.Publish(events.Envelope{
		Type:    "library.title.available",
		Payload: json.RawMessage(`{"titleName":"Arrival"}`),
	})

	gotKinds := map[string]Event{}
	deadline := time.After(5 * time.Second)
	for len(gotKinds) < 3 {
		select {
		case e := <-emitted:
			gotKinds[e.Kind] = e
		case <-deadline:
			t.Fatalf("timed out waiting for bridged events, got %v", gotKinds)
		}
	}
	if !strings.Contains(gotKinds["playback.error"].Message, "Blade Runner") || !strings.Contains(gotKinds["playback.error"].Message, "upstream 502") {
		t.Fatalf("playback.error event = %+v", gotKinds["playback.error"])
	}
	if !strings.Contains(gotKinds["provider.outage"].Title, "NewsDemon") {
		t.Fatalf("provider.outage event = %+v", gotKinds["provider.outage"])
	}
	if gotKinds["title.available"].Message != "Arrival" {
		t.Fatalf("title.available event = %+v", gotKinds["title.available"])
	}

	// Drain queue and verify all 3 delivered to legacy channel and 0 to migrated channel.
	for i := 0; i < 4; i++ {
		if err := svc.Drain(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(received) != 3 {
		t.Fatalf("legacy webhook received %d events, want 3", len(received))
	}
	if migratedHit {
		t.Fatal("migrated channel with empty Events should not have received legacy bridged events")
	}

	// Verify mapping coverage across every legacy kind except "test".
	for _, leg := range Kinds {
		if leg == "test" {
			continue
		}
		if len(LegacyToCanonicalTypes(leg)) == 0 {
			t.Errorf("legacy notify kind %q has no canonical event mapping", leg)
		}
	}
}
