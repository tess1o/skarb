package money

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Currency describes a supported ISO 4217 currency.
type Currency struct {
	Code       string
	Symbol     string
	MinorUnits int
}

var currencies = map[string]Currency{
	"UAH": {Code: "UAH", Symbol: "₴", MinorUnits: 2},
	"USD": {Code: "USD", Symbol: "$", MinorUnits: 2},
	"EUR": {Code: "EUR", Symbol: "€", MinorUnits: 2},
	"PLN": {Code: "PLN", Symbol: "zł", MinorUnits: 2},
	"GBP": {Code: "GBP", Symbol: "£", MinorUnits: 2},
	"CHF": {Code: "CHF", Symbol: "CHF", MinorUnits: 2},
	"CZK": {Code: "CZK", Symbol: "Kč", MinorUnits: 2},
	"HUF": {Code: "HUF", Symbol: "Ft", MinorUnits: 2},
	"CAD": {Code: "CAD", Symbol: "CA$", MinorUnits: 2},
	"AUD": {Code: "AUD", Symbol: "A$", MinorUnits: 2},
	"JPY": {Code: "JPY", Symbol: "¥", MinorUnits: 0},
	"CNY": {Code: "CNY", Symbol: "¥", MinorUnits: 2},
	"TRY": {Code: "TRY", Symbol: "₺", MinorUnits: 2},
	"ILS": {Code: "ILS", Symbol: "₪", MinorUnits: 2},
}

const RateScale = 8

func CurrencyFor(code string) (Currency, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		code = "UAH"
	}
	c, ok := currencies[code]
	if !ok {
		return Currency{}, fmt.Errorf("unsupported currency %q", code)
	}
	return c, nil
}

func IsSupported(code string) bool {
	_, err := CurrencyFor(code)
	return err == nil
}

func SupportedCurrencies() []string {
	out := make([]string, 0, len(currencies))
	for code := range currencies {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

func MinorUnits(code string) int {
	c, err := CurrencyFor(code)
	if err != nil {
		return 2
	}
	return c.MinorUnits
}

// ParseAmount parses a positive amount in the given currency into minor units.
func ParseAmount(s, currency string) (int64, error) {
	c, err := CurrencyFor(currency)
	if err != nil {
		return 0, err
	}
	minor, err := parseScaled(s, c.MinorUnits)
	if err != nil {
		return 0, err
	}
	if minor <= 0 {
		return 0, fmt.Errorf("amount must be greater than zero")
	}
	return minor, nil
}

// ParseBalance parses a non-negative balance in the given currency into minor units.
func ParseBalance(s, currency string) (int64, error) {
	c, err := CurrencyFor(currency)
	if err != nil {
		return 0, err
	}
	minor, err := parseScaled(s, c.MinorUnits)
	if err != nil {
		return 0, err
	}
	if minor < 0 {
		return 0, fmt.Errorf("balance cannot be negative")
	}
	return minor, nil
}

// ParseRate parses UAH per one foreign unit into an 8-decimal scaled integer.
func ParseRate(s string) (int64, error) {
	scaled, err := parseScaled(s, RateScale)
	if err != nil {
		return 0, err
	}
	if scaled <= 0 {
		return 0, fmt.Errorf("rate must be greater than zero")
	}
	return scaled, nil
}

// FormatRate formats a scaled rate with up to 8 decimal places.
func FormatRate(scaled int64) string {
	return formatScaled(scaled, RateScale)
}

// ConvertToUAH converts a foreign amount in minor units to UAH minor units.
// rateScaled is UAH per one foreign unit scaled by 10^RateScale.
func ConvertToUAH(amountMinor int64, currency string, rateScaled int64) (int64, error) {
	c, err := CurrencyFor(currency)
	if err != nil {
		return 0, err
	}
	if amountMinor < 0 {
		return 0, fmt.Errorf("negative amounts are not supported")
	}
	if rateScaled <= 0 {
		return 0, fmt.Errorf("rate must be greater than zero")
	}
	if c.Code == "UAH" {
		return amountMinor, nil
	}

	exp := RateScale + c.MinorUnits - 2
	if exp < 0 {
		exp = 0
	}
	num := new(big.Int).Mul(big.NewInt(amountMinor), big.NewInt(rateScaled))
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exp)), nil)
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	if new(big.Int).Lsh(r, 1).Cmp(den) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, fmt.Errorf("converted amount overflows int64")
	}
	return q.Int64(), nil
}

// ConvertFromUAH converts UAH minor units to another currency's minor units.
func ConvertFromUAH(amountMinor int64, currency string, rateScaled int64) (int64, error) {
	c, err := CurrencyFor(currency)
	if err != nil {
		return 0, err
	}
	if amountMinor < 0 {
		return 0, fmt.Errorf("negative amounts are not supported")
	}
	if rateScaled <= 0 {
		return 0, fmt.Errorf("rate must be greater than zero")
	}
	if c.Code == "UAH" {
		return amountMinor, nil
	}

	exp := RateScale + c.MinorUnits - 2
	num := new(big.Int).Mul(big.NewInt(amountMinor), new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exp)), nil))
	den := big.NewInt(rateScaled)
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	if new(big.Int).Lsh(r, 1).Cmp(den) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, fmt.Errorf("converted amount overflows int64")
	}
	return q.Int64(), nil
}

// FormatAmount renders minor units in Ukrainian style, e.g. 1 234,56 ₴ or 1 234,56 USD.
func FormatAmount(minor int64, currency string) string {
	c, err := CurrencyFor(currency)
	if err != nil {
		c = Currency{Code: strings.ToUpper(currency), MinorUnits: 2}
	}
	s := formatScaled(minor, c.MinorUnits)
	if c.Code == "UAH" {
		return s + " ₴"
	}
	return s + " " + c.Code
}

// FormatScaled renders a scaled integer with a decimal comma and NBSP thousands separators.
func FormatScaled(value int64, scale int) string {
	return formatScaled(value, scale)
}

// FormatMinor is an alias for FormatScaled for money values.
func FormatMinor(value int64, scale int) string {
	return formatScaled(value, scale)
}

func formatScaled(value int64, scale int) string {
	if scale < 0 {
		scale = 0
	}
	neg := value < 0
	var abs *big.Int
	if neg {
		abs = new(big.Int).Abs(big.NewInt(value))
	} else {
		abs = big.NewInt(value)
	}
	s := abs.String()
	if scale > 0 {
		if len(s) <= scale {
			s = strings.Repeat("0", scale-len(s)+1) + s
		}
		intPart := s[:len(s)-scale]
		fracPart := s[len(s)-scale:]
		fracPart = strings.TrimRight(fracPart, "0")
		if fracPart != "" {
			s = intPart + "," + fracPart
		} else {
			s = intPart
		}
	}
	s = groupThousands(s)
	if neg {
		return "−" + s
	}
	return s
}

func groupThousands(s string) string {
	parts := strings.SplitN(s, ",", 2)
	intPart := parts[0]
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	if len(parts) == 2 {
		b.WriteRune(',')
		b.WriteString(parts[1])
	}
	return b.String()
}

// parseScaled parses a non-negative decimal into a scaled integer, rounding half up.
func parseScaled(raw string, scale int) (int64, error) {
	if scale < 0 {
		return 0, fmt.Errorf("invalid scale")
	}
	s, err := normalizeNumber(raw, scale)
	if err != nil {
		return 0, err
	}
	intPart := "0"
	fracPart := ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart = s[:i]
		fracPart = s[i+1:]
	} else {
		intPart = s
	}
	if intPart == "" {
		intPart = "0"
	}
	digits := intPart
	roundUp := false
	if len(fracPart) > scale {
		if fracPart[scale] >= '5' {
			roundUp = true
		}
		fracPart = fracPart[:scale]
	}
	if len(fracPart) < scale {
		fracPart += strings.Repeat("0", scale-len(fracPart))
	}
	digits += fracPart
	n := new(big.Int)
	if _, ok := n.SetString(digits, 10); !ok {
		return 0, fmt.Errorf("invalid amount %q", raw)
	}
	if roundUp {
		n.Add(n, big.NewInt(1))
	}
	if !n.IsInt64() {
		return 0, fmt.Errorf("amount %q is too large", raw)
	}
	return n.Int64(), nil
}

func normalizeNumber(raw string, scale int) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("empty amount")
	}
	// Remove thousands separators and currency decoration around the number.
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r), r == ' ', r == ' ', r == '\'':
			return -1
		default:
			return r
		}
	}, s)
	s = strings.TrimPrefix(s, "+")
	if strings.ContainsAny(s, "-−") {
		return "", fmt.Errorf("negative amounts are not supported")
	}
	// Keep only the numeric span so "20 USD", "$20", and "45.2021 UAH" work.
	first, last := -1, -1
	for i, r := range s {
		if r >= '0' && r <= '9' {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 || last < first {
		return "", fmt.Errorf("invalid amount %q", raw)
	}
	s = s[first : last+1]
	if s == "" {
		return "", fmt.Errorf("empty amount")
	}

	dots := strings.Count(s, ".")
	commas := strings.Count(s, ",")
	if dots+commas == 0 {
		if !allDigits(s) {
			return "", fmt.Errorf("invalid amount %q", raw)
		}
		return s, nil
	}

	lastDot := strings.LastIndexByte(s, '.')
	lastComma := strings.LastIndexByte(s, ',')
	var decimalSep byte
	if lastDot >= 0 && lastComma >= 0 {
		if lastDot > lastComma {
			decimalSep = '.'
		} else {
			decimalSep = ','
		}
	} else if lastDot >= 0 {
		decimalSep = '.'
	} else {
		decimalSep = ','
	}

	// Build integer and fraction around the decimal separator, stripping the other separator.
	var intPart, fracPart strings.Builder
	foundDecimal := false
	for _, r := range s {
		switch r {
		case '.', ',':
			if byte(r) == decimalSep && !foundDecimal {
				foundDecimal = true
				continue
			}
			// A repeated decimal separator or the other separator is thousands punctuation.
			continue
		default:
			if foundDecimal {
				fracPart.WriteRune(r)
			} else {
				intPart.WriteRune(r)
			}
		}
	}
	if !foundDecimal {
		return "", fmt.Errorf("invalid amount %q", raw)
	}
	ip := intPart.String()
	fp := fracPart.String()
	if !allDigits(ip) || !allDigits(fp) {
		return "", fmt.Errorf("invalid amount %q", raw)
	}

	// For an amount (scale <= 2), a single separator followed by exactly three digits
	// is treated as a thousands separator, not a decimal point.
	if dots+commas == 1 && len(fp) == 3 && scale <= 2 && len(ip) >= 1 && len(ip) <= 3 {
		return ip + fp, nil
	}
	if fp == "" {
		return ip, nil
	}
	if ip == "" {
		ip = "0"
	}
	return ip + "." + fp, nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// NormalizeAmountString is exposed for parsers that want the same canonical decimal form.
func NormalizeAmountString(raw string, scale int) (string, error) {
	return normalizeNumber(raw, scale)
}

// MustParseAmount is intended for tests and static fixtures.
func MustParseAmount(s, currency string) int64 {
	v, err := ParseAmount(s, currency)
	if err != nil {
		panic(err)
	}
	return v
}

// PlainAmount renders a scaled integer with a dot decimal separator, for CSV/API output.
func PlainAmount(value int64, scale int) string {
	if scale < 0 {
		scale = 0
	}
	neg := value < 0
	abs := value
	if neg {
		abs = -abs
	}
	s := strconv.FormatInt(abs, 10)
	if scale > 0 {
		if len(s) <= scale {
			s = strings.Repeat("0", scale-len(s)+1) + s
		}
		s = s[:len(s)-scale] + "." + s[len(s)-scale:]
	}
	if neg {
		return "-" + s
	}
	return s
}

// FormatInt is a small helper for diagnostics.
func FormatInt(v int64) string { return strconv.FormatInt(v, 10) }
