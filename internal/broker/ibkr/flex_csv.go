package ibkr

import (
	"bytes"
	"encoding/csv"
	"io"
	"sort"
	"strings"

	"github.com/nite/traio/internal/broker"
)

func parseFlexEquityCSV(body []byte) []broker.AccountEquityPoint {
	reader := csv.NewReader(bytes.NewReader(body))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	pointsByTime := map[string]broker.AccountEquityPoint{}
	var (
		inEquitySection bool
		headers         []string
		sectionDate     string
	)

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(record) == 0 {
			continue
		}
		tag := strings.ToUpper(strings.TrimSpace(record[0]))

		switch tag {
		case "BOA":
			sectionDate = flexCSVBOADate(record)
		case "BOS":
			code, name := flexCSVSectionMeta(record)
			inEquitySection = isFlexCSVEquitySection(code, name)
			headers = nil
		case "EOS":
			inEquitySection = false
			headers = nil
		default:
			if !inEquitySection {
				continue
			}
			if flexCSVLooksLikeHeader(record) {
				headers = flexCSVNormalizeHeaders(record)
				continue
			}
			if point, ok := flexCSVEquityPoint(record, headers, sectionDate); ok {
				pointsByTime[point.Time] = point
			}
		}
	}

	out := make([]broker.AccountEquityPoint, 0, len(pointsByTime))
	for _, point := range pointsByTime {
		out = append(out, point)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Time < out[j].Time
	})
	return out
}

func flexCSVBOADate(record []string) string {
	if len(record) >= 4 {
		return strings.TrimSpace(record[3])
	}
	if len(record) >= 3 {
		return strings.TrimSpace(record[2])
	}
	return ""
}

func flexCSVSectionMeta(record []string) (code, name string) {
	if len(record) >= 2 {
		code = strings.TrimSpace(record[1])
	}
	if len(record) >= 3 {
		name = strings.TrimSpace(record[2])
	}
	return code, name
}

func isFlexCSVEquitySection(code, name string) bool {
	combined := strings.ToLower(strings.TrimSpace(code + " " + name))
	if strings.Contains(combined, "change in position") || strings.Contains(combined, "cpov") {
		return false
	}
	if strings.Contains(combined, "net asset value") {
		return true
	}
	if strings.Contains(combined, "change in nav") {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(code), "NAV") {
		return true
	}
	return false
}

func flexCSVLooksLikeHeader(record []string) bool {
	for _, field := range record {
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "reportdate", "total", "endingvalue", "currencyprimary", "currency", "clientaccountid":
			return true
		}
	}
	return false
}

func flexCSVNormalizeHeaders(record []string) []string {
	out := make([]string, len(record))
	for i, field := range record {
		out[i] = strings.ToLower(strings.TrimSpace(field))
	}
	return out
}

func flexCSVEquityPoint(record, headers []string, sectionDate string) (broker.AccountEquityPoint, bool) {
	date := sectionDate
	value := 0.0
	currency := ""

	if len(headers) > 0 {
		date = flexCSVField(record, headers, "reportdate", "todate", "date")
		if date == "" {
			date = sectionDate
		}
		value = flexCSVFieldFloat(record, headers,
			"total",
			"endingvalue",
			"endingnav",
			"netassetvalue",
			"netliquidation",
		)
		currency = flexCSVField(record, headers, "currencyprimary", "currency", "basecurrency")
	} else {
		value = flexCSVLooseValue(record)
	}

	if date == "" || value == 0 {
		return broker.AccountEquityPoint{}, false
	}
	normalized := normalizeFlexDate(date)
	if normalized == "" {
		return broker.AccountEquityPoint{}, false
	}
	return broker.AccountEquityPoint{
		Time:     normalized,
		Value:    value,
		Currency: currency,
		Source:   "IBKR Flex",
	}, true
}

func flexCSVField(record, headers []string, keys ...string) string {
	for _, key := range keys {
		key = strings.ToLower(key)
		for i, header := range headers {
			if header != key || i >= len(record) {
				continue
			}
			if value := strings.TrimSpace(record[i]); value != "" {
				return value
			}
		}
	}
	return ""
}

func flexCSVFieldFloat(record, headers []string, keys ...string) float64 {
	if value := flexCSVField(record, headers, keys...); value != "" {
		return parseFloat(value)
	}
	return 0
}

func flexCSVLooseValue(record []string) float64 {
	for i := len(record) - 1; i >= 0; i-- {
		if n := parseFloat(record[i]); n != 0 {
			return n
		}
	}
	return 0
}
