package broker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type providerFactoryStub struct {
	definition ProviderDefinition
}

func (s providerFactoryStub) Definition() ProviderDefinition { return s.definition }
func (providerFactoryStub) Open(context.Context, ConnectionConfig) (BrokerSession, error) {
	return nil, nil
}

func TestCapabilitySet(t *testing.T) {
	set := NewCapabilitySet(CapabilityAccounts, CapabilityPositions, CapabilityTrading)
	if !set.Has(CapabilityAccounts, CapabilityPositions) {
		t.Fatal("expected accounts and positions capabilities")
	}
	if set.Has(CapabilityMarketData) {
		t.Fatal("did not expect market-data capability")
	}
	if set.Has(0) || set.Has(Capability(1<<63)) {
		t.Fatal("zero and unknown capability flags must not be present")
	}
}

func TestProviderRegistryRegisterAndResolve(t *testing.T) {
	registry := NewProviderRegistry()
	factory := providerFactoryStub{definition: ProviderDefinition{
		Code:         " schwab ",
		Name:         "Charles Schwab",
		AuthModes:    []AuthMode{AuthModeOAuth},
		Capabilities: NewCapabilitySet(CapabilityAccounts, CapabilityPositions),
		ConfigSchema: ConfigSchema{ProviderFields: []ConfigField{{Key: "client_id", Required: true, Secret: true}}},
	}}
	if err := registry.Register(factory); err != nil {
		t.Fatalf("register provider: %v", err)
	}

	got, err := registry.Factory("ScHwAb")
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}
	if got.Definition().Code != factory.Definition().Code {
		t.Fatalf("resolved wrong factory: %#v", got.Definition())
	}

	definitions := registry.Definitions()
	if len(definitions) != 1 || definitions[0].Code != "SCHWAB" || definitions[0].DisplayName != "Charles Schwab" {
		t.Fatalf("unexpected definitions: %#v", definitions)
	}
}

func TestProviderRegistryRejectsDuplicateCode(t *testing.T) {
	registry := NewProviderRegistry()
	first := providerFactoryStub{definition: ProviderDefinition{Code: "IBKR", Name: "Interactive Brokers"}}
	duplicate := providerFactoryStub{definition: ProviderDefinition{Code: "ibkr", Name: "Another IBKR"}}
	if err := registry.Register(first); err != nil {
		t.Fatalf("register first provider: %v", err)
	}
	if err := registry.Register(duplicate); !errors.Is(err, ErrProviderAlreadyRegistered) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestProviderRegistryReturnsNotRegisteredError(t *testing.T) {
	registry := NewProviderRegistry()
	if _, err := registry.Factory("missing"); !errors.Is(err, ErrProviderNotRegistered) {
		t.Fatalf("expected not-registered error, got %v", err)
	}
}

func TestProviderRegistryRejectsInvalidDefinitions(t *testing.T) {
	tests := []struct {
		name       string
		definition ProviderDefinition
	}{
		{name: "missing code", definition: ProviderDefinition{Name: "Broker"}},
		{name: "missing name", definition: ProviderDefinition{Code: "BROKER"}},
		{name: "unknown capability", definition: ProviderDefinition{Code: "BROKER", Name: "Broker", Capabilities: CapabilitySet(1 << 63)}},
		{name: "unknown auth mode", definition: ProviderDefinition{Code: "BROKER", Name: "Broker", AuthModes: []AuthMode{"password"}}},
		{name: "duplicate auth mode", definition: ProviderDefinition{Code: "BROKER", Name: "Broker", AuthModes: []AuthMode{AuthModeOAuth, AuthModeOAuth}}},
		{name: "duplicate config field", definition: ProviderDefinition{Code: "BROKER", Name: "Broker", ConfigSchema: ConfigSchema{ConnectionFields: []ConfigField{{Key: "token"}, {Key: "token"}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewProviderRegistry()
			err := registry.Register(providerFactoryStub{definition: tt.definition})
			if !errors.Is(err, ErrInvalidProviderDefinition) {
				t.Fatalf("expected invalid-definition error, got %v", err)
			}
		})
	}
}

func TestConnectionConfigFormattingRedactsValues(t *testing.T) {
	config := ConnectionConfig{
		ID: 7, ProviderCode: "SCHWAB", Username: "private-user",
		ProviderConfig:  map[string]any{"redirect_uri": "https://private.example/callback"},
		ProviderSecrets: map[string]string{"client_secret": "provider-secret"},
		Config:          map[string]any{"account": "private-account"},
		Secrets:         map[string]string{"access_token": "connection-secret"},
	}
	formatted := fmt.Sprintf("%+v %#v", config, config)
	for _, secret := range []string{
		"private-user", "private.example", "provider-secret", "private-account", "connection-secret",
	} {
		if strings.Contains(formatted, secret) {
			t.Fatalf("formatted connection config leaked %q: %s", secret, formatted)
		}
	}
}

func TestProviderRegistryDefinitionSnapshotsAreIndependent(t *testing.T) {
	registry := NewProviderRegistry()
	factory := providerFactoryStub{definition: ProviderDefinition{
		Code:      "ALPACA",
		Name:      "Alpaca",
		AuthModes: []AuthMode{AuthModeAPIKey},
		ConfigSchema: ConfigSchema{
			ConnectionFields: []ConfigField{{Key: "api_key", Secret: true}},
		},
	}}
	if err := registry.Register(factory); err != nil {
		t.Fatalf("register provider: %v", err)
	}

	first := registry.Definitions()
	first[0].AuthModes[0] = AuthModeOAuth
	first[0].ConfigSchema.ConnectionFields[0].Key = "changed"
	second := registry.Definitions()
	if second[0].AuthModes[0] != AuthModeAPIKey || second[0].ConfigSchema.ConnectionFields[0].Key != "api_key" {
		t.Fatalf("registry metadata was mutated through a snapshot: %#v", second[0])
	}
}

func TestZeroValueProviderRegistryCanRegister(t *testing.T) {
	var registry ProviderRegistry
	err := registry.Register(providerFactoryStub{definition: ProviderDefinition{Code: "IBKR", Name: "Interactive Brokers"}})
	if err != nil {
		t.Fatalf("register with zero-value registry: %v", err)
	}
}
