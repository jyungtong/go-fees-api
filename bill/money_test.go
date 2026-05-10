package bill

import "testing"

func TestParseMoneyAmount(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  int64
	}{
		{name: "whole", value: "3", want: 300},
		{name: "one decimal", value: "3.5", want: 350},
		{name: "two decimals", value: "3.50", want: 350},
		{name: "leading zeroes", value: "001.20", want: 120},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMoneyAmount(tt.value)
			if err != nil {
				t.Fatalf("parseMoneyAmount(%q) error = %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("parseMoneyAmount(%q) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

func TestParseMoneyAmountRejectsInvalidValues(t *testing.T) {
	tests := []string{"", "0", "0.00", "-1.00", ".50", "1.", "1.234", "abc"}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			if got, err := parseMoneyAmount(tt); err == nil {
				t.Fatalf("parseMoneyAmount(%q) = %d, want error", tt, got)
			}
		})
	}
}

func TestFormatMoneyAmount(t *testing.T) {
	tests := []struct {
		minor int64
		want  string
	}{
		{minor: 0, want: "0.00"},
		{minor: 5, want: "0.05"},
		{minor: 350, want: "3.50"},
		{minor: 123456, want: "1234.56"},
	}

	for _, tt := range tests {
		if got := formatMoneyAmount(tt.minor); got != tt.want {
			t.Fatalf("formatMoneyAmount(%d) = %q, want %q", tt.minor, got, tt.want)
		}
	}
}
