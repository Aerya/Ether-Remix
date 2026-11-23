package events

// Canonical live-event alert type names (A7). LegacyNames keep the short
// two-part names ("liveevent.started", "liveevent.close", "liveevent.final")
// resolvable in Flows rules and subscriptions.
const (
	EventLiveEventStarted = "liveevent.event.started"
	EventLiveEventClose   = "liveevent.event.close"
	EventLiveEventFinal   = "liveevent.event.final"
)

// LiveEventAlertPayload is the generic payload emitted for live event alerts
// involving a viewer's favourited entity.
type LiveEventAlertPayload struct {
	AlertID   string `json:"alertId" event:"label=Alert ID;desc=Stable deduplication identifier for this alert;example=started:plugin:feed:101:plugin:feed:team:a"`
	Kind      string `json:"kind" event:"label=Alert kind;desc=Transition that triggered the alert;enum=started|close|final;example=started"`
	EventID   string `json:"eventId" event:"label=Event ID;desc=Namespaced live event identifier;example=plugin:feed:101"`
	Feed      string `json:"feed,omitempty" event:"label=Feed;desc=Live feed identifier;example=main"`
	Category  string `json:"category,omitempty" event:"label=Category;desc=Generic event category;example=match"`
	League    string `json:"league,omitempty" event:"label=League;desc=Competition or circuit name;example=Premier Division"`
	EntityKey string `json:"entityKey" event:"label=Entity key;desc=Favourited entity key that matched;example=plugin:feed:team:home"`
	ViewerID  int64  `json:"viewerId" event:"label=Viewer ID;desc=Viewer whose favourite matched;example=42"`
	Title     string `json:"title" event:"label=Title;desc=Alert headline;example=North at South"`
	Body      string `json:"body" event:"label=Body;desc=Alert summary text;example=Live now"`
	State     string `json:"state,omitempty" event:"label=Event state;desc=Current live event state;enum=pre|in|post|delayed|postponed|canceled;example=in"`
	Close     bool   `json:"close,omitempty" event:"label=Close contest;desc=Whether the plugin marked the event as close;example=true"`
}

// RegisterLiveEventSchemas registers built-in live-event alert schemas.
func RegisterLiveEventSchemas(r *Registry) {
	mustRegisterBuiltin[LiveEventAlertPayload](r, EventSchema{
		Type:        EventLiveEventStarted,
		Version:     1,
		Domain:      "liveevent",
		Label:       "Live event started",
		Description: "Emitted when a live event involving a favourited entity begins.",
		Owner:       OwnerBuiltin,
		LegacyNames: []string{"liveevent.started"},
	})
	mustRegisterBuiltin[LiveEventAlertPayload](r, EventSchema{
		Type:        EventLiveEventClose,
		Version:     1,
		Domain:      "liveevent",
		Label:       "Live event close",
		Description: "Emitted when a live event involving a favourited entity enters a close finish.",
		Owner:       OwnerBuiltin,
		LegacyNames: []string{"liveevent.close"},
	})
	mustRegisterBuiltin[LiveEventAlertPayload](r, EventSchema{
		Type:        EventLiveEventFinal,
		Version:     1,
		Domain:      "liveevent",
		Label:       "Live event final",
		Description: "Emitted when a live event involving a favourited entity finishes.",
		Owner:       OwnerBuiltin,
		LegacyNames: []string{"liveevent.final"},
	})
}

func init() {
	registerBuiltin(RegisterLiveEventSchemas)
}
