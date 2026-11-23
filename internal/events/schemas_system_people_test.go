package events

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestSystemPeopleBuiltinSchemasComplete(t *testing.T) {
	reg := NewBuiltinRegistry()

	// Calling RegisterBuiltins again must be idempotent.
	RegisterBuiltins(reg)

	expected := []struct {
		typ      string
		domain   string
		critical bool
		legacy   []string
		pii      []string
	}{
		// Library (11)
		{EventLibraryTitleAdded, "library", false, nil, nil},
		{EventLibraryBatchAdded, "library", false, nil, nil},
		{EventLibraryTitleAvailable, "library", false, []string{"title.available"}, nil},
		{EventLibraryTitleUnavailable, "library", false, nil, nil},
		{EventLibraryEpisodeAired, "library", false, []string{"viewer.episode"}, nil},
		{EventLibrarySyncStarted, "library", false, nil, nil},
		{EventLibrarySyncCompleted, "library", false, nil, nil},
		{EventLibrarySyncFailed, "library", false, nil, nil},
		{EventLibraryCollectionRebuilt, "library", false, []string{"collection.rebuilt"}, nil},
		{EventLibraryWatchlistSynced, "library", false, nil, nil},
		{EventLibraryMetadataFailed, "library", false, nil, nil},

		// Viewer (9)
		{EventViewerAccountJoined, "viewer", false, []string{"viewer.added", "user.joined"}, []string{"username"}},
		{EventViewerAccountDeactivated, "viewer", false, nil, []string{"username"}},
		{EventViewerAccountMerged, "viewer", false, nil, nil},
		{EventViewerAccessChanged, "viewer", false, []string{"viewer.access_changed"}, []string{"username"}},
		{EventViewerSafetyUpdated, "viewer", false, nil, []string{"username"}},
		{EventViewerInviteCreated, "viewer", false, nil, nil},
		{EventViewerInviteAccepted, "viewer", false, nil, []string{"username"}},
		{EventViewerWatchScrobbled, "viewer", false, nil, []string{"username"}},
		{EventViewerRatingSubmitted, "viewer", false, nil, []string{"username"}},

		// Auth (9)
		{EventAuthLoginSucceeded, "auth", false, nil, []string{"username", "remoteIP"}},
		{EventAuthLoginFailed, "auth", false, nil, []string{"username", "remoteIP"}},
		{EventAuthGrantRevoked, "auth", false, nil, nil},
		{EventAuthDeviceRegistered, "auth", false, nil, []string{"username", "remoteIP"}},
		{EventAuthDeviceQuickConnectApproved, "auth", false, nil, nil},
		{EventAuthSessionNewIP, "auth", false, nil, []string{"username", "remoteIP", "previousIPs"}},
		{EventAuthTokenExpired, "auth", true, nil, []string{"username"}},
		{EventAuthRateLimited, "auth", true, nil, []string{"remoteIP"}},
		{EventAuthCertificateRenewed, "auth", false, []string{"cert.renewed"}, nil},

		// Plugin (9)
		{EventPluginPackageInstalled, "plugin", false, nil, nil},
		{EventPluginPackageUpdated, "plugin", false, nil, nil},
		{EventPluginPackageRemoved, "plugin", false, nil, nil},
		{EventPluginStateEnabled, "plugin", false, nil, nil},
		{EventPluginStateDisabled, "plugin", false, nil, nil},
		{EventPluginHealthDegraded, "plugin", true, nil, nil},
		{EventPluginHealthRecovered, "plugin", false, nil, nil},
		{EventPluginQuotaExceeded, "plugin", false, nil, nil},
		{EventPluginJobFailed, "plugin", false, nil, nil},

		// Cluster (6)
		{EventClusterNodeJoined, "cluster", false, nil, nil},
		{EventClusterNodeLost, "cluster", true, nil, nil},
		{EventClusterNodeDraining, "cluster", false, nil, nil},
		{EventClusterLeaderElected, "cluster", false, nil, nil},
		{EventClusterDatabaseUnavailable, "cluster", true, nil, nil},
		{EventClusterDatabaseRecovered, "cluster", false, nil, nil},

		// System (11)
		{EventSystemServerStarted, "system", false, []string{"server.started"}, nil},
		{EventSystemServerStopping, "system", false, []string{"server.stopping"}, nil},
		{EventSystemServerCrashed, "system", true, []string{"server.crash"}, nil},
		{EventSystemHealthChanged, "system", true, []string{"server.health_changed"}, nil},
		{EventSystemReachabilityChanged, "system", true, nil, nil},
		{EventSystemUpdateAvailable, "system", false, []string{"update.available"}, nil},
		{EventSystemUpdateInstalled, "system", false, []string{"update.installed"}, nil},
		{EventSystemUpdateRolledBack, "system", true, []string{"update.rolledback"}, nil},
		{EventSystemBackupCompleted, "system", false, nil, nil},
		{EventSystemBackupFailed, "system", true, []string{"backup.failed"}, nil},
		{EventSystemDiskLow, "system", true, nil, nil},

		// Flows (4)
		{EventFlowsRunFailed, "flows", false, nil, nil},
		{EventFlowsFlowAutoDisabled, "flows", true, nil, nil},
		{EventFlowsBusDropped, "flows", false, nil, nil},
		{EventFlowsDigestScheduled, "flows", false, []string{"newsletter.weekly"}, nil},
	}

	for _, tc := range expected {
		t.Run(tc.typ, func(t *testing.T) {
			s, ok := reg.Get(tc.typ)
			if !ok {
				t.Fatalf("schema %q not registered", tc.typ)
			}
			if err := s.Validate(); err != nil {
				t.Fatalf("Validate() failed: %v", err)
			}
			if s.Domain != tc.domain {
				t.Errorf("Domain = %q, want %q", s.Domain, tc.domain)
			}
			if s.Owner != OwnerBuiltin {
				t.Errorf("Owner = %q, want %q", s.Owner, OwnerBuiltin)
			}
			if s.Critical != tc.critical {
				t.Errorf("Critical = %v, want %v", s.Critical, tc.critical)
			}
			if len(s.Fields) == 0 {
				t.Errorf("expected fields for %q", tc.typ)
			}
			if s.Sample == nil {
				t.Fatalf("expected non-nil sample for %q", tc.typ)
			}

			raw, err := json.Marshal(s.Sample)
			if err != nil {
				t.Fatalf("marshal sample: %v", err)
			}
			if err := reg.ValidatePayload(tc.typ, raw); err != nil {
				t.Fatalf("ValidatePayload(sample) failed: %v (raw=%s)", err, string(raw))
			}

			// Verify every legacy alias maps back to this schema.
			for _, alias := range tc.legacy {
				got, ok := reg.GetByLegacy(alias)
				if !ok {
					t.Errorf("legacy alias %q not mapped", alias)
					continue
				}
				if got.Type != tc.typ {
					t.Errorf("legacy alias %q mapped to %q, want %q", alias, got.Type, tc.typ)
				}
			}

			// Verify PII fields are marked and stripped by Redact.
			var gotPII []string
			for _, f := range s.Fields {
				if f.PII {
					gotPII = append(gotPII, f.Name)
				}
			}
			for _, want := range tc.pii {
				if !slices.Contains(gotPII, want) {
					t.Errorf("field %q missing PII tag (got PII fields: %v)", want, gotPII)
				}
			}
			if len(tc.pii) > 0 {
				var m map[string]any
				if err := json.Unmarshal(raw, &m); err != nil {
					t.Fatalf("unmarshal sample map: %v", err)
				}
				redacted := s.Redact(m, RedactOptions{StripPII: true})
				for _, piiField := range tc.pii {
					if _, present := redacted[piiField]; present {
						t.Errorf("PII field %q remained after Redact(StripPII: true)", piiField)
					}
				}
			}
		})
	}
}

func TestSystemPeopleSchemasPublishThroughBus(t *testing.T) {
	reg := NewBuiltinRegistry()
	bus, rec := NewTestBus(t, BusOptions{Registry: reg, NodeID: "test-node"})

	// Publish every registered schema's sample payload through the bus to confirm
	// end-to-end envelope encoding and strict registry validation.
	domains := []string{"library", "viewer", "auth", "plugin", "cluster", "system", "flows"}
	count := 0
	for _, d := range domains {
		for _, s := range reg.ListByDomain(d) {
			if err := bus.PublishTyped(s.Type, s.Sample, WithSource("test")); err != nil {
				t.Fatalf("PublishTyped(%s): %v", s.Type, err)
			}
			count++
		}
	}
	if count != 59 {
		t.Fatalf("published %d domain events, want 59", count)
	}
	for _, d := range domains {
		for _, s := range reg.ListByDomain(d) {
			ev := rec.WaitFor(t, s.Type, time.Second)
			if err := reg.ValidatePayload(ev.Type, ev.Payload); err != nil {
				t.Fatalf("ValidatePayload(%s): %v", ev.Type, err)
			}
		}
	}
}
