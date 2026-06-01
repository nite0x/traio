package ibkr

import (
	"net/url"
	"testing"
)

func TestApplyFlexPeriodSetsLastNDays(t *testing.T) {
	q := url.Values{}
	applyFlexPeriod(q, flexPeriod{days: flexEquityPeriodDays})
	if got := q.Get("p"); got != "365" {
		t.Fatalf("expected p=365, got %q", got)
	}

	q = url.Values{}
	applyFlexPeriod(q, flexPeriod{days: 0})
	if q.Get("p") != "" {
		t.Fatalf("expected no p for zero days, got %q", q.Get("p"))
	}

	q = url.Values{}
	applyFlexPeriod(q, flexPeriod{days: 400})
	if q.Get("p") != "" {
		t.Fatalf("expected no p for >365 days, got %q", q.Get("p"))
	}
}

func TestParseFlexEquityUsesNetAssetValueRows(t *testing.T) {
	body := []byte(`
<FlexStatementResponse>
  <FlexStatements>
    <FlexStatement>
      <NetAssetValue reportDate="20260528" currency="USD" endingValue="12345.67" />
      <CashReport reportDate="20260528" currency="USD" total="999.99" />
      <NetAssetValue reportDate="20260529" currency="USD" total="12400.00" />
    </FlexStatement>
  </FlexStatements>
</FlexStatementResponse>`)

	points := parseFlexEquity(body)
	if len(points) != 2 {
		t.Fatalf("expected 2 equity points, got %d", len(points))
	}
	if points[0].Time != "2026-05-28T00:00:00Z" || points[0].Value != 12345.67 {
		t.Fatalf("unexpected first point: %+v", points[0])
	}
	if points[1].Time != "2026-05-29T00:00:00Z" || points[1].Value != 12400 {
		t.Fatalf("unexpected second point: %+v", points[1])
	}
}

func TestParseFlexEquityCSVMNetAssetValue(t *testing.T) {
	body := []byte(`"BOF","U15772871","Activity","1","20250530","20260529","20260530;060712","100","100"
"BOA","U15772871",20260528,20260528
"BOS","NAV","Net Asset Value in Base"
"ClientAccountID","CurrencyPrimary","Total","ReportDate"
"U15772871","USD","12345.67","20260528"
"EOS","NAV","1","0"
"BOA","U15772871",20260529,20260529
"BOS","NAV","Net Asset Value in Base"
"ClientAccountID","CurrencyPrimary","Total","ReportDate"
"U15772871","USD","12400.00","20260529"
"EOS","NAV","1","0"
`)

	points := parseFlexEquity(body)
	if len(points) != 2 {
		t.Fatalf("expected 2 equity points, got %d", len(points))
	}
	if points[0].Value != 12345.67 || points[1].Value != 12400 {
		t.Fatalf("unexpected values: %+v", points)
	}
}

func TestParseFlexEquityCSVSkipsChangeInPositionValues(t *testing.T) {
	body := []byte(`"BOA","U15772871",20250530,20250530
"BOS","CPOV","Change in Position Values"
"U15772871","BASE_SUMMARY","52.44"
"EOS","CPOV","1","0"
`)

	points := parseFlexEquity(body)
	if len(points) != 0 {
		t.Fatalf("expected 0 equity points for CPOV CSV, got %d: %+v", len(points), points)
	}
}
