package broker

import (
	"context"
	"time"
)

// ConnectionState is the provider-neutral lifecycle state of a runtime session.
type ConnectionState string

const (
	// ConnectionStateDisconnected means the session has no verified credentials.
	ConnectionStateDisconnected ConnectionState = "disconnected"
	// ConnectionStateAuthenticating means an external login action is pending.
	ConnectionStateAuthenticating ConnectionState = "authenticating"
	// ConnectionStateConnected means authentication was verified successfully.
	ConnectionStateConnected ConnectionState = "connected"
	// ConnectionStateDegraded is reserved for authenticated sessions whose
	// non-authentication dependencies are only partially available.
	ConnectionStateDegraded ConnectionState = "degraded"
	// ConnectionStateError means the most recent health check failed.
	ConnectionStateError ConnectionState = "error"
)

// ConnectionHealth is a point-in-time health report for a runtime session.
type ConnectionHealth struct {
	State     ConnectionState `json:"state"`
	Message   string          `json:"message,omitempty"`
	CheckedAt time.Time       `json:"checked_at"`
}

// BrokerSession is one opened runtime connection. Optional broker features are
// exposed by implementing the small capability interfaces in this package.
type BrokerSession interface {
	ConnectionID() int64
	ProviderCode() string
	Health(ctx context.Context) (ConnectionHealth, error)
	Close(ctx context.Context) error
}
