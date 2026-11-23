package events

const (
	EventSystemServerStarted       = "system.server.started"
	EventSystemServerStopping      = "system.server.stopping"
	EventSystemServerCrashed       = "system.server.crashed"
	EventSystemHealthChanged       = "system.health.changed"
	EventSystemReachabilityChanged = "system.reachability.changed"
	EventSystemUpdateAvailable     = "system.update.available"
	EventSystemUpdateInstalled     = "system.update.installed"
	EventSystemUpdateRolledBack    = "system.update.rolled_back"
	EventSystemBackupCompleted     = "system.backup.completed"
	EventSystemBackupFailed        = "system.backup.failed"
	EventSystemDiskLow             = "system.disk.low"
)

type SystemServerStartedPayload struct {
	Version   string `json:"version" event:"label=Version;desc=Ether server version;example=0.3.17"`
	Mode      string `json:"mode" event:"label=Mode;desc=Runtime deployment mode;enum=standalone|cluster;example=standalone"`
	NodeID    string `json:"nodeId" event:"label=Node ID;desc=Local node identifier;example=node-a"`
	StartupMS int64  `json:"startupMs" event:"label=Startup Time;desc=Milliseconds from server initialization to ready;duration_ms;unit=ms;example=1420"`
}

type SystemServerStoppingPayload struct {
	Version        string `json:"version" event:"label=Version;desc=Ether server version;example=0.3.17"`
	Reason         string `json:"reason" event:"label=Reason;desc=Shutdown reason;example=signal"`
	ActiveSessions int    `json:"activeSessions" event:"label=Active Sessions;desc=Active playback sessions at shutdown;example=1"`
}

type SystemServerCrashedPayload struct {
	Version    string `json:"version" event:"label=Version;desc=Ether server version;example=0.3.17"`
	ExitReason string `json:"exitReason" event:"label=Exit Reason;desc=Unexpected exit status or signal reported by the watcher;example=exit status 2"`
	Watcher    string `json:"watcher,omitempty" event:"label=Watcher;desc=Supervisor or watcher process that observed the crash;example=ether-watch"`
}

type SystemHealthChangedPayload struct {
	FromStatus string `json:"fromStatus" event:"label=Previous Status;desc=Previous health status of the component;enum=ok|degraded|down|unknown;example=ok"`
	ToStatus   string `json:"toStatus" event:"label=New Status;desc=Current health status of the component;enum=ok|degraded|down|unknown;example=degraded"`
	Component  string `json:"component" event:"label=Component;desc=Subsystem whose health changed;example=database"`
	Summary    string `json:"summary,omitempty" event:"label=Summary;desc=Human-readable explanation of the health change;example=database connection lost"`
}

type SystemReachabilityChangedPayload struct {
	Scope      string `json:"scope" event:"label=Scope;desc=Reachability probe scope;enum=internal|external|remote_access;example=remote_access"`
	FromStatus string `json:"fromStatus" event:"label=Previous Status;desc=Previous reachability status;enum=reachable|unreachable|unknown;example=reachable"`
	ToStatus   string `json:"toStatus" event:"label=New Status;desc=Current reachability status;enum=reachable|unreachable|unknown;example=unreachable"`
	URL        string `json:"url,omitempty" event:"label=URL;desc=Checked endpoint address or URL;example=https://1-2-3-4.abc.plex.direct:32400"`
	StatusCode int    `json:"statusCode,omitempty" event:"label=Status Code;desc=HTTP status code returned by the probe if applicable;example=502"`
	LatencyMS  int64  `json:"latencyMs,omitempty" event:"label=Latency;desc=Probe round-trip latency in milliseconds;duration_ms;unit=ms;example=240"`
	Error      string `json:"error,omitempty" event:"label=Error;desc=Probe failure reason when unreachable;example=connection timed out"`
}

type SystemUpdateAvailablePayload struct {
	CurrentVersion string `json:"currentVersion" event:"label=Current Version;desc=Currently running Ether version;example=0.3.16"`
	NewVersion     string `json:"newVersion" event:"label=New Version;desc=Newly available Ether version;example=0.3.17"`
	Channel        string `json:"channel" event:"label=Channel;desc=Release channel;example=stable"`
	Critical       bool   `json:"critical" event:"label=Critical;desc=True if the update fixes a critical issue;example=false"`
}

type SystemUpdateInstalledPayload struct {
	FromVersion string `json:"fromVersion" event:"label=From Version;desc=Version before upgrade;example=0.3.16"`
	ToVersion   string `json:"toVersion" event:"label=To Version;desc=Version after upgrade;example=0.3.17"`
	Channel     string `json:"channel" event:"label=Channel;desc=Release channel;example=stable"`
	Auto        bool   `json:"auto" event:"label=Automatic;desc=True when installed automatically;example=true"`
}

type SystemUpdateRolledBackPayload struct {
	FailedVersion   string `json:"failedVersion" event:"label=Failed Version;desc=Version that failed verification and was rolled back;example=0.3.17"`
	RestoredVersion string `json:"restoredVersion" event:"label=Restored Version;desc=Previous working version restored;example=0.3.16"`
	Reason          string `json:"reason" event:"label=Reason;desc=Reason the upgrade was rolled back;example=health check failed after restart"`
}

type SystemBackupCompletedPayload struct {
	Kind        string `json:"kind" event:"label=Kind;desc=Whether the backup was scheduled or manually triggered;enum=scheduled|manual;example=scheduled"`
	SizeBytes   int64  `json:"sizeBytes" event:"label=Size;desc=Total archive size in bytes;unit=bytes;example=48234496"`
	DurationMS  int64  `json:"durationMs" event:"label=Duration;desc=Time spent producing the backup in milliseconds;duration_ms;unit=ms;example=3120"`
	Destination string `json:"destination" event:"label=Destination;desc=Backup archive path or destination;example=/var/lib/ether/backups/ether-2026-11-19.tar.gz"`
	Encrypted   bool   `json:"encrypted" event:"label=Encrypted;desc=Whether backup secrets are encrypted;example=true"`
}

type SystemBackupFailedPayload struct {
	Kind                string `json:"kind" event:"label=Kind;desc=Whether the backup was scheduled or manually triggered;enum=scheduled|manual;example=scheduled"`
	Error               string `json:"error" event:"label=Error;desc=Reason the backup failed;example=no space left on device"`
	ConsecutiveFailures int    `json:"consecutiveFailures" event:"label=Consecutive Failures;desc=Number of consecutive failed backups;example=2"`
}

type SystemDiskLowPayload struct {
	Volume      string  `json:"volume" event:"label=Volume;desc=Logical Ether storage volume name;example=data"`
	Path        string  `json:"path" event:"label=Path;desc=Filesystem path checked;example=/var/lib/ether"`
	FreeBytes   int64   `json:"freeBytes" event:"label=Free Bytes;desc=Remaining free space in bytes;unit=bytes;example=536870912"`
	TotalBytes  int64   `json:"totalBytes" event:"label=Total Bytes;desc=Total filesystem capacity in bytes;unit=bytes;example=107374182400"`
	PercentUsed float64 `json:"percentUsed" event:"label=Percent Used;desc=Percentage of filesystem capacity currently used;unit=%;example=99.5"`
}

func init() {
	registerBuiltin(RegisterSystemSchemas)
}

// RegisterSystemSchemas registers all system domain event schemas on r.
func RegisterSystemSchemas(r *Registry) {
	mustRegisterBuiltin[SystemServerStartedPayload](r, EventSchema{
		Type:        EventSystemServerStarted,
		Label:       "Server Started",
		Description: "Ether completed startup and began serving traffic.",
		LegacyNames: []string{"server.started"},
		Sample: SystemServerStartedPayload{
			Version:   "0.3.17",
			Mode:      "standalone",
			NodeID:    "node-a",
			StartupMS: 1420,
		},
	})

	mustRegisterBuiltin[SystemServerStoppingPayload](r, EventSchema{
		Type:        EventSystemServerStopping,
		Label:       "Server Stopping",
		Description: "Ether began a graceful shutdown.",
		LegacyNames: []string{"server.stopping"},
		Sample: SystemServerStoppingPayload{
			Version:        "0.3.17",
			Reason:         "signal",
			ActiveSessions: 1,
		},
	})

	mustRegisterBuiltin[SystemServerCrashedPayload](r, EventSchema{
		Type:        EventSystemServerCrashed,
		Label:       "Server Crashed",
		Description: "The Ether server process exited unexpectedly.",
		Critical:    true,
		LegacyNames: []string{"server.crash"},
		Sample: SystemServerCrashedPayload{
			Version:    "0.3.17",
			ExitReason: "exit status 2",
			Watcher:    "ether-watch",
		},
	})

	mustRegisterBuiltin[SystemHealthChangedPayload](r, EventSchema{
		Type:        EventSystemHealthChanged,
		Label:       "System Health Changed",
		Description: "A core server subsystem transitioned between healthy, degraded, and down states.",
		Critical:    true,
		LegacyNames: []string{"server.health_changed"},
		Sample: SystemHealthChangedPayload{
			FromStatus: "ok",
			ToStatus:   "degraded",
			Component:  "database",
			Summary:    "database connection lost",
		},
	})

	mustRegisterBuiltin[SystemReachabilityChangedPayload](r, EventSchema{
		Type:        EventSystemReachabilityChanged,
		Label:       "Reachability Changed",
		Description: "Remote access or external server reachability changed state.",
		Critical:    true,
		Sample: SystemReachabilityChangedPayload{
			Scope:      "remote_access",
			FromStatus: "reachable",
			ToStatus:   "unreachable",
			URL:        "https://1-2-3-4.abc.plex.direct:32400",
			Error:      "connection timed out",
		},
	})

	mustRegisterBuiltin[SystemUpdateAvailablePayload](r, EventSchema{
		Type:        EventSystemUpdateAvailable,
		Label:       "Update Available",
		Description: "A newer Ether release is available on the configured channel.",
		LegacyNames: []string{"update.available"},
		Sample: SystemUpdateAvailablePayload{
			CurrentVersion: "0.3.16",
			NewVersion:     "0.3.17",
			Channel:        "stable",
			Critical:       false,
		},
	})

	mustRegisterBuiltin[SystemUpdateInstalledPayload](r, EventSchema{
		Type:        EventSystemUpdateInstalled,
		Label:       "Update Installed",
		Description: "Ether upgraded to a new release.",
		LegacyNames: []string{"update.installed"},
		Sample: SystemUpdateInstalledPayload{
			FromVersion: "0.3.16",
			ToVersion:   "0.3.17",
			Channel:     "stable",
			Auto:        true,
		},
	})

	mustRegisterBuiltin[SystemUpdateRolledBackPayload](r, EventSchema{
		Type:        EventSystemUpdateRolledBack,
		Label:       "Update Rolled Back",
		Description: "An Ether update failed startup checks and was rolled back.",
		Critical:    true,
		LegacyNames: []string{"update.rolledback"},
		Sample: SystemUpdateRolledBackPayload{
			FailedVersion:   "0.3.17",
			RestoredVersion: "0.3.16",
			Reason:          "health check failed after restart",
		},
	})

	mustRegisterBuiltin[SystemBackupCompletedPayload](r, EventSchema{
		Type:        EventSystemBackupCompleted,
		Label:       "Backup Completed",
		Description: "A scheduled or manual Ether backup completed successfully.",
		Sample: SystemBackupCompletedPayload{
			Kind:        "scheduled",
			SizeBytes:   48234496,
			DurationMS:  3120,
			Destination: "/var/lib/ether/backups/ether-2026-11-19.tar.gz",
			Encrypted:   true,
		},
	})

	mustRegisterBuiltin[SystemBackupFailedPayload](r, EventSchema{
		Type:        EventSystemBackupFailed,
		Label:       "Backup Failed",
		Description: "A scheduled or manual Ether backup failed.",
		Critical:    true,
		LegacyNames: []string{"backup.failed"},
		Sample: SystemBackupFailedPayload{
			Kind:                "scheduled",
			Error:               "no space left on device",
			ConsecutiveFailures: 2,
		},
	})

	mustRegisterBuiltin[SystemDiskLowPayload](r, EventSchema{
		Type:        EventSystemDiskLow,
		Label:       "Disk Space Low",
		Description: "Available disk space on an Ether data or cache volume dropped below the safety threshold.",
		Critical:    true,
		Sample: SystemDiskLowPayload{
			Volume:      "data",
			Path:        "/var/lib/ether",
			FreeBytes:   536870912,
			TotalBytes:  107374182400,
			PercentUsed: 99.5,
		},
	})
}
