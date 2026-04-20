package handler

import (
	"errors"
	"testing"
)

func TestValidateAssetType(t *testing.T) {
	cases := []struct {
		in   string
		want bool // want valid
	}{
		{"cash-cny", true},
		{"wealth-mgmt-product", true},
		{"etf-fund", true},
		{"cn-stock", true},
		{"hk-stock", true},
		{"us-stock", true},
		{"social-insurance", true},
		{"real-estate", true},
		{"crypto", false},
		{"", false},
		{"CASH-CNY", false}, // case sensitive matches DB CHECK
	}
	for _, tc := range cases {
		err := validateAssetType(tc.in)
		ok := err == nil
		if ok != tc.want {
			t.Errorf("validateAssetType(%q) ok=%v want=%v err=%v", tc.in, ok, tc.want, err)
		}
	}
}

func TestValidateBucket(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"cash", true},
		{"stable", true},
		{"growth", true},
		{"speculative", false},
		{"", false},
	}
	for _, tc := range cases {
		err := validateBucket(tc.in)
		if (err == nil) != tc.want {
			t.Errorf("validateBucket(%q) -> %v, want valid=%v", tc.in, err, tc.want)
		}
	}
}

func TestValidateDirection(t *testing.T) {
	for _, in := range []string{"buy", "sell", "dividend", "fee", "transfer-in", "transfer-out", "adjust"} {
		if err := validateDirection(in); err != nil {
			t.Errorf("validateDirection(%q) unexpected err: %v", in, err)
		}
	}
	for _, in := range []string{"", "purchase", "BUY", "transfer"} {
		if err := validateDirection(in); err == nil {
			t.Errorf("validateDirection(%q) should fail", in)
		}
	}
}

func TestValidateRiskLevel(t *testing.T) {
	for _, in := range []string{"", "R1", "R2", "R3", "R4", "R5"} {
		if err := validateRiskLevel(in); err != nil {
			t.Errorf("validateRiskLevel(%q) unexpected err: %v", in, err)
		}
	}
	for _, in := range []string{"R0", "R6", "high", "r3"} {
		if err := validateRiskLevel(in); err == nil {
			t.Errorf("validateRiskLevel(%q) should fail", in)
		}
	}
}

func TestValidateDate(t *testing.T) {
	for _, in := range []string{"2026-04-20", "1999-12-31", "0000-00-00"} {
		// 0000-00-00 passes regex; SQLite GLOB also tolerates it. Real
		// calendar validation is intentionally NOT done here — same as DB.
		if err := validateDate(in); err != nil {
			t.Errorf("validateDate(%q) unexpected err: %v", in, err)
		}
	}
	for _, in := range []string{"", "2026-4-20", "20260420", "2026/04/20", "2026-04-20T00:00:00Z"} {
		if err := validateDate(in); err == nil {
			t.Errorf("validateDate(%q) should fail", in)
		}
	}
}

func TestValidateCurrency(t *testing.T) {
	for _, in := range []string{"CNY", "USD", "HKD"} {
		if err := validateCurrency(in); err != nil {
			t.Errorf("validateCurrency(%q) unexpected err: %v", in, err)
		}
	}
	for _, in := range []string{"", "cny", "RMB1", "us"} {
		if err := validateCurrency(in); err == nil {
			t.Errorf("validateCurrency(%q) should fail", in)
		}
	}
}

func TestValidateNonNegativeCents(t *testing.T) {
	if err := validateNonNegativeCents("amount", 0); err != nil {
		t.Errorf("0 should be valid: %v", err)
	}
	if err := validateNonNegativeCents("amount", 100); err != nil {
		t.Errorf("100 should be valid: %v", err)
	}
	if err := validateNonNegativeCents("amount", -1); err == nil {
		t.Errorf("-1 should fail")
	}
}

func TestResolveCents(t *testing.T) {
	intPtr := func(v int64) *int64 { return &v }
	flPtr := func(v float64) *float64 { return &v }

	// happy path: cents wins precision
	got, err := resolveCents(intPtr(1234567), nil)
	if err != nil || got != 1234567 {
		t.Errorf("cents-only: got %d err %v", got, err)
	}

	// yuan path: 12345.67 → 1234567 (no float drift)
	got, err = resolveCents(nil, flPtr(12345.67))
	if err != nil || got != 1234567 {
		t.Errorf("yuan path: got %d err %v want 1234567", got, err)
	}

	// trickier float that exposes naive truncation: 0.1 + 0.2 = 0.30000000000000004
	got, err = resolveCents(nil, flPtr(0.1+0.2))
	if err != nil || got != 30 {
		t.Errorf("float drift: got %d err %v want 30", got, err)
	}

	// both supplied → conflict
	_, err = resolveCents(intPtr(100), flPtr(1.0))
	if !errors.Is(err, ErrMoneyConflict) {
		t.Errorf("both supplied: want ErrMoneyConflict, got %v", err)
	}

	// neither → missing
	_, err = resolveCents(nil, nil)
	if !errors.Is(err, ErrMoneyMissing) {
		t.Errorf("neither: want ErrMoneyMissing, got %v", err)
	}
}
