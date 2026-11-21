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
	DisabledDetectors      map[string]bool
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
		DisabledDetectors:      map[string]bool{},
	}
}

// IsDisabled reports whether a detector is turned off in this config.
func (c Config) IsDisabled(detector string) bool {
	return c.DisabledDetectors[detector]
}

// DefaultSeverity returns the severity a detector assigns when no
// override is in place. The second result is false for a name that
// is not a known detector.
func DefaultSeverity(detector string) (Severity, bool) {
	switch detector {
	case "crashloop", "image-pull", "job-failed", "oomkilled", "rollout-stuck":
		return SeverityHigh, true
	case "cronjob-never-ran", "cronjob-stale", "cronjob-suspended", "memory-close-to-limit", "unschedulable":
		return SeverityMedium, true
	case "no-resource-limits":
		return SeverityLow, true
	case "node-pressure":
		return SeverityCritical, true
	}
	return "", false
}

// EffectiveSeverity returns the severity a detector will report
// under this config: the override when one is set, otherwise the
// default. The second result is false for an unknown detector.
func (c Config) EffectiveSeverity(detector string) (Severity, bool) {
	if sev, ok := c.SeverityOverrides[detector]; ok {
		return sev, true
	}
	return DefaultSeverity(detector)
}

// Describe returns the effective config as plain text, one detector
// per line in KnownDetectors order. It shows the memory threshold,
// the severity each detector will report, whether an override set
// that severity, and whether the detector is disabled. I use it for
// the print config flag, so a run can show the settings it used
// without the reader having to merge the file and the defaults by
// hand.
func (c Config) Describe() string {
	var b strings.Builder
	b.WriteString("Effective config\n")
	fmt.Fprintf(&b, "memory_threshold_percent: %s\n", strconv.FormatFloat(c.MemoryThresholdPercent, 'g', -1, 64))
	b.WriteString("detectors:\n")
	for _, name := range KnownDetectors() {
		sev, _ := c.EffectiveSeverity(name)
		state := "enabled"
		if c.IsDisabled(name) {
			state = "disabled"
		}
		marker := ""
		if _, ok := c.SeverityOverrides[name]; ok {
			marker = " (override)"
		}
		fmt.Fprintf(&b, "  %s: %s%s, %s\n", name, sev, marker, state)
	}
	return b.String()
}

// KnownDetectors lists every detector name a config file may name in
// a severity override.
func KnownDetectors() []string {
	return []string{
		"crashloop",
		"cronjob-never-ran",
		"cronjob-stale",
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
//	disabled_detectors:
//	  node-pressure: true
//
// Blank lines and lines starting with # are ignored. Unknown keys,
// unknown detector names, bad severities, bad disabled values, and a
// memory threshold outside 1 to 100 are errors, with the line number
// named.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	section := ""
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
			switch section {
			case "severities":
				if err := cfg.setSeverity(key, value); err != nil {
					return Config{}, fmt.Errorf("config line %d: %w", lineNo, err)
				}
			case "disabled_detectors":
				if err := cfg.setDisabled(key, value); err != nil {
					return Config{}, fmt.Errorf("config line %d: %w", lineNo, err)
				}
			default:
				return Config{}, fmt.Errorf("config line %d: indented entry %q is not under a severities or disabled_detectors block", lineNo, key)
			}
			continue
		}
		section = ""
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
			section = "severities"
		case "disabled_detectors":
			if value != "" {
				return Config{}, fmt.Errorf("config line %d: disabled_detectors takes indented detector lines, not a value on the same line", lineNo)
			}
			section = "disabled_detectors"
		default:
			return Config{}, fmt.Errorf("config line %d: unknown key %q (use memory_threshold_percent, severities, or disabled_detectors)", lineNo, key)
		}
	}
	return cfg, nil
}

func (c *Config) setDisabled(detector, value string) error {
	if !isKnownDetector(detector) {
		return fmt.Errorf("unknown detector %q (known detectors: %s)", detector, strings.Join(KnownDetectors(), ", "))
	}
	switch value {
	case "true":
		if c.DisabledDetectors == nil {
			c.DisabledDetectors = map[string]bool{}
		}
		c.DisabledDetectors[detector] = true
	case "false":
		if c.DisabledDetectors == nil {
			c.DisabledDetectors = map[string]bool{}
		}
		c.DisabledDetectors[detector] = false
	default:
		return fmt.Errorf("detector %s: disabled value %q is not true or false", detector, value)
	}
	return nil
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
