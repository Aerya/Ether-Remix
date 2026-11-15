package specs

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// Silo's pinned upstream spec predates Ether's additive live playback policy.
// Keep the extension checked in while retaining verification of upstream bytes.
//
//go:embed silo-live-policy.json
var siloLivePolicy json.RawMessage

// Extend adds Ether's protocol fields after the upstream checksum is verified.
func Extend(protocol string, data []byte) ([]byte, error) {
	if protocol != "silo" {
		return data, nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var components, schemas, plan, properties map[string]json.RawMessage
	if err := json.Unmarshal(doc["components"], &components); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(components["schemas"], &schemas); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(schemas["PlaybackPlan"], &plan); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(plan["properties"], &properties); err != nil {
		return nil, err
	}
	if properties == nil {
		return nil, fmt.Errorf("silo PlaybackPlan has no properties")
	}
	properties["livePolicy"] = siloLivePolicy
	var err error
	if plan["properties"], err = json.Marshal(properties); err != nil {
		return nil, err
	}
	if schemas["PlaybackPlan"], err = json.Marshal(plan); err != nil {
		return nil, err
	}
	if components["schemas"], err = json.Marshal(schemas); err != nil {
		return nil, err
	}
	if doc["components"], err = json.Marshal(components); err != nil {
		return nil, err
	}
	return json.Marshal(doc)
}
