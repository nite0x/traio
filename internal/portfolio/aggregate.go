package portfolio

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/store"
)

// AggregatedPosition is one portfolio holding grouped by its canonical listing
// identity. Legs preserve the account-level broker projections used to calculate it.
type AggregatedPosition struct {
	PositionID           string            `json:"position_id"`
	InstrumentID         int64             `json:"instrument_id"`
	InstrumentIDs        []int64           `json:"instrument_ids"`
	AssetType            string            `json:"asset_type"`
	Market               string            `json:"market"`
	Symbol               string            `json:"symbol"`
	Name                 string            `json:"name,omitempty"`
	Currency             string            `json:"currency"`
	Quantity             float64           `json:"quantity"`
	AverageCost          float64           `json:"average_cost"`
	CostBasis            float64           `json:"cost_basis"`
	MarketPrice          float64           `json:"market_price"`
	MarketValue          float64           `json:"market_value"`
	PortfolioWeight      float64           `json:"portfolio_weight"`
	UnrealizedPnL        float64           `json:"unrealized_pnl"`
	UnrealizedPnLPercent *float64          `json:"unrealized_pnl_percent,omitempty"`
	RealizedPnL          float64           `json:"realized_pnl"`
	DailyPnL             *float64          `json:"daily_pnl,omitempty"`
	DailyPnLPercent      *float64          `json:"daily_pnl_percent,omitempty"`
	Brokers              []string          `json:"brokers"`
	Legs                 []broker.Position `json:"legs"`
	SyncedAt             string            `json:"synced_at,omitempty"`
}

type aggregatedPositionAccumulator struct {
	position      AggregatedPosition
	costBasis     float64
	dailyPnL      float64
	hasDailyPnL   bool
	brokerNames   map[string]struct{}
	instrumentIDs map[int64]struct{}
}

func isEquityListing(assetType string) bool {
	switch strings.ToLower(strings.TrimSpace(assetType)) {
	case "", "security", "stock", "stk", "equity", "us_equity", "etf", "fund", "collective_investment":
		return true
	default:
		return false
	}
}

func normalizedPositionMarket(position broker.Position) string {
	market := strings.ToUpper(strings.TrimSpace(position.Market))
	if market != "" && market != "UNKNOWN" {
		return market
	}
	if strings.EqualFold(strings.TrimSpace(position.Currency), "USD") {
		return "US"
	}
	if market != "" {
		return market
	}
	return "UNKNOWN"
}

func positionAggregationKey(position broker.Position) string {
	if !isEquityListing(position.AssetType) {
		return "instrument:" + strconv.FormatInt(position.InstrumentID, 10)
	}
	symbol := store.NormalizeInstrumentSymbol(position.Symbol)
	currency := strings.ToUpper(strings.TrimSpace(position.Currency))
	if symbol == "" || currency == "" {
		return "instrument:" + strconv.FormatInt(position.InstrumentID, 10)
	}
	return strings.Join([]string{
		"equity",
		symbol,
		normalizedPositionMarket(position),
		currency,
	}, ":")
}

func aggregatePositions(positions []broker.Position, netAssetValue float64) ([]AggregatedPosition, error) {
	groups := map[string]*aggregatedPositionAccumulator{}
	instrumentGroups := map[int64]string{}
	for _, leg := range positions {
		if leg.InstrumentID <= 0 {
			return nil, fmt.Errorf("position %s in %s is missing instrument_id", leg.Symbol, leg.Account)
		}
		key := positionAggregationKey(leg)
		if existingKey, ok := instrumentGroups[leg.InstrumentID]; ok {
			key = existingKey
		} else {
			instrumentGroups[leg.InstrumentID] = key
		}
		group := groups[key]
		if group == nil {
			group = &aggregatedPositionAccumulator{
				position: AggregatedPosition{
					PositionID: "position:" + strconv.FormatInt(leg.InstrumentID, 10), InstrumentID: leg.InstrumentID,
					InstrumentIDs: []int64{},
					AssetType:     leg.AssetType, Market: normalizedPositionMarket(leg), Symbol: store.NormalizeInstrumentSymbol(leg.Symbol),
					Name: leg.Name, Currency: strings.ToUpper(strings.TrimSpace(leg.Currency)), Brokers: []string{}, Legs: []broker.Position{},
				},
				brokerNames: map[string]struct{}{}, instrumentIDs: map[int64]struct{}{},
			}
			groups[key] = group
		}
		group.instrumentIDs[leg.InstrumentID] = struct{}{}
		if leg.InstrumentID < group.position.InstrumentID {
			group.position.InstrumentID = leg.InstrumentID
			group.position.PositionID = "position:" + strconv.FormatInt(leg.InstrumentID, 10)
		}
		if group.position.Name == "" && leg.Name != "" {
			group.position.Name = leg.Name
		}
		if !strings.EqualFold(group.position.AssetType, "etf") && strings.EqualFold(leg.AssetType, "etf") {
			group.position.AssetType = leg.AssetType
		}
		group.position.Quantity += leg.Quantity
		group.position.MarketValue += leg.MarketValue
		group.position.UnrealizedPnL += leg.Unrealized
		group.position.RealizedPnL += leg.Realized
		legCost := leg.Quantity * leg.AvgCost
		if leg.AvgCost == 0 && leg.MarketValue != 0 {
			legCost = leg.MarketValue - leg.Unrealized
		}
		group.costBasis += legCost
		if leg.DailyPnL != nil {
			group.dailyPnL += *leg.DailyPnL
			group.hasDailyPnL = true
		}
		if brokerName := strings.ToUpper(strings.TrimSpace(leg.Broker)); brokerName != "" {
			group.brokerNames[brokerName] = struct{}{}
		}
		group.position.Legs = append(group.position.Legs, leg)
		if leg.SyncedAt > group.position.SyncedAt {
			group.position.SyncedAt = leg.SyncedAt
		}
	}

	result := make([]AggregatedPosition, 0, len(groups))
	for _, group := range groups {
		position := group.position
		position.CostBasis = group.costBasis
		if position.Quantity != 0 {
			position.AverageCost = group.costBasis / position.Quantity
			position.MarketPrice = position.MarketValue / position.Quantity
		}
		if netAssetValue != 0 {
			position.PortfolioWeight = position.MarketValue / netAssetValue * 100
		}
		position.UnrealizedPnLPercent = percentOfCost(position.UnrealizedPnL, position.MarketValue)
		if group.hasDailyPnL {
			position.DailyPnL = floatPointer(group.dailyPnL)
			position.DailyPnLPercent = percentOfPriorValue(group.dailyPnL, position.MarketValue)
		}
		for brokerName := range group.brokerNames {
			position.Brokers = append(position.Brokers, brokerName)
		}
		for instrumentID := range group.instrumentIDs {
			position.InstrumentIDs = append(position.InstrumentIDs, instrumentID)
		}
		sort.Strings(position.Brokers)
		sort.Slice(position.InstrumentIDs, func(i, j int) bool { return position.InstrumentIDs[i] < position.InstrumentIDs[j] })
		sort.Slice(position.Legs, func(i, j int) bool {
			if position.Legs[i].Broker == position.Legs[j].Broker {
				return position.Legs[i].Account < position.Legs[j].Account
			}
			return position.Legs[i].Broker < position.Legs[j].Broker
		})
		result = append(result, position)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].MarketValue == result[j].MarketValue {
			return result[i].PositionID < result[j].PositionID
		}
		return result[i].MarketValue > result[j].MarketValue
	})
	return result, nil
}

func floatPointer(value float64) *float64 { return &value }

func (s *SyncService) AggregatedPositions(ctx context.Context) ([]AggregatedPosition, error) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return snapshot.Positions, nil
}

func (s *SyncService) AggregatedPosition(ctx context.Context, positionID string) (AggregatedPosition, error) {
	positions, err := s.AggregatedPositions(ctx)
	if err != nil {
		return AggregatedPosition{}, err
	}
	for _, position := range positions {
		if position.PositionID == positionID {
			return position, nil
		}
	}
	return AggregatedPosition{}, fmt.Errorf("%w: portfolio position %s", store.ErrNotFound, positionID)
}
