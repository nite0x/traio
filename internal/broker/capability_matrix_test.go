package broker_test

import (
	"testing"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/broker/alpaca"
	"github.com/nite/traio/internal/broker/ibkr"
	"github.com/nite/traio/internal/broker/schwab"
)

func TestCurrentBrokerCapabilityMatrix(t *testing.T) {
	tests := []struct {
		name     string
		client   any
		expected broker.CapabilitySet
	}{
		{
			name:   "IBKR",
			client: (*ibkr.Client)(nil),
			expected: broker.NewCapabilitySet(
				broker.CapabilityAccounts,
				broker.CapabilityCashBalances,
				broker.CapabilityPositions,
				broker.CapabilityDailyPerformance,
				broker.CapabilityInstruments,
				broker.CapabilityMarketData,
				broker.CapabilityCandles,
				broker.CapabilityTrading,
				broker.CapabilityAccountEquity,
			),
		},
		{
			name:   "Schwab",
			client: (*schwab.Client)(nil),
			expected: broker.NewCapabilitySet(
				broker.CapabilityAccounts,
				broker.CapabilityCashBalances,
				broker.CapabilityPositions,
				broker.CapabilityDailyPerformance,
				broker.CapabilityAccountSnapshots,
				broker.CapabilityMarketData,
				broker.CapabilityTrading,
				broker.CapabilityAccountEquity,
			),
		},
		{
			name:   "Alpaca",
			client: (*alpaca.Client)(nil),
			expected: broker.NewCapabilitySet(
				broker.CapabilityAccounts,
				broker.CapabilityCashBalances,
				broker.CapabilityPositions,
				broker.CapabilityDailyPerformance,
				broker.CapabilityTrading,
				broker.CapabilityAccountEquity,
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if actual := detectedCapabilities(tt.client); actual != tt.expected {
				t.Fatalf("capability matrix changed: got %b, want %b", actual, tt.expected)
			}
		})
	}
}

func detectedCapabilities(client any) broker.CapabilitySet {
	var capabilities []broker.Capability
	if _, ok := client.(broker.AccountProvider); ok {
		capabilities = append(capabilities, broker.CapabilityAccounts, broker.CapabilityCashBalances)
	}
	if _, ok := client.(broker.PositionProvider); ok {
		capabilities = append(capabilities, broker.CapabilityPositions)
	}
	if _, ok := client.(broker.PerformanceProvider); ok {
		capabilities = append(capabilities, broker.CapabilityDailyPerformance)
	}
	if _, ok := client.(broker.PortfolioProvider); ok {
		capabilities = append(capabilities, broker.CapabilityAccountSnapshots)
	}
	if _, ok := client.(broker.InstrumentProvider); ok {
		capabilities = append(capabilities, broker.CapabilityInstruments)
	}
	if _, single := client.(broker.MarketDataProvider); single {
		capabilities = append(capabilities, broker.CapabilityMarketData)
	} else if _, batch := client.(broker.BatchMarketDataProvider); batch {
		capabilities = append(capabilities, broker.CapabilityMarketData)
	}
	if _, ok := client.(broker.CandleProvider); ok {
		capabilities = append(capabilities, broker.CapabilityCandles)
	}
	if _, ok := client.(broker.TradingProvider); ok {
		capabilities = append(capabilities, broker.CapabilityTrading)
	}
	if _, ok := client.(broker.AccountEquityProvider); ok {
		capabilities = append(capabilities, broker.CapabilityAccountEquity)
	}
	return broker.NewCapabilitySet(capabilities...)
}
