package handler

import (
	"errors"
	"fmt"
	"math"
	"regexp"
)

// Validation rules below are kept in code (not just DB CHECK constraints) for
// two reasons:
//   1. The DB error from a CHECK violation is opaque (e.g.
//      "CHECK constraint failed: assets") and can't be turned into a useful
//      400 response without parsing strings.
//   2. Validators run before any DB call, so we don't waste a write on
//      garbage input.
//
// The two layers MUST stay in sync. Source of truth is
// internal/db/migrations/0001_init_*.sql.

var (
	validAssetTypes = map[string]struct{}{
		"cash-cny": {}, "wealth-mgmt-product": {}, "etf-fund": {},
		"cn-stock": {}, "hk-stock": {}, "us-stock": {},
		"social-insurance": {}, "real-estate": {},
	}
	validBuckets = map[string]struct{}{
		"cash": {}, "stable": {}, "growth": {},
	}
	validDirections = map[string]struct{}{
		"buy": {}, "sell": {}, "dividend": {}, "fee": {},
		"transfer-in": {}, "transfer-out": {}, "adjust": {},
	}
	validRiskLevels = map[string]struct{}{
		"R1": {}, "R2": {}, "R3": {}, "R4": {}, "R5": {},
	}
	dateRE     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	currencyRE = regexp.MustCompile(`^[A-Z]{3}$`)
)

func validateAssetType(s string) error {
	if _, ok := validAssetTypes[s]; !ok {
		return fmt.Errorf("asset_type %q invalid; must be one of cash-cny|wealth-mgmt-product|etf-fund|cn-stock|hk-stock|us-stock|social-insurance|real-estate", s)
	}
	return nil
}

func validateBucket(s string) error {
	if _, ok := validBuckets[s]; !ok {
		return fmt.Errorf("bucket %q invalid; must be one of cash|stable|growth", s)
	}
	return nil
}

func validateDirection(s string) error {
	if _, ok := validDirections[s]; !ok {
		return fmt.Errorf("direction %q invalid; must be one of buy|sell|dividend|fee|transfer-in|transfer-out|adjust", s)
	}
	return nil
}

func validateRiskLevel(s string) error {
	if s == "" {
		return nil
	}
	if _, ok := validRiskLevels[s]; !ok {
		return fmt.Errorf("risk_level %q invalid; must be empty or one of R1..R5", s)
	}
	return nil
}

func validateDate(s string) error {
	if !dateRE.MatchString(s) {
		return fmt.Errorf("date %q invalid; must be YYYY-MM-DD", s)
	}
	return nil
}

func validateCurrency(s string) error {
	if !currencyRE.MatchString(s) {
		return fmt.Errorf("currency %q invalid; must be 3 uppercase letters (ISO 4217)", s)
	}
	return nil
}

func validateNonNegativeCents(name string, c int64) error {
	if c < 0 {
		return fmt.Errorf("%s must be >= 0, got %d cents", name, c)
	}
	return nil
}

// resolveCents converts a money input that may be specified as either cents
// (preferred, no precision loss) or yuan (convenience). Returns ErrMoneyMissing
// when both are nil and ErrMoneyConflict when both are set.
//
// Accepting both lets a curl user write {"balance_yuan": 12345.67} while a
// programmatic client sends {"balance_cents": 1234567}.
var (
	ErrMoneyMissing  = errors.New("must supply either *_cents or *_yuan")
	ErrMoneyConflict = errors.New("supply only one of *_cents or *_yuan, not both")
)

func resolveCents(cents *int64, yuan *float64) (int64, error) {
	switch {
	case cents != nil && yuan != nil:
		return 0, ErrMoneyConflict
	case cents != nil:
		return *cents, nil
	case yuan != nil:
		// math.Round avoids 12.34 → 1233 due to float rep error.
		return int64(math.Round(*yuan * 100)), nil
	default:
		return 0, ErrMoneyMissing
	}
}
