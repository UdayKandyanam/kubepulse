package diagnose

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds the settings a config file can change. The zero value
// is not used directly: call DefaultConfig for the built in defaults,
// then apply a file over them with LoadConfig.
type Config struct {
	MemoryThresholdPercent float64
	SeverityOverrides      map[string]Severity
}

// DefaultMemoryThresholdPercent is the memory usage percent that the
// memory-close-to-limit detector flags when no config file says
// otherwise.
const DefaultMemoryThresholdPercent = 90.0

// DefaultConfig returns the settings kubepulse runs with when no
// config file is given.
func DefaultConfig() Config {
	return Config{
		MemoryThresholdPercent: DefaultMemoryThresholdPercent,
		SeverityOverrides:      map[string]Severity{},
	}
}

// KnownDetectors lists every detector name a config file may name in
// a severity override.
func KnownDetectors() []string {
	return []string{
		"crashloop",
		"cronjob-never-ran",
		"cronjob-suspended",
		"image-pull",
		"job-failed",
		"memory-close-to-limit",
		"no-resource-limits",
		"node-pressure",
		"oomkilled",
		"rollout-stuck",
		"unschedulable",
	}
}

func isKnownDetector(name string) bool {
	for _, d := range KnownDetectors() {
		if d == name {
			return true
		}
	}
	return false
}

// LoadConfig reads a config file and returns the defaults with the
// file settings applied. The format is a small subset of YAML, parsed
// here so kubepulse keeps no dependencies beyond the standard
// library:
//
//	memory_threshold_percent: 85
//	severities:
//	  crashloop: critical
//	  no-resource-limits: medium
//
// Blank lines and lines starting with # are ignored. Unknown keys,
// unknown detector names, bad severities, and a memory threshold
// outside 1 to 100 are errors, with the line number named.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	inSeverities := false
	lines := strings.Split(string(data), "\n")
	for i, raw := range lines {
		lineNo := i + 1
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indented := strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
		key, value, found := strings.Cut(trimmed, ":")
		if !found {
			return Config{}, fmt.Errorf("config line %d: want key: value, got %q", lineNo, trimmed)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if indented {
			if !inSeverities {
				return Config{}, fmt.Errorf("config line %d: indented entry %q is not under a severities block", lineNo, key)
			}
			if err := cfg.setSeverity(key, value); err != nil {
				return Config{}, fmt.Errorf("config line %d: %w", lineNo, err)
			}
			continue
		}
		inSeverities = false
		switch key {
		case "memory_threshold_percent":
			pct, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return Config{}, fmt.Errorf("config line %d: memory_threshold_percent %q is not a number", lineNo, value)
			}
			if pct <= 0 || pct > 100 {
				return Config{}, fmt.Errorf("config line %d: memory_threshold_percent must be above 0 and at most 100, got %v", lineNo, pct)
			}
			cfg.MemoryThresholdPercent = pct
		case "severities":
			if value != "" {
				return Config{}, fmt.Errorf("config line %d: severities takes indented detector lines, not a value on the same line", lineNo)
			}
			inSeverities = true
		default:
			return Config{}, fmt.Errorf("config line %d: unknown key %q (use memory_threshold_percent or severities)", lineNo, key)
		}
	}
	return cfg, nil
}

func (c *Config) setSeverity(detector, value string) error {
	if !isKnownDetector(detector) {
		return fmt.Errorf("unknown detector %q (known detectors: %s)", detector, strings.Join(KnownDetectors(), ", "))
	}
	sev, err := ParseSeverity(value)
	if err != nil {
		return fmt.Errorf("detector %s: %w", detector, err)
	}
	if c.SeverityOverrides == nil {
		c.SeverityOverrides = map[string]Severity{}
	}
	c.SeverityOverrides[detector] = sev
	return nil
}
