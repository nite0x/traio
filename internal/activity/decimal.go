package activity

import (
	"fmt"
	"math/big"
	"strings"
)

// CanonicalDecimal accepts plain decimal notation exactly representable by
// NUMERIC(38,18): at most 20 integer and 18 fractional digits. No rounding,
// binary floating point, exponent notation or thousands separators are used.
func CanonicalDecimal(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 80 {
		return "", fmt.Errorf("invalid decimal")
	}
	negative := s[0] == '-'
	if s[0] == '-' || s[0] == '+' {
		s = s[1:]
	}
	pieces := strings.Split(s, ".")
	if len(pieces) > 2 || s == "" {
		return "", fmt.Errorf("invalid decimal")
	}
	whole := pieces[0]
	frac := ""
	if len(pieces) == 2 {
		frac = pieces[1]
	}
	if whole == "" && frac == "" {
		return "", fmt.Errorf("invalid decimal")
	}
	for _, c := range whole + frac {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("invalid decimal")
		}
	}
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	frac = strings.TrimRight(frac, "0")
	if len(whole) > 20 || len(frac) > 18 {
		return "", fmt.Errorf("decimal exceeds NUMERIC(38,18)")
	}
	out := whole
	if frac != "" {
		out += "." + frac
	}
	if negative && out != "0" {
		out = "-" + out
	}
	return out, nil
}
func rational(s string) (*big.Rat, error) {
	n, e := CanonicalDecimal(s)
	if e != nil {
		return nil, e
	}
	r, ok := new(big.Rat).SetString(n)
	if !ok {
		return nil, fmt.Errorf("invalid decimal")
	}
	return r, nil
}
func exact(r *big.Rat) (string, error) {
	s := r.FloatString(18)
	q, ok := new(big.Rat).SetString(s)
	if !ok || q.Cmp(r) != 0 {
		return "", fmt.Errorf("decimal operation exceeds precision")
	}
	return CanonicalDecimal(s)
}
func Add(a, b string) (string, error) {
	x, e := rational(a)
	if e != nil {
		return "", e
	}
	y, e := rational(b)
	if e != nil {
		return "", e
	}
	return exact(x.Add(x, y))
}
func Sub(a, b string) (string, error) {
	x, e := rational(a)
	if e != nil {
		return "", e
	}
	y, e := rational(b)
	if e != nil {
		return "", e
	}
	return exact(x.Sub(x, y))
}
func Mul(a, b string) (string, error) {
	x, e := rational(a)
	if e != nil {
		return "", e
	}
	y, e := rational(b)
	if e != nil {
		return "", e
	}
	return exact(x.Mul(x, y))
}
func Compare(a, b string) (int, error) {
	x, e := rational(a)
	if e != nil {
		return 0, e
	}
	y, e := rational(b)
	if e != nil {
		return 0, e
	}
	return x.Cmp(y), nil
}
func Negate(a string) (string, error) { return Sub("0", a) }
func Abs(a string) (string, error)    { s, e := CanonicalDecimal(a); return strings.TrimPrefix(s, "-"), e }
