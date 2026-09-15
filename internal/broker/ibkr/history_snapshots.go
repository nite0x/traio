package ibkr

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"strings"

	"github.com/nite/traio/internal/activity"
)

// ParseReconciliationSnapshots extracts only explicit Flex closing balances.
// It uses the same bounded, entity-free validation as the activity importer.
func ParseReconciliationSnapshots(body []byte) ([]activity.ReconciliationSnapshot, error) {
	if _, err := ParseActivityXML(body); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(body)
	documentRef := "ibkr-flex:" + hex.EncodeToString(hash[:])
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true
	result := []activity.ReconciliationSnapshot{}
	current := -1
	depth, statementDepth := 0, 0
	hasCash, hasPositions, valid := false, false, false
	hasLots, hasSummary := false, false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("invalid activity XML")
		}
		switch node := token.(type) {
		case xml.Directive:
			return nil, errors.New("XML directives and external entities are not supported")
		case xml.StartElement:
			depth++
			attrs := snapshotAttributes(node.Attr)
			switch node.Name.Local {
			case "FlexStatement":
				result = append(result, activity.ReconciliationSnapshot{ProviderAccountID: attrs["accountId"], AsOfDate: activity.NormalizeDate(attrs["toDate"]), Basis: "trade_date", SourceRef: documentRef, Completeness: "partial", Balances: []activity.ReconciliationBalance{}})
				current, statementDepth, hasCash, hasPositions = len(result)-1, depth, false, false
				hasLots, hasSummary = false, false
				valid = result[current].ProviderAccountID != "" && result[current].AsOfDate != ""
			case "CashReport":
				if current >= 0 && depth == statementDepth+1 {
					hasCash = true
				}
			case "OpenPositions":
				if current >= 0 && depth == statementDepth+1 {
					hasPositions = true
				}
			case "CashReportCurrency":
				if current >= 0 {
					currency := strings.ToUpper(attrs["currency"])
					value := attrs["endingCash"]
					// A settled-cash value cannot substitute for trade-date cash.
					if currency == "BASE_SUMMARY" || currency == "BASE" {
						continue
					}
					if currency == "" || value == "" {
						valid = false
						continue
					}
					value, err = activity.CanonicalDecimal(value)
					if err != nil {
						return nil, errors.New("invalid reconciliation cash balance")
					}
					result[current].Balances = append(result[current].Balances, activity.ReconciliationBalance{Balance: activity.Balance{Kind: "cash", Currency: currency, Value: value}})
				}
			case "OpenPosition":
				if current >= 0 {
					if strings.EqualFold(attrs["levelOfDetail"], "LOT") {
						hasLots = true
						continue
					}
					if level := strings.ToUpper(attrs["levelOfDetail"]); level != "" && level != "SUMMARY" {
						valid = false
						continue
					}
					quantity, err := activity.CanonicalDecimal(attrs["position"])
					hasSummary = true
					if err != nil {
						return nil, errors.New("invalid reconciliation position balance")
					}
					currency := strings.ToUpper(attrs["currency"])
					assetType := snapshotAssetType(attrs["assetCategory"])
					if attrs["conid"] == "" || attrs["symbol"] == "" || assetType == "" || (currency != "" && !activity.ValidCurrency(currency)) {
						valid = false
						continue
					}
					cost := attrs["costBasisMoney"]
					if cost != "" {
						cost, err = activity.CanonicalDecimal(cost)
						if err != nil {
							return nil, errors.New("invalid reconciliation reported cost")
						}
					}
					result[current].Balances = append(result[current].Balances, activity.ReconciliationBalance{Balance: activity.Balance{Kind: "position", Currency: currency, Value: quantity}, ExternalInstrumentID: attrs["conid"], Symbol: attrs["symbol"], AssetType: assetType, ReportedCost: cost})
				}
			}
		case xml.EndElement:
			if node.Name.Local == "FlexStatement" && current >= 0 {
				if hasLots && !hasSummary {
					valid = false
				}
				if !hasCash && !hasPositions {
					result = result[:current]
				} else if hasCash && hasPositions && valid {
					result[current].Completeness = "complete"
				}
				current = -1
			}
			depth--
		}
	}
	return result, nil
}

func snapshotAssetType(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "STK":
		return "stock"
	case "OPT":
		return "option"
	case "FUT":
		return "future"
	case "BOND":
		return "bond"
	case "FUND":
		return "mutual_fund"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func snapshotAttributes(attributes []xml.Attr) map[string]string {
	out := map[string]string{}
	for _, attribute := range attributes {
		if !sensitiveActivityField(attribute.Name.Local) {
			out[attribute.Name.Local] = strings.TrimSpace(attribute.Value)
		}
	}
	return out
}
