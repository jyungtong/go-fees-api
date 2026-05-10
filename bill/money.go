package bill

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func parseMoneyAmount(value string) (int64, error) {
	if value == "" {
		return 0, errors.New("amount is required")
	}

	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("amount must be a decimal string")
	}
	if len(parts) == 2 && (parts[1] == "" || len(parts[1]) > 2) {
		return 0, errors.New("amount must have at most two decimal places")
	}

	if !allDigits(parts[0]) {
		return 0, errors.New("amount must be a decimal string")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > math.MaxInt64/100 {
		return 0, errors.New("amount is too large")
	}

	var cents int64
	if len(parts) == 2 {
		if !allDigits(parts[1]) {
			return 0, errors.New("amount must be a decimal string")
		}
		fraction := parts[1]
		if len(fraction) == 1 {
			fraction += "0"
		}
		cents, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, errors.New("amount must be a decimal string")
		}
	}

	minor := whole*100 + cents
	if minor <= 0 {
		return 0, errors.New("amount must be greater than 0")
	}
	return minor, nil
}

func formatMoneyAmount(minor int64) string {
	return fmt.Sprintf("%d.%02d", minor/100, minor%100)
}

func formatOptionalMoneyAmount(minor *int64) *string {
	if minor == nil {
		return nil
	}
	formatted := formatMoneyAmount(*minor)
	return &formatted
}

func allDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
