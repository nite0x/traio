package performance

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/nite/traio/internal/activity"
)

type calculator struct {
	input    Input
	base     string
	from     time.Time
	to       time.Time
	now      time.Time
	prices   map[int64]map[string]Price
	fx       map[FXPair]map[string]FXRate
	anchor   Anchor
	coverage Coverage
}

type datedLeg struct {
	activity activity.Activity
	leg      activity.Leg
}

func calculate(c calculator) (Series, error) {
	activities := append([]activity.Activity(nil), c.input.Activities...)
	sort.SliceStable(activities, func(i, j int) bool { return activityDate(activities[i]) < activityDate(activities[j]) })
	cash := map[string]*big.Rat{}
	positions := map[int64]*big.Rat{}
	costKnown := map[int64]bool{}
	byDate := map[string][]datedLeg{}
	for _, a := range activities {
		for _, leg := range a.Legs {
			date := legDate(a, leg)
			if date == "" || date > c.to.Format(time.DateOnly) {
				continue
			}
			byDate[date] = append(byDate[date], datedLeg{activity: a, leg: leg})
		}
	}

	// Replay prior activities to reconstruct opening state without fabricating a
	// balance snapshot. Valuation begins only at the requested/rebuild boundary.
	for _, a := range activities {
		for _, leg := range a.Legs {
			if date := legDate(a, leg); date != "" && date < c.from.Format(time.DateOnly) {
				if err := applyLeg(a, leg, cash, positions, costKnown); err != nil {
					return Series{}, err
				}
			}
		}
		if date := activityDate(a); date != "" && date < c.from.Format(time.DateOnly) {
			applyCostEvidence(a, costKnown)
		}
	}

	series := Series{
		AccountID: c.input.AccountID, BaseCurrency: c.base,
		AsOf: c.now.UTC().Format(time.RFC3339), Coverage: c.coverage,
		Points: []Point{}, CalculationVersion: CalculationVersion,
		ActivityGeneration: c.input.ActivityGeneration,
	}
	prevValue := ratPtr(c.anchor.TotalValue)
	twrFactor := new(big.Rat).SetInt64(1)
	returnsAllowed := c.coverage.Status == "complete"
	twrAvailable := returnsAllowed
	if c.anchor.CumulativeTWR != nil {
		anchorTWR, err := parseRat(*c.anchor.CumulativeTWR)
		if err != nil {
			return Series{}, err
		}
		twrFactor.Add(twrFactor, anchorTWR)
	} else if c.anchor.Date != "" {
		twrAvailable = false
	}
	firstPoint := c.anchor.Date == ""

	for date := c.from; !date.After(c.to); date = date.AddDate(0, 0, 1) {
		day := date.Format(time.DateOnly)
		dayEvents := byDate[day]
		for _, event := range dayEvents {
			if err := applyLeg(event.activity, event.leg, cash, positions, costKnown); err != nil {
				return Series{}, err
			}
			applyCostEvidence(event.activity, costKnown)
		}

		point := Point{
			Date: day, Quality: "official", Finality: "eod",
			MissingPrices: []string{}, MissingFX: []string{}, MissingCostBasis: []string{},
			SourceMix: []string{}, CalculationVersion: CalculationVersion,
		}
		if sameDate(date, c.now) || date.After(startOfDay(c.now)) {
			point.Quality = "estimated"
			point.Finality = "intraday"
		}
		if c.coverage.Status != "complete" {
			point.Quality = "partial"
		}
		sources := map[string]struct{}{}
		missingPrices := map[string]struct{}{}
		missingFX := map[string]struct{}{}

		cashValue, cashOK, err := valueCash(day, cash, c.base, c.fx, sources, missingFX)
		if err != nil {
			return Series{}, err
		}
		holdingsValue, holdingsOK, err := valuePositions(day, positions, c.input.Instruments, c.base, c.prices, c.fx, sources, missingPrices, missingFX)
		if err != nil {
			return Series{}, err
		}
		flow, flowOK, err := externalFlow(day, dayEvents, c.input.Instruments, c.base, c.prices, c.fx, sources, missingPrices, missingFX)
		if err != nil {
			return Series{}, err
		}

		if cashOK {
			point.CashValue = decimalPtr(cashValue)
		}
		if holdingsOK {
			point.HoldingsValue = decimalPtr(holdingsValue)
		}
		if flowOK {
			point.ExternalCashFlow = decimalPtr(flow)
		}
		complete := cashOK && holdingsOK && flowOK
		var total *big.Rat
		if complete {
			total = new(big.Rat).Add(cashValue, holdingsValue)
			point.TotalValue = decimalPtr(total)
		}
		if len(missingPrices) > 0 || len(missingFX) > 0 {
			point.Quality = "partial"
		}

		if returnsAllowed && complete && prevValue != nil && prevValue.Sign() != 0 {
			pnl := new(big.Rat).Sub(total, prevValue)
			pnl.Sub(pnl, flow)
			ret := new(big.Rat).Quo(pnl, prevValue)
			point.DailyPnL = decimalPtr(pnl)
			point.DailyReturn = decimalPtr(ret)
			if twrAvailable {
				twrFactor.Mul(twrFactor, new(big.Rat).Add(new(big.Rat).SetInt64(1), ret))
				point.CumulativeTWR = decimalPtr(new(big.Rat).Sub(twrFactor, new(big.Rat).SetInt64(1)))
			}
		} else if returnsAllowed && complete && firstPoint {
			zero := "0"
			point.CumulativeTWR = &zero
		} else if !complete || !returnsAllowed {
			twrAvailable = false
		}
		if complete {
			prevValue = new(big.Rat).Set(total)
		} else {
			prevValue = nil
		}
		firstPoint = false

		for id, quantity := range positions {
			if quantity.Sign() != 0 && !costKnown[id] {
				point.MissingCostBasis = append(point.MissingCostBasis, instrumentLabel(c.input.Instruments[id]))
			}
		}
		point.MissingPrices = sortedKeys(missingPrices)
		point.MissingFX = sortedKeys(missingFX)
		point.SourceMix = sortedKeys(sources)
		if point.Quality == "official" {
			for _, source := range point.SourceMix {
				if source == "yahoo" {
					point.Quality = "estimated"
					break
				}
			}
		}
		sort.Strings(point.MissingCostBasis)
		series.Points = append(series.Points, point)
	}
	return series, nil
}

func applyLeg(a activity.Activity, leg activity.Leg, cash map[string]*big.Rat, positions map[int64]*big.Rat, costKnown map[int64]bool) error {
	if a.Status != activity.StatusEffective || a.BookingStatus != activity.BookingBooked {
		return nil
	}
	switch leg.Kind {
	case "cash":
		value, err := parseRat(leg.CashDelta)
		if err != nil {
			return err
		}
		currency := strings.ToUpper(leg.Currency)
		cash[currency] = addTo(cash[currency], value)
	case "position":
		value, err := parseRat(leg.QuantityDelta)
		if err != nil {
			return err
		}
		positions[leg.InstrumentID] = addTo(positions[leg.InstrumentID], value)
		if a.Fill != nil && a.Fill.Price != "" {
			costKnown[leg.InstrumentID] = true
		}
	}
	return nil
}

func applyCostEvidence(a activity.Activity, costKnown map[int64]bool) {
	for _, adjustment := range a.CostAdjustments {
		if adjustment.InstrumentID > 0 && (adjustment.BasisDelta != "" || adjustment.BasisAfter != "") {
			costKnown[adjustment.InstrumentID] = true
		}
	}
}

func valueCash(day string, balances map[string]*big.Rat, base string, fx map[FXPair]map[string]FXRate, sources, missing map[string]struct{}) (*big.Rat, bool, error) {
	total := new(big.Rat)
	ok := true
	for currency, balance := range balances {
		if balance.Sign() == 0 {
			continue
		}
		rate, source, found, err := fxAt(day, currency, base, fx)
		if err != nil {
			return nil, false, err
		}
		if !found {
			missing[currency+"/"+base] = struct{}{}
			ok = false
			continue
		}
		if source != "" {
			sources[source] = struct{}{}
		}
		total.Add(total, new(big.Rat).Mul(balance, rate))
	}
	return total, ok, nil
}

func valuePositions(day string, positions map[int64]*big.Rat, instruments map[int64]Instrument, base string, prices map[int64]map[string]Price, fx map[FXPair]map[string]FXRate, sources, missingPrices, missingFX map[string]struct{}) (*big.Rat, bool, error) {
	total := new(big.Rat)
	ok := true
	for id, quantity := range positions {
		if quantity.Sign() == 0 {
			continue
		}
		instrument := instruments[id]
		price, source, found, err := priceAt(day, id, prices)
		if err != nil {
			return nil, false, err
		}
		if !found {
			missingPrices[instrumentLabel(instrument)] = struct{}{}
			ok = false
			continue
		}
		if source != "" {
			sources[source] = struct{}{}
		}
		rate, fxSource, found, err := fxAt(day, instrument.Currency, base, fx)
		if err != nil {
			return nil, false, err
		}
		if !found {
			missingFX[strings.ToUpper(instrument.Currency)+"/"+base] = struct{}{}
			ok = false
			continue
		}
		if fxSource != "" {
			sources[fxSource] = struct{}{}
		}
		value := new(big.Rat).Mul(quantity, price)
		value.Mul(value, rate)
		total.Add(total, value)
	}
	return total, ok, nil
}

func externalFlow(day string, events []datedLeg, instruments map[int64]Instrument, base string, prices map[int64]map[string]Price, fx map[FXPair]map[string]FXRate, sources, missingPrices, missingFX map[string]struct{}) (*big.Rat, bool, error) {
	total := new(big.Rat)
	ok := true
	for _, event := range events {
		a, leg := event.activity, event.leg
		if a.Status != activity.StatusEffective || a.BookingStatus != activity.BookingBooked || !externalFlowType(a.Type) {
			continue
		}
		if leg.Kind == "cash" {
			amount, err := parseRat(leg.CashDelta)
			if err != nil {
				return nil, false, err
			}
			rate, source, found, err := fxAt(day, leg.Currency, base, fx)
			if err != nil {
				return nil, false, err
			}
			if !found {
				missingFX[strings.ToUpper(leg.Currency)+"/"+base] = struct{}{}
				ok = false
				continue
			}
			if source != "" {
				sources[source] = struct{}{}
			}
			total.Add(total, new(big.Rat).Mul(amount, rate))
		}
		if leg.Kind == "position" && a.Type == "security_transfer" {
			quantity, err := parseRat(leg.QuantityDelta)
			if err != nil {
				return nil, false, err
			}
			instrument := instruments[leg.InstrumentID]
			price, source, found, err := priceAt(day, leg.InstrumentID, prices)
			if err != nil {
				return nil, false, err
			}
			if !found {
				missingPrices[instrumentLabel(instrument)] = struct{}{}
				ok = false
				continue
			}
			rate, fxSource, found, err := fxAt(day, instrument.Currency, base, fx)
			if err != nil {
				return nil, false, err
			}
			if !found {
				missingFX[strings.ToUpper(instrument.Currency)+"/"+base] = struct{}{}
				ok = false
				continue
			}
			if source != "" {
				sources[source] = struct{}{}
			}
			if fxSource != "" {
				sources[fxSource] = struct{}{}
			}
			value := new(big.Rat).Mul(quantity, price)
			value.Mul(value, rate)
			total.Add(total, value)
		}
	}
	return total, ok, nil
}

func externalFlowType(value string) bool {
	switch value {
	case "deposit", "withdrawal", "cash_transfer", "security_transfer":
		return true
	default:
		return false
	}
}

func activityDate(a activity.Activity) string {
	earliest := ""
	for _, leg := range a.Legs {
		date := legDate(a, leg)
		if date != "" && (earliest == "" || date < earliest) {
			earliest = date
		}
	}
	if earliest != "" {
		return earliest
	}
	if a.SettlementDate != "" {
		return a.SettlementDate
	}
	if a.TradeDate != "" {
		return a.TradeDate
	}
	if len(a.OccurredAt) >= len(time.DateOnly) {
		return a.OccurredAt[:len(time.DateOnly)]
	}
	return ""
}

func legDate(a activity.Activity, leg activity.Leg) string {
	if leg.EffectiveDate != "" {
		return leg.EffectiveDate
	}
	if leg.SettlementDate != "" {
		return leg.SettlementDate
	}
	if a.SettlementDate != "" {
		return a.SettlementDate
	}
	if a.TradeDate != "" {
		return a.TradeDate
	}
	if len(a.OccurredAt) >= len(time.DateOnly) {
		return a.OccurredAt[:len(time.DateOnly)]
	}
	return ""
}

func priceAt(day string, id int64, prices map[int64]map[string]Price) (*big.Rat, string, bool, error) {
	values := prices[id]
	selected := ""
	for date := range values {
		if date <= day && date > selected {
			selected = date
		}
	}
	if selected == "" {
		return nil, "", false, nil
	}
	if values[selected].Missing {
		return nil, "", false, nil
	}
	if staleDailyValue(day, selected) {
		return nil, "", false, nil
	}
	value, err := parseRat(values[selected].Value)
	return value, values[selected].Source, err == nil, err
}

func fxAt(day, from, to string, rates map[FXPair]map[string]FXRate) (*big.Rat, string, bool, error) {
	from, to = strings.ToUpper(from), strings.ToUpper(to)
	if from == "" || to == "" {
		return nil, "", false, nil
	}
	if from == to {
		return new(big.Rat).SetInt64(1), "", true, nil
	}
	pair := FXPair{From: from, To: to}
	values := rates[pair]
	selected := ""
	for date := range values {
		if date <= day && date > selected {
			selected = date
		}
	}
	if selected == "" {
		return nil, "", false, nil
	}
	if staleDailyValue(day, selected) {
		return nil, "", false, nil
	}
	value, err := parseRat(values[selected].Value)
	return value, values[selected].Source, err == nil, err
}

func staleDailyValue(day, observed string) bool {
	requested, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return true
	}
	seen, err := time.Parse(time.DateOnly, observed)
	if err != nil {
		return true
	}
	// Seven calendar days covers weekends and ordinary exchange holidays while
	// preventing a suspended/missing instrument or FX pair from being carried
	// forward indefinitely.
	return requested.Sub(seen) > 7*24*time.Hour
}

func parseRat(value string) (*big.Rat, error) {
	value, err := activity.CanonicalDecimal(value)
	if err != nil {
		return nil, err
	}
	r, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, fmt.Errorf("invalid decimal")
	}
	return r, nil
}

func ratPtr(value *string) *big.Rat {
	if value == nil {
		return nil
	}
	r, err := parseRat(*value)
	if err != nil {
		return nil
	}
	return r
}

func decimalPtr(value *big.Rat) *string {
	if value == nil {
		return nil
	}
	text := value.FloatString(18)
	canonical, err := activity.CanonicalDecimal(text)
	if err != nil {
		canonical = text
	}
	return &canonical
}

func addTo(current, delta *big.Rat) *big.Rat {
	if current == nil {
		current = new(big.Rat)
	}
	return new(big.Rat).Add(current, delta)
}

func instrumentLabel(instrument Instrument) string {
	if instrument.Symbol != "" {
		return instrument.Symbol
	}
	return fmt.Sprintf("instrument:%d", instrument.ID)
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sameDate(a, b time.Time) bool { return a.Format(time.DateOnly) == b.Format(time.DateOnly) }
func startOfDay(value time.Time) time.Time {
	y, m, d := value.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, value.Location())
}
