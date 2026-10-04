package signing

import (
	"encoding/json"
	"fmt"
)

// SettingsKey is the workspace.settings jsonb key that arms the signature
// authorization feature. Default OFF: the feature grants high-risk
// authorizations (offensive drills, dangerous agent/skill creation), so it
// must not exist on any install until the user explicitly acknowledges the
// risk notice in Settings. The value must be JSON boolean true — any other
// shape (string "true", 1, null) reads as disabled.
const SettingsKey = "signature_authorization_enabled"

// EnabledFromSettings reads the arm switch from a workspace.settings JSONB
// payload. Missing key / non-object / non-boolean-true all mean disabled.
func EnabledFromSettings(settings []byte) bool {
	if len(settings) == 0 {
		return false
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(settings, &parsed); err != nil {
		return false
	}
	raw, ok := parsed[SettingsKey]
	if !ok {
		return false
	}
	var enabled bool
	if err := json.Unmarshal(raw, &enabled); err != nil {
		return false
	}
	return enabled
}

// WithEnabled returns a merged settings payload carrying the arm switch,
// preserving every unknown key (append-only settings law — the
// default_runtime_id convention). Marshal errors are impossible for
// map[string]any inputs but are surfaced for honesty.
func WithEnabled(settings map[string]any, enabled bool) (map[string]any, error) {
	if settings == nil {
		settings = map[string]any{}
	}
	out := make(map[string]any, len(settings)+1)
	for k, v := range settings {
		out[k] = v
	}
	out[SettingsKey] = enabled
	if _, err := json.Marshal(out); err != nil {
		return nil, fmt.Errorf("signing: merge settings: %w", err)
	}
	return out, nil
}
