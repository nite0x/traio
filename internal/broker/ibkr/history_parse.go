package ibkr

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/nite/traio/internal/activity"
)

const MaxActivityXMLBytes = 20 << 20
const maxActivityXMLDepth = 32
const maxActivityRecords = 100000

// ParseActivityXML parses Activity Flex execution and cash/asset movement rows.
// XML entities and directives are rejected; input size, depth, attributes and
// record count are bounded. Account identification is preserved, never granted
// access implicitly. The importer must map it to an authorized canonical account.
func ParseActivityXML(body []byte) ([]activity.RawRecord, error) {
	if len(body) == 0 || len(body) > MaxActivityXMLBytes {
		return nil, fmt.Errorf("activity XML must contain 1 to %d bytes", MaxActivityXMLBytes)
	}
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = true
	records := []activity.RawRecord{}
	stack := []string{}
	accountID := ""
	generated := ""
	root := ""
	row := 0
	statementStart := 0
	statementEnd := ""
	statementFrom := ""
	costTrades, costCorporate := false, false
	transferLots := []map[string]string{}
	sum := sha256.Sum256(body)
	docID := hex.EncodeToString(sum[:])
	for {
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("invalid activity XML")
		}
		switch t := tok.(type) {
		case xml.Directive:
			return nil, fmt.Errorf("XML directives and external entities are not supported")
		case xml.ProcInst:
			if t.Target != "xml" {
				return nil, fmt.Errorf("XML processing instructions are not supported")
			}
		case xml.StartElement:
			if len(stack) == 0 {
				if root != "" {
					return nil, fmt.Errorf("multiple XML roots")
				}
				root = t.Name.Local
				if root != "FlexQueryResponse" && root != "FlexStatementResponse" && root != "FlexStatements" && root != "FlexStatement" {
					return nil, fmt.Errorf("expected IBKR Activity Flex XML")
				}
			}
			stack = append(stack, t.Name.Local)
			if len(stack) > maxActivityXMLDepth || len(t.Attr) > 256 {
				return nil, fmt.Errorf("activity XML structural limit exceeded")
			}
			attrs := map[string]string{}
			for _, a := range t.Attr {
				key := a.Name.Local
				if sensitiveActivityField(key) {
					continue
				}
				attrs[key] = strings.TrimSpace(a.Value)
			}
			if t.Name.Local == "FlexStatement" {
				accountID = attrs["accountId"]
				generated = attrs["whenGenerated"]
				statementStart = len(records)
				statementEnd = activity.NormalizeDate(attrs["toDate"])
				statementFrom = activity.NormalizeDate(attrs["fromDate"])
				costTrades, costCorporate = false, false
				transferLots = nil
			}
			if t.Name.Local == "Trades" {
				costTrades = true
			}
			if t.Name.Local == "CorporateActions" {
				costCorporate = true
			}
			if t.Name.Local == "OpenPosition" && strings.EqualFold(attrs["levelOfDetail"], "LOT") {
				if attrs["accountId"] == "" {
					attrs["accountId"] = accountID
				}
				transferLots = append(transferLots, attrs)
			}
			if len(stack) < 2 || len(attrs) == 0 {
				continue
			}
			if !activityRow(t.Name.Local, attrs) {
				continue
			}
			row++
			if row > maxActivityRecords {
				return nil, fmt.Errorf("too many activity rows")
			}
			if attrs["accountId"] == "" {
				attrs["accountId"] = accountID
			}
			r := normalizeFlexActivity(t.Name.Local, attrs)
			r.SourceUpdatedAt = historyTimestamp(generated)
			if r.Key == "" {
				r.Key = docID + ":" + strings.Join(stack, "/") + ":" + strconv.Itoa(row)
				r.Activity.Warnings = append(r.Activity.Warnings, "source_identity_missing")
				if r.Activity.Status == activity.StatusEffective {
					r.Activity.Status = activity.StatusNeedsReview
				}
			}
			payload, _ := json.Marshal(attrs)
			r.Payload = payload
			finishHistoryRecord(&r)
			records = append(records, r)
		case xml.EndElement:
			if t.Name.Local == "FlexStatement" {
				if costTrades && costCorporate {
					attachTransferLotCosts(records[statementStart:], transferLots, statementFrom, statementEnd)
				}
				accountID = ""
				generated = ""
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if root == "" || len(stack) != 0 {
		return nil, fmt.Errorf("invalid activity XML")
	}
	return records, nil
}

func sensitiveActivityField(key string) bool {
	k := strings.ToLower(key)
	return strings.Contains(k, "token") || strings.Contains(k, "password") || strings.Contains(k, "secret") ||
		k == "authorization" || k == "accountalias" || k == "accountname" || k == "accounttitle" ||
		k == "transferaccount" || k == "transferaccountname" || k == "traderid" || k == "allocatedto" ||
		k == "submitter" || k == "account_allocation_name"
}
func activityRow(name string, a map[string]string) bool {
	switch name {
	case "Trade":
		return a["levelOfDetail"] == "" || strings.EqualFold(a["levelOfDetail"], "EXECUTION") || strings.EqualFold(a["levelOfDetail"], "EXECUTIONS")
	case "CashTransaction", "Transfer", "CorporateAction", "OptionEAE", "OptionEAETransaction":
		return true
	// These are alternative views of already counted activities or snapshots.
	case "Order", "ClosedLot", "WashSale", "StatementOfFundsLine", "CashReportCurrency", "OpenPosition", "EquitySummaryInBase", "FinancialInstrument", "ConversionRate", "ChangeInDividendAccrual", "OpenDividendAccrual", "InterestAccrual", "UnbookedTrade", "UnsettledTransfer", "TradeTransfer", "CommissionDetail":
		return false
	}
	// Preserve unfamiliar business rows with financial content, without storing
	// account profile sections or treating summary amounts as new cash movements.
	if strings.Contains(strings.ToLower(name), "summary") || strings.Contains(strings.ToLower(name), "accrual") {
		return false
	}
	return a["transactionID"] != "" || a["transactionId"] != "" || ((a["amount"] != "" || a["quantity"] != "") && (a["type"] != "" || a["dateTime"] != ""))
}
func normalizeFlexActivity(name string, m map[string]string) activity.RawRecord {
	a := activity.Activity{Provider: "IBKR", ProviderAccountID: m["accountId"], Type: "unknown", Subtype: m["type"], Status: activity.StatusEffective, BookingStatus: activity.BookingBooked, Description: m["description"], TradeDate: activity.NormalizeDate(firstHistory(m, "tradeDate", "dateTime", "date", "reportDate")), SettlementDate: activity.NormalizeDate(firstHistory(m, "settleDateTarget", "settleDate")), OccurredAt: historyTimestamp(m["dateTime"]), SourceTimezone: m["timeZone"], TimePrecision: "date"}
	if a.OccurredAt != "" {
		a.TimePrecision = "second"
	}
	r := activity.RawRecord{Source: "flex", Namespace: "ibkr.flex", RecordType: name, ProviderAccountID: a.ProviderAccountID, Activity: a}
	if id := firstHistory(m, "transactionID", "transactionId"); id != "" {
		r.Key = id
		r.Identities = append(r.Identities, activity.Identity{Namespace: "ibkr.flex", Kind: name, ExternalID: id})
	}
	switch name {
	case "Trade":
		normalizeFlexTrade(&r, m)
	case "CashTransaction":
		normalizeFlexCash(&r, m)
	case "Transfer":
		normalizeFlexTransfer(&r, m)
	case "CorporateAction":
		normalizeFlexCorporate(&r, m)
	case "OptionEAE", "OptionEAETransaction":
		normalizeFlexOption(&r, m)
	default:
		r.Activity.Status = activity.StatusUnsupported
		r.Activity.Warnings = append(r.Activity.Warnings, "unsupported_record_type")
	}
	if r.Activity.TradeDate == "" && r.Activity.OccurredAt == "" {
		markHistoryReview(&r.Activity, "activity_date_missing")
	}
	return r
}
func normalizeFlexTrade(r *activity.RawRecord, m map[string]string) {
	a := &r.Activity
	a.Type = "trade"
	a.Subtype = m["transactionType"]
	execution := m["ibExecID"]
	if execution != "" {
		r.Identities = append(r.Identities, activity.Identity{Namespace: "ibkr.execution", Kind: "execution", ExternalID: execution})
		if r.Key == "" {
			r.Key = execution
		}
	}
	tradeID := m["tradeID"]
	if tradeID != "" {
		r.Identities = append(r.Identities, activity.Identity{Namespace: "ibkr.trade", Kind: "trade", ExternalID: tradeID})
		if r.Key == "" {
			r.Key = tradeID
		}
	}
	side := strings.ToUpper(m["buySell"])
	qty, e := activity.Abs(m["quantity"])
	if e != nil {
		markHistoryUnsupported(a, "invalid_quantity")
		return
	}
	signed := qty
	if side == "SELL" {
		signed, _ = activity.Negate(qty)
	} else if side != "BUY" {
		markHistoryUnsupported(a, "unknown_trade_side")
		return
	}
	currency := strings.ToUpper(strings.TrimSpace(m["currency"]))
	f := &activity.Fill{ExecutionID: execution, OrderID: m["ibOrderID"], Side: strings.ToLower(side), OpenClose: m["openCloseIndicator"], Quantity: qty, Price: m["tradePrice"], PriceCurrency: currency, Multiplier: m["multiplier"], Exchange: m["exchange"], ReportedNetCash: m["netCash"], ReportedRealizedPnL: m["fifoPnlRealized"]}
	a.Fill = f
	typ := strings.ToUpper(m["assetCategory"])
	if typ == "CASH" || typ == "FX" {
		pair := strings.Split(strings.ToUpper(m["symbol"]), ".")
		if len(pair) != 2 || !activity.ValidCurrency(pair[0]) || pair[1] != currency || pair[0] == pair[1] {
			markHistoryUnsupported(a, "fx_currency_pair_missing")
			return
		}
		a.Type = "fx_conversion"
		addHistoryCash(a, pair[0], signed, "fx_principal")
	} else {
		addHistoryPosition(a, m, signed, "execution")
	}
	// Futures executions change contracts but not cash by notional. Their daily
	// variation margin is an independent cash settlement record.
	if typ == "FUT" {
		a.Warnings = append(a.Warnings, "futures_settlement_requires_cash_records")
	} else if m["proceeds"] != "" {
		addHistoryCash(a, currency, m["proceeds"], "principal")
	} else {
		markHistoryReview(a, "trade_proceeds_missing")
	}
	if m["ibCommission"] != "" {
		commission, e := activity.CanonicalDecimal(m["ibCommission"])
		if e != nil {
			markHistoryUnsupported(a, "invalid_commission")
			return
		}
		if commission != "0" && m["ibCommissionCurrency"] == "" {
			markHistoryReview(a, "commission_currency_missing")
		} else if commission != "0" {
			addHistoryCash(a, m["ibCommissionCurrency"], commission, "commission")
		}
	} else {
		markHistoryReview(a, "commission_unknown")
	}
	if m["taxes"] != "" {
		addHistoryCash(a, currency, m["taxes"], "tax")
	}
	transactionType := strings.ToUpper(m["transactionType"])
	if transactionType == "TRADECANCEL" || transactionType == "CANCEL" || transactionType == "CANCELLED" || transactionType == "CANCELED" {
		orig := m["origTradeID"]
		if orig == "" {
			markHistoryReview(a, "cancellation_original_identity_missing")
		} else {
			r.Identities = append(r.Identities, activity.Identity{Namespace: "ibkr.trade", Kind: "trade", ExternalID: orig})
			a.Status = activity.StatusVoided
			a.Legs = nil
		}
	} else if m["origTradeID"] != "" && m["origTradeID"] != tradeID {
		// Original ID explicitly identifies the correction target. Both old and new
		// provider aliases must be attached to the same immutable activity lineage.
		r.Identities = append(r.Identities, activity.Identity{Namespace: "ibkr.trade", Kind: "trade", ExternalID: m["origTradeID"]})
	}
	if m["netCash"] != "" && a.Status == activity.StatusEffective && typ != "FUT" {
		effects, e := activity.CashEffects(a.Legs)
		if e == nil {
			if value := effects[currency]; value != "" {
				if cmp, e := activity.Compare(value, m["netCash"]); e != nil || cmp != 0 {
					markHistoryReview(a, "reported_net_cash_mismatch")
				}
			}
		}
	}
}
func normalizeFlexCash(r *activity.RawRecord, m map[string]string) {
	a := &r.Activity
	kind := strings.ToLower(strings.TrimSpace(m["type"]))
	component := "cash"
	switch kind {
	case "dividends", "dividend", "payment in lieu of dividends":
		a.Type = "dividend"
		component = "dividend"
	case "broker interest received", "broker interest paid", "interest", "bond interest received", "bond interest paid":
		a.Type = "interest"
		component = "interest"
	case "withholding tax", "tax":
		a.Type = "tax"
		component = "tax"
	case "deposits/withdrawals", "deposits & withdrawals", "deposit", "withdrawal":
		cmp, e := activity.Compare(m["amount"], "0")
		if e != nil {
			markHistoryUnsupported(a, "invalid_cash_amount")
			return
		}
		a.Type = "deposit"
		if cmp < 0 {
			a.Type = "withdrawal"
		}
	case "other fees", "advisor fees", "client fees", "fee", "fees":
		a.Type = "fee"
		component = "fee"
	case "commissions", "commission":
		a.Type = "fee"
		component = "commission"
		markHistoryReview(a, "potential_trade_fee_overlap")
	case "refund", "tax refund":
		a.Type = "refund"
		component = "refund"
	case "securities lending income", "securities lending interest":
		a.Type = "lending_income"
		component = "lending_income"
	case "internal transfer", "cash transfer":
		a.Type = "cash_transfer"
		component = "transfer"
	case "futures settlement", "futures variation margin":
		a.Type = "variation_margin"
		component = "variation_margin"
	case "return of capital":
		a.Type = "return_of_capital"
		component = "return_of_capital"
		markHistoryReview(a, "cost_basis_adjustment_missing")
	default:
		markHistoryUnsupported(a, "unsupported_cash_type")
		return
	}
	addHistoryCash(a, m["currency"], m["amount"], component)
	if len(a.Legs) > 0 {
		a.Legs[len(a.Legs)-1].Symbol = m["symbol"]
		a.Legs[len(a.Legs)-1].ExternalInstrumentID = m["conid"]
		a.Legs[len(a.Legs)-1].AssetType = snapshotAssetType(m["assetCategory"])
	}
}
func normalizeFlexTransfer(r *activity.RawRecord, m map[string]string) {
	a := &r.Activity
	a.Type = "security_transfer"
	a.Subtype = m["type"]
	direction := strings.ToUpper(m["direction"])
	if direction != "IN" && direction != "OUT" {
		markHistoryUnsupported(a, "transfer_direction_missing")
		return
	}
	qty, e := activity.Abs(m["quantity"])
	if e != nil {
		markHistoryUnsupported(a, "transfer_quantity_missing")
		return
	}
	if direction == "OUT" {
		qty, _ = activity.Negate(qty)
	}
	if strings.EqualFold(m["assetCategory"], "CASH") {
		a.Type = "cash_transfer"
		addHistoryCash(a, m["currency"], qty, "transfer")
	} else {
		addHistoryPosition(a, m, qty, "transfer")
		a.Warnings = append(a.Warnings, "transferred_cost_basis_unknown")
	}
	// positionAmount is a market value, never a second cash movement or cost basis.
}
func normalizeFlexCorporate(r *activity.RawRecord, m map[string]string) {
	a := &r.Activity
	switch strings.ToUpper(m["type"]) {
	case "FS", "FI", "RS", "CS":
		a.Type = "split"
	case "TC":
		a.Type = "merger"
	case "SO", "CO":
		a.Type = "spinoff"
	default:
		a.Type = "corporate_action"
		markHistoryUnsupported(a, "unsupported_corporate_action")
		return
	}
	if m["quantity"] == "" {
		markHistoryUnsupported(a, "corporate_quantity_missing")
		return
	}
	addHistoryPosition(a, m, m["quantity"], "corporate_action")
	// Proceeds, when explicitly present, are cash. Amount/value are valuation
	// fields and must not be synthesized into cash.
	if m["proceeds"] != "" {
		addHistoryCash(a, m["currency"], m["proceeds"], "corporate_proceeds")
	}
	if a.Type == "merger" || a.Type == "spinoff" {
		markHistoryReview(a, "corporate_cost_allocation_missing")
	}
}
func normalizeFlexOption(r *activity.RawRecord, m map[string]string) {
	a := &r.Activity
	k := strings.ToLower(m["transactionType"])
	switch k {
	case "expiration":
		a.Type = "expiration"
	case "exercise":
		a.Type = "exercise"
	case "assignment":
		a.Type = "assignment"
	default:
		markHistoryUnsupported(a, "unsupported_option_activity")
		return
	}
	if id := m["tradeID"]; id != "" {
		r.Identities = append(r.Identities, activity.Identity{Namespace: "ibkr.trade", Kind: "trade", ExternalID: id})
		if r.Key == "" {
			r.Key = id
		}
	}
	// Exercise and assignment rows may have separate option and underlying rows
	// and can overlap Trades. Expiration itself is a terminal quantity change, so
	// its explicitly reported quantity/cash can stand alone.
	if k != "expiration" {
		markHistoryReview(a, "option_execution_linkage_required")
	}
	if m["quantity"] != "" {
		addHistoryPosition(a, m, m["quantity"], "option_lifecycle")
	}
	if m["proceeds"] != "" {
		addHistoryCash(a, m["currency"], m["proceeds"], "principal")
	}
	if m["commissionsAndTax"] != "" {
		addHistoryCash(a, m["currency"], m["commissionsAndTax"], "commission_tax")
	}
}
func addHistoryCash(a *activity.Activity, currency, value, component string) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	n, e := activity.CanonicalDecimal(value)
	if e != nil || !activity.ValidCurrency(currency) {
		markHistoryReview(a, "invalid_cash_amount_or_currency")
		return
	}
	if n == "0" {
		return
	}
	a.Legs = append(a.Legs, activity.Leg{Kind: "cash", Component: component, Currency: currency, CashDelta: n, EffectiveDate: a.TradeDate, SettlementDate: a.SettlementDate})
}
func addHistoryPosition(a *activity.Activity, m map[string]string, value, component string) {
	n, e := activity.CanonicalDecimal(value)
	if e != nil || (m["conid"] == "" && strings.TrimSpace(m["symbol"]) == "") {
		markHistoryReview(a, "invalid_quantity_or_instrument")
		return
	}
	if n == "0" {
		return
	}
	a.Legs = append(a.Legs, activity.Leg{Kind: "position", Component: component, Symbol: m["symbol"], AssetType: snapshotAssetType(m["assetCategory"]), ExternalInstrumentID: m["conid"], Currency: strings.ToUpper(strings.TrimSpace(m["currency"])), QuantityDelta: n, EffectiveDate: a.TradeDate, SettlementDate: a.SettlementDate})
}
func markHistoryReview(a *activity.Activity, warning string) {
	if a.Status != activity.StatusUnsupported && a.Status != activity.StatusVoided {
		a.Status = activity.StatusNeedsReview
	}
	a.Warnings = append(a.Warnings, warning)
}
func markHistoryUnsupported(a *activity.Activity, warning string) {
	a.Status = activity.StatusUnsupported
	a.Warnings = append(a.Warnings, warning)
}
func finishHistoryRecord(r *activity.RawRecord) {
	if r.Activity.ProviderAccountID == "" {
		markHistoryUnsupported(&r.Activity, "account_identity_missing")
	}
	if len(r.Activity.Legs) == 0 && r.Activity.Status == activity.StatusEffective {
		markHistoryReview(&r.Activity, "no_explicit_effects")
	}
	r.Activity.Sources = []activity.Source{{Source: r.Source, Namespace: r.Namespace, Key: r.Key, Role: "principal", MatchMethod: "provider_identity"}}
	if e := activity.Validate(&r.Activity); e != nil {
		markHistoryUnsupported(&r.Activity, "invalid_activity_fields")
		r.Activity.Fill = nil
		r.Activity.Legs = nil
		r.Activity.CashEffectsByCurrency = map[string]string{}
	}
	if r.Activity.Warnings == nil {
		r.Activity.Warnings = []string{}
	}
	if r.Activity.Legs == nil {
		r.Activity.Legs = []activity.Leg{}
	}
}
func firstHistory(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if m[k] != "" {
			return m[k]
		}
	}
	return ""
}
func historyTimestamp(s string) string {
	if t, e := time.Parse(time.RFC3339Nano, s); e == nil {
		return t.UTC().Format(time.RFC3339Nano)
	}
	return ""
}
