package money

import "testing"

func TestParseAndFormat(t *testing.T) {
	cases := []struct {
		raw      string
		currency string
		want     int64
	}{
		{"512,50", "UAH", 51250},
		{"1 234,56", "UAH", 123456},
		{"1,234.56", "USD", 123456},
		{"1.234", "UAH", 123400},
		{"0.5", "UAH", 50},
		{"20 USD", "USD", 2000},
		{"$20", "USD", 2000},
		{"1000", "JPY", 1000},
	}
	for _, tc := range cases {
		got, err := ParseAmount(tc.raw, tc.currency)
		if err != nil {
			t.Fatalf("ParseAmount(%q, %s): %v", tc.raw, tc.currency, err)
		}
		if got != tc.want {
			t.Fatalf("ParseAmount(%q, %s) = %d, want %d", tc.raw, tc.currency, got, tc.want)
		}
	}
	if got := FormatAmount(123456, "UAH"); got != "1 234,56 ₴" {
		t.Fatalf("FormatAmount = %q", got)
	}
	if got := FormatAmount(51200, "UAH"); got != "512 ₴" {
		t.Fatalf("FormatAmount = %q", got)
	}
}

func TestConvertToUAH(t *testing.T) {
	// 5000 USD at 45.2021 UAH/USD = 226010.50 UAH.
	got, err := ConvertToUAH(500000, "USD", 4520210000)
	if err != nil {
		t.Fatal(err)
	}
	if got != 22601050 {
		t.Fatalf("ConvertToUAH = %d, want 22601050", got)
	}
	if got, err := ConvertToUAH(12345, "UAH", 100000000); err != nil || got != 12345 {
		t.Fatalf("UAH conversion = %d, %v", got, err)
	}
}

func TestParseRate(t *testing.T) {
	for _, raw := range []string{"45.2021", "45,2021", "45.2021 UAH"} {
		got, err := ParseRate(raw)
		if err != nil {
			t.Fatalf("ParseRate(%q): %v", raw, err)
		}
		if got != 4520210000 {
			t.Fatalf("ParseRate(%q) = %d", raw, got)
		}
	}
}

func TestRejectInvalidAmounts(t *testing.T) {
	for _, raw := range []string{"", "0", "-5", "abc", "1e5"} {
		if _, err := ParseAmount(raw, "UAH"); err == nil {
			t.Fatalf("ParseAmount(%q) should fail", raw)
		}
	}
}
