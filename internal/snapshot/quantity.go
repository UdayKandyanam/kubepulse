package snapshot

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseMemory converts a Kubernetes memory quantity such as "512Mi" or
// "2Gi" into bytes. Plain numbers are bytes. Decimal units (k, M, G, T)
// and binary units (Ki, Mi, Gi, Ti) are both accepted, matching what
// kubectl accepts in a manifest.
func ParseMemory(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty quantity")
	}
	multipliers := []struct {
		suffix string
		mult   float64
	}{
		{"Ki", 1024},
		{"Mi", 1024 * 1024},
		{"Gi", 1024 * 1024 * 1024},
		{"Ti", 1024 * 1024 * 1024 * 1024},
		{"k", 1000},
		{"M", 1000 * 1000},
		{"G", 1000 * 1000 * 1000},
		{"T", 1000 * 1000 * 1000 * 1000},
		{"m", 0.001},
	}
	num := s
	mult := float64(1)
	for _, m := range multipliers {
		if strings.HasSuffix(s, m.suffix) {
			num = strings.TrimSuffix(s, m.suffix)
			mult = m.mult
			break
		}
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("parse quantity %q: %w", s, err)
	}
	return int64(v * mult), nil
}
