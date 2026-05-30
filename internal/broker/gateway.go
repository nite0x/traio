package broker

import "context"

// GatewayController abstracts the IBKR gateway so higher layers (api, runtime)
// do not import the ibkr package directly. Desktop builds inject a real adapter
// around *ibkr.GatewayManager; iOS builds inject nil and the /ibkr/* routes
// degrade gracefully via gw == nil guards.
//
// Status returns a value that serializes to the gateway status JSON. It is
// typed as any so this package stays free of ibkr-package types, which is what
// lets the iOS build drop the ibkr package (and its chromedp / os.exec deps) at
// compile time.
type GatewayController interface {
	Status() any
	StartGateway(ctx context.Context) error
	StopGateway(keepSession bool)
	Reconnect()
}
