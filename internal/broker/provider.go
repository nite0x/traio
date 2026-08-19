package broker

import (
	"context"
	"fmt"
	"strings"
)

// AuthMode identifies how a connection authenticates with its provider.
// Providers list every mode they support in ProviderDefinition.AuthModes.
type AuthMode string

const (
	AuthModeInteractive AuthMode = "interactive"
	AuthModeOAuth       AuthMode = "oauth"
	AuthModeAPIKey      AuthMode = "api_key"
	AuthModeGateway     AuthMode = "gateway"
)

func (m AuthMode) valid() bool {
	switch m {
	case AuthModeInteractive, AuthModeOAuth, AuthModeAPIKey, AuthModeGateway:
		return true
	default:
		return false
	}
}

// Capability identifies one independently consumable provider feature.
// Values are flags so a provider's features can be represented by CapabilitySet.
type Capability uint64

const (
	CapabilityAccounts Capability = 1 << iota
	CapabilityCashBalances
	CapabilityPositions
	CapabilityDailyPerformance
	CapabilityAccountSnapshots
	CapabilityInstruments
	CapabilityMarketData
	CapabilityCandles
	CapabilityTrading
	CapabilityAccountEquity
)

const allCapabilities = CapabilityAccounts |
	CapabilityCashBalances |
	CapabilityPositions |
	CapabilityDailyPerformance |
	CapabilityAccountSnapshots |
	CapabilityInstruments |
	CapabilityMarketData |
	CapabilityCandles |
	CapabilityTrading |
	CapabilityAccountEquity

// CapabilitySet is an immutable-by-convention set of provider capabilities.
type CapabilitySet uint64

// NewCapabilitySet constructs a set from individual capability flags.
func NewCapabilitySet(capabilities ...Capability) CapabilitySet {
	var set CapabilitySet
	for _, capability := range capabilities {
		set |= CapabilitySet(capability)
	}
	return set
}

// Has reports whether every requested capability is present.
func (s CapabilitySet) Has(capabilities ...Capability) bool {
	for _, capability := range capabilities {
		if capability == 0 || uint64(capability)&uint64(allCapabilities) != uint64(capability) {
			return false
		}
		if uint64(s)&uint64(capability) != uint64(capability) {
			return false
		}
	}
	return true
}

func (s CapabilitySet) valid() bool {
	return uint64(s)&^uint64(allCapabilities) == 0
}

// ConfigField describes one provider- or connection-scoped configuration
// value. It intentionally carries no runtime value, especially no secret.
type ConfigField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Secret      bool   `json:"secret,omitempty"`
	Description string `json:"description,omitempty"`
}

// ConfigSchema describes the configuration accepted by a provider factory.
type ConfigSchema struct {
	ProviderFields   []ConfigField `json:"provider_fields,omitempty"`
	ConnectionFields []ConfigField `json:"connection_fields,omitempty"`
}

// ProviderDefinition is immutable metadata for one broker integration type.
// It describes the provider itself, not one user's configured connection.
type ProviderDefinition struct {
	Code         string        `json:"code"`
	Name         string        `json:"name"`
	DisplayName  string        `json:"display_name"`
	AuthModes    []AuthMode    `json:"auth_modes,omitempty"`
	Capabilities CapabilitySet `json:"capabilities"`
	ConfigSchema ConfigSchema  `json:"config_schema"`
}

func (d ProviderDefinition) normalized() ProviderDefinition {
	d.Code = strings.ToUpper(strings.TrimSpace(d.Code))
	d.Name = strings.TrimSpace(d.Name)
	d.DisplayName = strings.TrimSpace(d.DisplayName)
	if d.DisplayName == "" {
		d.DisplayName = d.Name
	}
	d.AuthModes = append([]AuthMode(nil), d.AuthModes...)
	for i, mode := range d.AuthModes {
		d.AuthModes[i] = AuthMode(strings.ToLower(strings.TrimSpace(string(mode))))
	}
	d.ConfigSchema.ProviderFields = normalizeConfigFields(d.ConfigSchema.ProviderFields)
	d.ConfigSchema.ConnectionFields = normalizeConfigFields(d.ConfigSchema.ConnectionFields)
	return d
}

func normalizeConfigFields(fields []ConfigField) []ConfigField {
	fields = append([]ConfigField(nil), fields...)
	for i := range fields {
		fields[i].Key = strings.TrimSpace(fields[i].Key)
		fields[i].Label = strings.TrimSpace(fields[i].Label)
		fields[i].Type = strings.TrimSpace(fields[i].Type)
		fields[i].Description = strings.TrimSpace(fields[i].Description)
	}
	return fields
}

func (d ProviderDefinition) validate() error {
	if d.Code == "" {
		return fmt.Errorf("provider code is required")
	}
	if d.Name == "" {
		return fmt.Errorf("provider name is required")
	}
	if !d.Capabilities.valid() {
		return fmt.Errorf("provider %s declares unknown capabilities", d.Code)
	}
	seenAuthModes := make(map[AuthMode]struct{}, len(d.AuthModes))
	for _, mode := range d.AuthModes {
		if !mode.valid() {
			return fmt.Errorf("provider %s declares unknown auth mode %q", d.Code, mode)
		}
		if _, exists := seenAuthModes[mode]; exists {
			return fmt.Errorf("provider %s declares duplicate auth mode %q", d.Code, mode)
		}
		seenAuthModes[mode] = struct{}{}
	}
	if err := validateConfigFields(d.Code, "provider", d.ConfigSchema.ProviderFields); err != nil {
		return err
	}
	return validateConfigFields(d.Code, "connection", d.ConfigSchema.ConnectionFields)
}

func validateConfigFields(providerCode, scope string, fields []ConfigField) error {
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		key := strings.TrimSpace(field.Key)
		if key == "" {
			return fmt.Errorf("provider %s has a %s config field without a key", providerCode, scope)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("provider %s has duplicate %s config field %q", providerCode, scope, key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// ConnectionConfig contains the provider-neutral inputs needed to open one
// runtime session. The store layer remains responsible for persistence and for
// keeping Secrets out of API responses and logs.
type ConnectionConfig struct {
	ID              int64             `json:"id"`
	ProviderCode    string            `json:"provider_code"`
	ConnectionKey   string            `json:"connection_key"`
	Name            string            `json:"name"`
	ProviderUserID  string            `json:"provider_user_id,omitempty"`
	Username        string            `json:"username,omitempty"`
	Environment     string            `json:"environment"`
	AuthMode        AuthMode          `json:"auth_mode"`
	ProviderConfig  map[string]any    `json:"provider_config,omitempty"`
	Config          map[string]any    `json:"config,omitempty"`
	ProviderSecrets map[string]string `json:"-"`
	Secrets         map[string]string `json:"-"`
}

// String and GoString intentionally expose only routing metadata. This keeps
// provider configuration, usernames, OAuth tokens, and API keys out of logs
// even when callers accidentally format the whole value with fmt.
func (c ConnectionConfig) String() string {
	return fmt.Sprintf(
		"ConnectionConfig{ID:%d ProviderCode:%q Environment:%q AuthMode:%q}",
		c.ID,
		c.ProviderCode,
		c.Environment,
		c.AuthMode,
	)
}

func (c ConnectionConfig) GoString() string { return c.String() }

// ProviderFactory creates runtime sessions for one registered provider type.
// Authentication remains an optional provider capability until the dedicated
// authentication lifecycle is introduced.
type ProviderFactory interface {
	Definition() ProviderDefinition
	Open(ctx context.Context, config ConnectionConfig) (BrokerSession, error)
}
