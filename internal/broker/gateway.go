package broker

import "context"

// GatewayController abstracts the IBKR gateway so higher layers (api, runtime)
// do not import the ibkr package directly.
//
// Status returns a value that serializes to the gateway status JSON. It is
// typed as any so this package stays free of ibkr-package types.
type GatewayController interface {
	Status() any
	LoginURL() string
	StartGateway(ctx context.Context) error
	StopGateway(keepSession bool) error
	Reconnect() error
	Upgrade(ctx context.Context) error
	Rollback(ctx context.Context) error
}
