package events

const (
	EventPluginPackageInstalled = "plugin.package.installed"
	EventPluginPackageUpdated   = "plugin.package.updated"
	EventPluginPackageRemoved   = "plugin.package.removed"
	EventPluginStateEnabled     = "plugin.state.enabled"
	EventPluginStateDisabled    = "plugin.state.disabled"
	EventPluginHealthDegraded   = "plugin.health.degraded"
	EventPluginHealthRecovered  = "plugin.health.recovered"
	EventPluginQuotaExceeded    = "plugin.quota.exceeded"
	EventPluginJobFailed        = "plugin.job.failed"
)

type PluginPackageInstalledPayload struct {
	PluginID    string `json:"pluginId" event:"label=Plugin ID;desc=Unique plugin identifier;example=com.example.trakt-helper"`
	Name        string `json:"name" event:"label=Name;desc=Display name of the plugin;example=Trakt Helper"`
	Version     string `json:"version" event:"label=Version;desc=Installed plugin version;example=1.2.0"`
	Kind        string `json:"kind" event:"label=Kind;desc=Plugin capability category;example=integration"`
	RuntimeKind string `json:"runtimeKind" event:"label=Runtime;desc=Execution runtime for the plugin;example=wasm"`
	Verified    bool   `json:"verified" event:"label=Verified;desc=Whether the package signature was verified;example=true"`
}

type PluginPackageUpdatedPayload struct {
	PluginID    string `json:"pluginId" event:"label=Plugin ID;desc=Unique plugin identifier;example=com.example.trakt-helper"`
	Name        string `json:"name" event:"label=Name;desc=Display name of the plugin;example=Trakt Helper"`
	FromVersion string `json:"fromVersion" event:"label=From Version;desc=Previously installed version;example=1.2.0"`
	ToVersion   string `json:"toVersion" event:"label=To Version;desc=Newly installed version;example=1.3.0"`
	Auto        bool   `json:"auto" event:"label=Automatic;desc=True when applied by auto-update;example=true"`
}

type PluginPackageRemovedPayload struct {
	PluginID string `json:"pluginId" event:"label=Plugin ID;desc=Unique plugin identifier;example=com.example.trakt-helper"`
	Name     string `json:"name" event:"label=Name;desc=Display name of the removed plugin;example=Trakt Helper"`
	Version  string `json:"version" event:"label=Version;desc=Version that was removed;example=1.3.0"`
}

type PluginStateEnabledPayload struct {
	PluginID string `json:"pluginId" event:"label=Plugin ID;desc=Unique plugin identifier;example=com.example.trakt-helper"`
	Name     string `json:"name" event:"label=Name;desc=Display name of the plugin;example=Trakt Helper"`
	Shadow   bool   `json:"shadow" event:"label=Shadow Mode;desc=Whether the plugin is running in shadow mode;example=false"`
}

type PluginStateDisabledPayload struct {
	PluginID string `json:"pluginId" event:"label=Plugin ID;desc=Unique plugin identifier;example=com.example.trakt-helper"`
	Name     string `json:"name" event:"label=Name;desc=Display name of the plugin;example=Trakt Helper"`
	Reason   string `json:"reason" event:"label=Reason;desc=Reason the plugin was disabled;example=owner"`
}

type PluginHealthDegradedPayload struct {
	PluginID            string `json:"pluginId" event:"label=Plugin ID;desc=Unique plugin identifier;example=com.example.trakt-helper"`
	Name                string `json:"name" event:"label=Name;desc=Display name of the plugin;example=Trakt Helper"`
	Status              string `json:"status" event:"label=Status;desc=Degraded health status reported for the plugin;example=error"`
	Restarts            int64  `json:"restarts" event:"label=Restarts;desc=Total runtime restart count;example=3"`
	ConsecutiveFailures int    `json:"consecutiveFailures" event:"label=Consecutive Failures;desc=Recent consecutive failure count;example=2"`
	LastError           string `json:"lastError" event:"label=Last Error;desc=Most recent failure message;example=timeout waiting for rpc response"`
}

type PluginHealthRecoveredPayload struct {
	PluginID   string `json:"pluginId" event:"label=Plugin ID;desc=Unique plugin identifier;example=com.example.trakt-helper"`
	Name       string `json:"name" event:"label=Name;desc=Display name of the plugin;example=Trakt Helper"`
	DowntimeMS int64  `json:"downtimeMs" event:"label=Downtime;desc=Duration the plugin remained degraded in milliseconds;duration_ms;unit=ms;example=45000"`
}

type PluginQuotaExceededPayload struct {
	PluginID  string `json:"pluginId" event:"label=Plugin ID;desc=Unique plugin identifier;example=com.example.trakt-helper"`
	Name      string `json:"name" event:"label=Name;desc=Display name of the plugin;example=Trakt Helper"`
	QuotaKind string `json:"quotaKind" event:"label=Quota Kind;desc=Resource quota that was exceeded;example=events_outbox"`
	Used      int64  `json:"used" event:"label=Used;desc=Current usage count or bytes;example=512"`
	Limit     int64  `json:"limit" event:"label=Limit;desc=Configured quota limit;example=512"`
	Drops     int64  `json:"drops" event:"label=Drops;desc=Number of items or events dropped;example=4"`
}

type PluginJobFailedPayload struct {
	PluginID   string `json:"pluginId" event:"label=Plugin ID;desc=Unique plugin identifier;example=com.example.trakt-helper"`
	Name       string `json:"name" event:"label=Name;desc=Display name of the plugin;example=Trakt Helper"`
	Job        string `json:"job" event:"label=Job;desc=Name or call identifier of the failed plugin job;example=sync_lists"`
	Kind       string `json:"kind" event:"label=Job Kind;desc=Category of plugin background job;enum=scheduled|action|expand|resolver;example=scheduled"`
	Error      string `json:"error" event:"label=Error;desc=Failure message returned by the job;example=status 503: upstream unavailable"`
	DurationMS int64  `json:"durationMs" event:"label=Duration;desc=Execution duration before failure in milliseconds;duration_ms;unit=ms;example=1240"`
}

func init() {
	registerBuiltin(RegisterPluginSchemas)
}

// RegisterPluginSchemas registers all plugin domain event schemas on r.
func RegisterPluginSchemas(r *Registry) {
	mustRegisterBuiltin[PluginPackageInstalledPayload](r, EventSchema{
		Type:        EventPluginPackageInstalled,
		Label:       "Plugin Installed",
		Description: "A new plugin or add-on package was installed.",
		Sample: PluginPackageInstalledPayload{
			PluginID:    "com.example.trakt-helper",
			Name:        "Trakt Helper",
			Version:     "1.2.0",
			Kind:        "integration",
			RuntimeKind: "wasm",
			Verified:    true,
		},
	})

	mustRegisterBuiltin[PluginPackageUpdatedPayload](r, EventSchema{
		Type:        EventPluginPackageUpdated,
		Label:       "Plugin Updated",
		Description: "An installed plugin package was upgraded to a new version.",
		Sample: PluginPackageUpdatedPayload{
			PluginID:    "com.example.trakt-helper",
			Name:        "Trakt Helper",
			FromVersion: "1.2.0",
			ToVersion:   "1.3.0",
			Auto:        true,
		},
	})

	mustRegisterBuiltin[PluginPackageRemovedPayload](r, EventSchema{
		Type:        EventPluginPackageRemoved,
		Label:       "Plugin Removed",
		Description: "A plugin package was uninstalled from Ether.",
		Sample: PluginPackageRemovedPayload{
			PluginID: "com.example.trakt-helper",
			Name:     "Trakt Helper",
			Version:  "1.3.0",
		},
	})

	mustRegisterBuiltin[PluginStateEnabledPayload](r, EventSchema{
		Type:        EventPluginStateEnabled,
		Label:       "Plugin Enabled",
		Description: "A plugin was enabled.",
		Sample: PluginStateEnabledPayload{
			PluginID: "com.example.trakt-helper",
			Name:     "Trakt Helper",
			Shadow:   false,
		},
	})

	mustRegisterBuiltin[PluginStateDisabledPayload](r, EventSchema{
		Type:        EventPluginStateDisabled,
		Label:       "Plugin Disabled",
		Description: "A plugin was disabled by the owner, policy, or failure quarantine.",
		Sample: PluginStateDisabledPayload{
			PluginID: "com.example.trakt-helper",
			Name:     "Trakt Helper",
			Reason:   "owner",
		},
	})

	mustRegisterBuiltin[PluginHealthDegradedPayload](r, EventSchema{
		Type:        EventPluginHealthDegraded,
		Label:       "Plugin Health Degraded",
		Description: "A plugin entered an unhealthy, slow, or error state.",
		Critical:    true,
		Sample: PluginHealthDegradedPayload{
			PluginID:            "com.example.trakt-helper",
			Name:                "Trakt Helper",
			Status:              "error",
			Restarts:            3,
			ConsecutiveFailures: 2,
			LastError:           "timeout waiting for rpc response",
		},
	})

	mustRegisterBuiltin[PluginHealthRecoveredPayload](r, EventSchema{
		Type:        EventPluginHealthRecovered,
		Label:       "Plugin Health Recovered",
		Description: "A previously degraded plugin returned to healthy operation.",
		Sample: PluginHealthRecoveredPayload{
			PluginID:   "com.example.trakt-helper",
			Name:       "Trakt Helper",
			DowntimeMS: 45000,
		},
	})

	mustRegisterBuiltin[PluginQuotaExceededPayload](r, EventSchema{
		Type:        EventPluginQuotaExceeded,
		Label:       "Plugin Quota Exceeded",
		Description: "A plugin hit a storage, badge, or event delivery quota.",
		Sample: PluginQuotaExceededPayload{
			PluginID:  "com.example.trakt-helper",
			Name:      "Trakt Helper",
			QuotaKind: "events_outbox",
			Used:      512,
			Limit:     512,
			Drops:     4,
		},
	})

	mustRegisterBuiltin[PluginJobFailedPayload](r, EventSchema{
		Type:        EventPluginJobFailed,
		Label:       "Plugin Job Failed",
		Description: "A scheduled call or background action job executed by a plugin failed.",
		Sample: PluginJobFailedPayload{
			PluginID:   "com.example.trakt-helper",
			Name:       "Trakt Helper",
			Job:        "sync_lists",
			Kind:       "scheduled",
			Error:      "status 503: upstream unavailable",
			DurationMS: 1240,
		},
	})
}
