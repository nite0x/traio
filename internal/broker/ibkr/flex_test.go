package ibkr

import "testing"

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
