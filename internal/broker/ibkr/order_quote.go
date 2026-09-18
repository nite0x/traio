package ibkr

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/nite/traio/internal/broker"
)

func (c *Client) OrderQuote(ctx context.Context, id string) (broker.OrderQuote, error) {
	conid, err := strconv.ParseInt(id, 10, 64)
	if err != nil || conid <= 0 {
		return broker.OrderQuote{}, invalidOrder("instrument_id must be a positive numeric conid")
	}
	c.orderFlow.mu.Lock()
	defer c.orderFlow.mu.Unlock()
	if c.orderFlow.pending != nil {
		return broker.OrderQuote{}, broker.NewOrderError("confirmation_pending", "resolve the pending IBKR order confirmation first")
	}
	if _, err := c.TradingAccounts(ctx); err != nil {
		return broker.OrderQuote{}, err
	}
	quote := broker.OrderQuote{ConID: conid, LastKind: "last"}
	// The first snapshot can just establish the subscription. Retry that read once;
	// subsequent UI polling fills in fields without ever treating missing data as zero.
	for attempt := 0; attempt < 2; attempt++ {
		var rows []map[string]any
		if err := c.getTradingJSON(ctx, "/iserver/marketdata/snapshot?conids="+strconv.FormatInt(conid, 10)+"&fields=31,84,86,82,83,6509,7741", &rows); err != nil {
			return broker.OrderQuote{}, err
		}
		for _, row := range rows {
			rowID, err := strconv.ParseInt(textValue(row["conid"]), 10, 64)
			if err != nil || rowID != conid {
				continue
			}
			quote = normalizeOrderQuote(conid, row)
			break
		}
		if quote.Last != nil || quote.Bid != nil || quote.Ask != nil || strings.HasPrefix(quote.Availability, "N") || attempt == 1 {
			return quote, nil
		}
		timer := time.NewTimer(400 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return broker.OrderQuote{}, ctx.Err()
		case <-timer.C:
		}
	}
	return quote, nil
}

func normalizeOrderQuote(conid int64, raw map[string]any) broker.OrderQuote {
	last, kind := textValue(raw["31"]), "last"
	if strings.HasPrefix(last, "C") {
		kind, last = "close", strings.TrimSpace(last[1:])
	}
	if strings.HasPrefix(last, "H") {
		kind, last = "halted", strings.TrimSpace(last[1:])
	}
	quote := broker.OrderQuote{ConID: conid, LastKind: kind, Availability: textValue(raw["6509"]),
		Last: orderQuoteNumber(last, true), Bid: orderQuoteNumber(raw["84"], true), Ask: orderQuoteNumber(raw["86"], true),
		Change: orderQuoteNumber(raw["82"], false), ChangePct: orderQuoteNumber(raw["83"], false), PriorClose: orderQuoteNumber(raw["7741"], true)}
	if updated, err := strconv.ParseInt(textValue(raw["_updated"]), 10, 64); err == nil && updated > 0 {
		quote.UpdatedAt = time.UnixMilli(updated).UTC().Format(time.RFC3339Nano)
	}
	return quote
}

func orderQuoteNumber(raw any, positive bool) *float64 {
	text := strings.TrimSuffix(strings.ReplaceAll(textValue(raw), ",", ""), "%")
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || (positive && value <= 0) {
		return nil
	}
	return &value
}
