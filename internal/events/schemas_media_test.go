package events

import (
	"encoding/json"
	"testing"
)

func TestMediaDomainSchemasComplete(t *testing.T) {
	reg := NewBuiltinRegistry()

	expected := map[string]struct {
		domain   string
		critical bool
		legacy   []string
	}{
		// playback (17)
		EventPlaybackSessionStarted:       {domain: "playback", legacy: []string{"playback.started", "playback.start"}},
		EventPlaybackSessionPaused:        {domain: "playback", legacy: []string{"playback.paused"}},
		EventPlaybackSessionResumed:       {domain: "playback", legacy: []string{"playback.resumed"}},
		EventPlaybackSessionStopped:       {domain: "playback", legacy: []string{"playback.stopped", "playback.stop"}},
		EventPlaybackSessionCompleted:     {domain: "playback", legacy: []string{"playback.completed"}},
		EventPlaybackSessionFailed:        {domain: "playback", critical: true, legacy: []string{"playback.error"}},
		EventPlaybackSessionTerminated:    {domain: "playback"},
		EventPlaybackSourceFailover:       {domain: "playback", legacy: []string{"playback.failover"}},
		EventPlaybackStreamStalled:        {domain: "playback"},
		EventPlaybackStreamDecision:       {domain: "playback"},
		EventPlaybackStreamQualityChanged: {domain: "playback"},
		EventPlaybackStreamSeeked:         {domain: "playback"},
		EventPlaybackMarkerEntered:        {domain: "playback"},
		EventPlaybackMarkerExited:         {domain: "playback"},
		EventPlaybackConcurrencyExceeded:  {domain: "playback"},
		EventPlaybackAdmissionDenied:      {domain: "playback"},
		EventPlaybackTranscodeFallback:    {domain: "playback"},

		// livetv (12)
		EventLiveTVChannelTuned:         {domain: "livetv"},
		EventLiveTVChannelStopped:       {domain: "livetv"},
		EventLiveTVInputStalled:         {domain: "livetv"},
		EventLiveTVInputRecovered:       {domain: "livetv"},
		EventLiveTVInputLost:            {domain: "livetv", critical: true},
		EventLiveTVSourceFailover:       {domain: "livetv"},
		EventLiveTVSourceDegraded:       {domain: "livetv"},
		EventLiveTVSourceCooldown:       {domain: "livetv"},
		EventLiveTVProviderAuthFailed:   {domain: "livetv", critical: true},
		EventLiveTVProviderLimitReached: {domain: "livetv"},
		EventLiveTVGuideRefreshed:       {domain: "livetv"},
		EventLiveTVGuideFailed:          {domain: "livetv"},

		// resolver (8)
		EventResolverSearchCompleted:    {domain: "resolver"},
		EventResolverSearchEmpty:        {domain: "resolver"},
		EventResolverReleaseSelected:    {domain: "resolver"},
		EventResolverReleaseBlocklisted: {domain: "resolver"},
		EventResolverPinCreated:         {domain: "resolver"},
		EventResolverPinRemoved:         {domain: "resolver"},
		EventResolverPinUnhealthy:       {domain: "resolver", critical: true},
		EventResolverPinRejected:        {domain: "resolver"},

		// indexer (6)
		EventIndexerRequestFailed:    {domain: "indexer"},
		EventIndexerServiceOutage:    {domain: "indexer", critical: true, legacy: []string{"indexer.outage"}},
		EventIndexerServiceRecovered: {domain: "indexer", legacy: []string{"indexer.recovered"}},
		EventIndexerBudgetWarning:    {domain: "indexer"},
		EventIndexerBudgetExhausted:  {domain: "indexer", legacy: []string{"indexer.budget"}},
		EventIndexerRateLimited:      {domain: "indexer"},

		// provider (8)
		EventProviderServiceOutage:        {domain: "provider", critical: true, legacy: []string{"provider.outage"}},
		EventProviderServiceRecovered:     {domain: "provider", legacy: []string{"provider.recovered"}},
		EventProviderAuthRejected:         {domain: "provider", critical: true},
		EventProviderQuotaWarning:         {domain: "provider"},
		EventProviderQuotaExhausted:       {domain: "provider", critical: true},
		EventProviderDebridCached:         {domain: "provider"},
		EventProviderDebridFailed:         {domain: "provider"},
		EventProviderConnectionsSaturated: {domain: "provider"},

		// vault (5)
		EventVaultRecordStored:   {domain: "vault"},
		EventVaultRecordDead:     {domain: "vault"},
		EventVaultPayloadMissing: {domain: "vault"},
		EventVaultPackCompacted:  {domain: "vault"},
		EventVaultQuotaWarning:   {domain: "vault"},

		// cache (5)
		EventCacheBudgetFull:        {domain: "cache", critical: true, legacy: []string{"cache.full"}},
		EventCacheBudgetRecovered:   {domain: "cache", legacy: []string{"cache.recovered"}},
		EventCacheEvictionSpike:     {domain: "cache"},
		EventCachePrefetchCompleted: {domain: "cache"},
		EventCachePrefetchFailed:    {domain: "cache"},
	}

	if len(expected) != 61 {
		t.Fatalf("expected map size = %d, want 61", len(expected))
	}

	for typ, want := range expected {
		s, ok := reg.Get(typ)
		if !ok {
			t.Errorf("missing registered schema for %q", typ)
			continue
		}
		if s.Domain != want.domain {
			t.Errorf("%s: domain = %q, want %q", typ, s.Domain, want.domain)
		}
		if s.Critical != want.critical {
			t.Errorf("%s: critical = %v, want %v", typ, s.Critical, want.critical)
		}
		if s.Owner != OwnerBuiltin {
			t.Errorf("%s: owner = %q, want %q", typ, s.Owner, OwnerBuiltin)
		}
		if len(s.Fields) == 0 {
			t.Errorf("%s: schema has no fields", typ)
		}
		if s.Sample == nil {
			t.Errorf("%s: schema has nil sample", typ)
		} else {
			raw, err := json.Marshal(s.Sample)
			if err != nil {
				t.Errorf("%s: marshal sample: %v", typ, err)
			} else if err := reg.ValidatePayload(typ, raw); err != nil {
				t.Errorf("%s: sample fails ValidatePayload: %v (raw=%s)", typ, err, string(raw))
			}
		}
		for _, alias := range want.legacy {
			got, ok := reg.GetByLegacy(alias)
			if !ok {
				t.Errorf("%s: legacy alias %q not registered", typ, alias)
			} else if got.Type != typ {
				t.Errorf("%s: legacy alias %q resolved to %q", typ, alias, got.Type)
			}
		}
	}
}

func TestPlaybackSessionContextFieldsAndPIIRedaction(t *testing.T) {
	reg := NewBuiltinRegistry()

	s, ok := reg.Get(EventPlaybackSessionStarted)
	if !ok {
		t.Fatalf("missing %s", EventPlaybackSessionStarted)
	}
	fieldByName := make(map[string]FieldSchema, len(s.Fields))
	for _, f := range s.Fields {
		fieldByName[f.Name] = f
	}
	requiredFields := []string{
		"sessionId", "playId",
		"titleKey", "titleName", "showTitle", "season", "episode", "mediaType", "year",
		"libraryId", "libraryName", "contentRating", "genres", "labels", "actors", "directors",
		"studio", "durationMs", "posterUrl",
		"source.container", "source.videoCodec", "source.videoResolution", "source.videoWidth",
		"source.videoHeight", "source.videoDynamicRange", "source.audioCodec", "source.audioChannels",
		"source.bitrateKbps", "source.fileSizeBytes",
		"stream.delivery", "stream.videoDecision", "stream.audioDecision", "stream.subtitleDecision",
		"stream.container", "stream.videoCodec", "stream.videoResolution", "stream.videoHeight",
		"stream.videoDynamicRange", "stream.audioCodec", "stream.audioChannels", "stream.bitrateKbps",
		"stream.bandwidthKbps", "stream.hwDecode", "stream.hwEncode", "stream.transcodeSpeed", "stream.throttled",
		"viewer.id", "viewer.username", "viewer.role", "viewer.tags", "viewer.activeStreams",
		"viewer.activeTranscodes", "viewer.distinctActiveIPs",
		"client.id", "client.name", "client.platform", "client.product", "client.version", "client.protocol",
		"network.remoteIP", "network.location", "network.country", "network.city", "network.asn", "network.isRelay",
		"server.activeStreams", "server.activeTranscodes", "server.activeDirectPlays", "server.totalBandwidthKbps",
	}
	for _, name := range requiredFields {
		if _, exists := fieldByName[name]; !exists {
			t.Errorf("%s missing expected field %q", EventPlaybackSessionStarted, name)
		}
	}

	if !fieldByName["viewer.username"].PII {
		t.Error("viewer.username must be marked PII")
	}
	if !fieldByName["network.remoteIP"].PII {
		t.Error("network.remoteIP must be marked PII")
	}

	// Verify PII redaction strips nested PII fields while leaving non-PII intact.
	raw, err := json.Marshal(PlaybackSessionStartedPayload{
		SessionID: "sess_1",
		PlaybackSessionContext: PlaybackSessionContext{
			TitleName: "The Matrix",
			Viewer:    PlaybackViewerContext{ID: 7, Username: "alice"},
			Network:   PlaybackNetworkContext{RemoteIP: "203.0.113.10", Location: "wan"},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	redacted := s.Redact(doc, RedactOptions{StripPII: true})
	if _, ok := lookupDotPath(redacted, "viewer.username"); ok {
		t.Error("redacted viewer.username should be stripped")
	}
	if _, ok := lookupDotPath(redacted, "network.remoteIP"); ok {
		t.Error("redacted network.remoteIP should be stripped")
	}
	if v, ok := lookupDotPath(redacted, "titleName"); !ok || v != "The Matrix" {
		t.Errorf("redacted titleName = %v (ok=%v), want %q", v, ok, "The Matrix")
	}

	// Also check PII on admission.denied and livetv.channel.tuned.
	for _, typ := range []string{EventPlaybackAdmissionDenied, EventLiveTVChannelTuned} {
		sc, ok := reg.Get(typ)
		if !ok {
			t.Fatalf("missing %s", typ)
		}
		found := false
		for _, f := range sc.Fields {
			if f.Name == "username" {
				found = true
				if !f.PII {
					t.Errorf("%s: username must be marked PII", typ)
				}
			}
		}
		if !found {
			t.Errorf("%s: missing username field", typ)
		}
	}
}
