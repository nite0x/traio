package ibkr

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nite/traio/internal/activity"
	"github.com/nite/traio/internal/broker"
)

// FlexSession deliberately exposes no Gateway, market-data or trading methods.
type FlexSession struct {
	id        int64
	client    *Client
	mu        sync.Mutex
	fetched   time.Time
	snapshots []broker.AccountSnapshot
}

func (s *FlexSession) ConnectionID() int64       { return s.id }
func (*FlexSession) ProviderCode() string        { return "IBKR" }
func (*FlexSession) Close(context.Context) error { return nil }
func (s *FlexSession) Health(ctx context.Context) (broker.ConnectionHealth, error) {
	_, err := s.ListAccountSnapshots(ctx)
	return broker.ConnectionHealthFromAuthentication(broker.LoginAction{Authenticated: err == nil}, err)
}
func (s *FlexSession) AuthenticationStatus(ctx context.Context) (broker.LoginAction, error) {
	_, err := s.ListAccountSnapshots(ctx)
	return broker.LoginAction{Authenticated: err == nil}, err
}
func (s *FlexSession) BeginAuthentication(ctx context.Context, _ broker.AuthenticationRequest) (broker.LoginAction, error) {
	return s.AuthenticationStatus(ctx)
}
func (s *FlexSession) ListAccountSnapshots(ctx context.Context) ([]broker.AccountSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Portfolio polling is frequent; reports are end-of-day and expensive to generate.
	if !s.fetched.IsZero() && time.Since(s.fetched) < 15*time.Minute {
		return s.snapshots, nil
	}
	query := s.client.cfg.FlexActivityQueryID
	if query == "" {
		query = s.client.cfg.FlexQueryID
	}
	ref, err := s.client.flexSendRequest(ctx, s.client.cfg.FlexToken, query, flexPeriod{})
	if err != nil {
		return nil, err
	}
	body, err := s.client.flexGetStatement(ctx, s.client.cfg.FlexToken, ref, flexPeriod{})
	if err != nil {
		return nil, err
	}
	snapshots, err := ParseFlexPortfolio(body)
	if err != nil {
		return nil, err
	}
	s.snapshots, s.fetched = snapshots, time.Now()
	return snapshots, nil
}

// ParseFlexPortfolio uses explicit closing positions only. Missing sections are
// errors for that resource, never an instruction to clear its last snapshot.
func ParseFlexPortfolio(body []byte) ([]broker.AccountSnapshot, error) {
	if _, err := ParseActivityXML(body); err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	missing := broker.ErrSnapshotResourceNotProvided
	result := []broker.AccountSnapshot{}
	var current *broker.AccountSnapshot
	var asOf string
	dates := map[string]string{}
	indexes := map[string]int{}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("invalid Flex portfolio XML")
		}
		switch node := token.(type) {
		case xml.StartElement:
			attrs := snapshotAttributes(node.Attr)
			if node.Name.Local == "FlexStatement" {
				id := strings.TrimSpace(attrs["accountId"])
				asOf = activity.NormalizeDate(attrs["toDate"])
				if id == "" || asOf == "" {
					return nil, errors.New("Flex statement requires Account ID and closing date")
				}
				current = &broker.AccountSnapshot{Account: broker.Account{ID: id, Broker: "IBKR", BaseCurrency: attrs["currency"]}, ResourceErrors: broker.AccountSnapshotErrors{Positions: missing, CashBalances: missing, DailyPerformance: missing}}
				continue
			}
			if current == nil {
				continue
			}
			number := func(key string) (float64, error) {
				raw := strings.TrimSpace(attrs[key])
				if raw == "" {
					return 0, nil
				}
				n, e := strconv.ParseFloat(raw, 64)
				if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
					return 0, errors.New("invalid Flex portfolio amount")
				}
				return n, nil
			}
			if isFlexEquityElement(node.Name.Local) {
				date := activity.NormalizeDate(firstAttr(lowerAttrs(node.Attr), "reportdate", "date", "todate"))
				if date == asOf {
					for _, key := range []string{"endingValue", "endingNAV", "endingNetAssetValue", "currentNAV", "netAssetValue", "netLiquidation", "equityWithLoanValue", "total"} {
						if attrs[key] == "" {
							continue
						}
						total, e := number(key)
						if e != nil {
							return nil, e
						}
						current.DailyPerformance = broker.DailyPerformance{AccountID: current.Account.ID, NetLiquidation: total, AsOf: asOf}
						current.ResourceErrors.DailyPerformance = nil
						if attrs["currency"] != "" {
							current.Account.BaseCurrency = attrs["currency"]
						}
						break
					}
				}
			}
			switch node.Name.Local {
			case "AccountInformation":
				current.Account.DisplayName = attrs["accountAlias"]
				current.Account.AccountType = attrs["accountType"]
				if attrs["currency"] != "" {
					current.Account.BaseCurrency = attrs["currency"]
				}
			case "OpenPositions":
				current.ResourceErrors.Positions = nil
			case "CashReport":
				current.ResourceErrors.CashBalances = nil
			case "OpenPosition":
				if level := strings.ToUpper(attrs["levelOfDetail"]); level != "" && level != "SUMMARY" {
					current.ResourceErrors.Positions = errors.New("Flex positions require SUMMARY detail")
					continue
				}
				if attrs["symbol"] == "" || attrs["position"] == "" || attrs["currency"] == "" {
					return nil, errors.New("Flex position requires symbol, quantity and currency")
				}
				p := broker.Position{Account: current.Account.ID, Broker: "IBKR", ExternalID: attrs["conid"], Symbol: attrs["symbol"], Name: attrs["description"], AssetType: snapshotAssetType(attrs["assetCategory"]), Currency: attrs["currency"], Exchange: attrs["listingExchange"], SyncedAt: asOf}
				if p.ExternalID != "" {
					p.ConID, err = strconv.ParseInt(p.ExternalID, 10, 64)
					if err != nil || p.ConID <= 0 {
						return nil, errors.New("invalid Flex Conid")
					}
				}
				values := []struct {
					key    string
					target *float64
				}{{"position", &p.Quantity}, {"costBasisPrice", &p.AvgCost}, {"markPrice", &p.MarketPrice}, {"positionValue", &p.MarketValue}, {"fifoPnlUnrealized", &p.Unrealized}}
				for _, v := range values {
					*v.target, err = number(v.key)
					if err != nil {
						return nil, err
					}
				}
				current.Positions = append(current.Positions, p)
			case "CashReportCurrency":
				currency := strings.ToUpper(attrs["currency"])
				if currency == "BASE_SUMMARY" || currency == "BASE" {
					continue
				}
				if currency == "" || attrs["endingCash"] == "" {
					current.ResourceErrors.CashBalances = missing
					continue
				}
				total, e := number("endingCash")
				if e != nil {
					return nil, e
				}
				settled, e := number("endingSettledCash")
				if e != nil {
					return nil, e
				}
				current.CashBalances = append(current.CashBalances, broker.CashBalance{AccountID: current.Account.ID, Currency: currency, Total: total, Settled: settled, AsOf: asOf})
			case "EquitySummaryByReportDateInBase":
				reportDate := activity.NormalizeDate(attrs["reportDate"])
				if reportDate != "" && reportDate != asOf {
					continue
				}
				if attrs["total"] == "" {
					continue
				}
				total, e := number("total")
				if e != nil {
					return nil, e
				}
				stocks, e := number("stock")
				if e != nil {
					return nil, e
				}
				current.DailyPerformance = broker.DailyPerformance{AccountID: current.Account.ID, NetLiquidation: total, MarketValue: stocks, AsOf: asOf}
				current.ResourceErrors.DailyPerformance = nil
			}
		case xml.EndElement:
			if node.Name.Local == "FlexStatement" && current != nil {
				id := current.Account.ID
				if previous, ok := indexes[id]; !ok {
					indexes[id] = len(result)
					dates[id] = asOf
					result = append(result, *current)
				} else if asOf >= dates[id] {
					result[previous] = *current
					dates[id] = asOf
				}
				current = nil
			}
		}
	}
	if len(result) == 0 {
		return nil, errors.New("Flex Query must return XML with Account ID; enable Account Information and Open Positions")
	}
	return result, nil
}

func (s *FlexSession) HistoricalEquity(ctx context.Context) ([]broker.AccountEquityPoint, error) {
	return s.client.HistoricalEquity(ctx)
}
func (s *FlexSession) AccountSummary(ctx context.Context) (broker.AccountSummary, error) {
	snapshots, err := s.ListAccountSnapshots(ctx)
	if err != nil {
		return broker.AccountSummary{}, err
	}
	if len(snapshots) != 1 {
		return broker.AccountSummary{}, errors.New("Flex account summary requires a single account")
	}
	snapshot := snapshots[0]
	if snapshot.ResourceErrors.DailyPerformance != nil {
		return broker.AccountSummary{}, snapshot.ResourceErrors.DailyPerformance
	}
	return broker.AccountSummary{AccountID: snapshot.Account.ID, Broker: "IBKR", Currency: snapshot.Account.BaseCurrency, NetLiquidation: snapshot.DailyPerformance.NetLiquidation, AsOf: snapshot.DailyPerformance.AsOf}, nil
}
