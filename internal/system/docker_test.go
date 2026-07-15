package system

import (
	"math"
	"testing"
)

func TestParsePercent(t *testing.T) {
	cases := map[string]float64{
		"12.34%":  12.34,
		" 0.00% ": 0,
		"100%":    100,
		"мусор":   0,
	}
	for in, want := range cases {
		if got := parsePercent(in); got != want {
			t.Errorf("parsePercent(%q) = %v, ожидали %v", in, got, want)
		}
	}
}

func TestParseSizeMB(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"256.3MiB", 256.3},
		{"2GiB", 2048},
		{"512KiB", 0.5},
		{"1048576B", 1},
		{"0B", 0},
	}
	for _, tc := range cases {
		got := parseSizeMB(tc.in)
		if math.Abs(got-tc.want) > 0.01 {
			t.Errorf("parseSizeMB(%q) = %v, ожидали %v", tc.in, got, tc.want)
		}
	}
}
