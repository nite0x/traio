package indicator

// Series is a time-aligned float slice matching bar closes.
type Series struct {
	Values []float64 `json:"values"`
}

// Calculator wraps technical indicator computation (go-talib in phase 3).
type Calculator struct{}

func New() *Calculator {
	return &Calculator{}
}

// Supported indicator names for API validation.
var Supported = []string{
	"SMA", "EMA", "MACD", "RSI", "BBANDS", "ATR", "STOCH",
}
