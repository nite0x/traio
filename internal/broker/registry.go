package broker

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	// ErrProviderNotRegistered is returned when a provider code has no factory.
	ErrProviderNotRegistered = errors.New("broker provider is not registered")
	// ErrProviderAlreadyRegistered is returned for duplicate provider codes.
	ErrProviderAlreadyRegistered = errors.New("broker provider is already registered")
	// ErrInvalidProviderDefinition is returned when factory metadata is incomplete.
	ErrInvalidProviderDefinition = errors.New("invalid broker provider definition")
)

type providerRegistration struct {
	definition ProviderDefinition
	factory    ProviderFactory
}

// ProviderRegistry is a concurrency-safe catalog of provider factories.
// Provider codes are normalized to upper case for registration and lookup.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]providerRegistration
}

// NewProviderRegistry creates an empty provider registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{providers: make(map[string]providerRegistration)}
}

// Register validates and registers a factory. A provider code can be
// registered only once, including case-insensitive variants of that code.
func (r *ProviderRegistry) Register(factory ProviderFactory) error {
	if factory == nil {
		return fmt.Errorf("%w: factory is nil", ErrInvalidProviderDefinition)
	}
	definition := factory.Definition().normalized()
	if err := definition.validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProviderDefinition, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providers == nil {
		r.providers = make(map[string]providerRegistration)
	}
	if _, exists := r.providers[definition.Code]; exists {
		return fmt.Errorf("%w: %s", ErrProviderAlreadyRegistered, definition.Code)
	}
	r.providers[definition.Code] = providerRegistration{definition: definition, factory: factory}
	return nil
}

// Factory resolves a registered provider factory by code.
func (r *ProviderRegistry) Factory(code string) (ProviderFactory, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	r.mu.RLock()
	registration, exists := r.providers[code]
	r.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotRegistered, code)
	}
	return registration.factory, nil
}

// Definitions returns a stable, code-sorted snapshot of registered metadata.
func (r *ProviderRegistry) Definitions() []ProviderDefinition {
	r.mu.RLock()
	definitions := make([]ProviderDefinition, 0, len(r.providers))
	for _, registration := range r.providers {
		definitions = append(definitions, registration.definition.normalized())
	}
	r.mu.RUnlock()
	sort.Slice(definitions, func(i, j int) bool {
		return definitions[i].Code < definitions[j].Code
	})
	return definitions
}
