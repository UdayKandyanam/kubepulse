package diagnose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubepulse.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MemoryThresholdPercent != 90 {
		t.Errorf("default memory threshold should be 90, got %v", cfg.MemoryThresholdPercent)
	}
	if len(cfg.SeverityOverrides) != 0 {
		t.Errorf("default config should have no overrides, got %v", cfg.SeverityOverrides)
	}
}

func TestLoadConfigAppliesSettings(t *testing.T) {
	path := writeConfig(t, "# team thresholds\nmemory_threshold_percent: 85\n\nseverities:\n  crashloop: critical\n  no-resource-limits: medium\n")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.MemoryThresholdPercent != 85 {
		t.Errorf("memory threshold should be 85, got %v", cfg.MemoryThresholdPercent)
	}
	if cfg.SeverityOverrides["crashloop"] != SeverityCritical {
		t.Errorf("crashloop should be critical, got %q", cfg.SeverityOverrides["crashloop"])
	}
	if cfg.SeverityOverrides["no-resource-limits"] != SeverityMedium {
		t.Errorf("no-resource-limits should be medium, got %q", cfg.SeverityOverrides["no-resource-limits"])
	}
}

func TestLoadConfigEmptyFileKeepsDefaults(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, "# only a comment\n"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.MemoryThresholdPercent != 90 || len(cfg.SeverityOverrides) != 0 {
		t.Errorf("empty config should keep defaults, got %+v", cfg)
	}
}

func TestLoadConfigRejectsBadFiles(t *testing.T) {
	cases := map[string]string{
		"unknown key":          "mem_threshold: 85\n",
		"bad severity":         "severities:\n  crashloop: urgent\n",
		"unknown detector":     "severities:\n  made-up-detector: high\n",
		"threshold too high":   "memory_threshold_percent: 140\n",
		"threshold zero":       "memory_threshold_percent: 0\n",
		"threshold not number": "memory_threshold_percent: high\n",
		"missing colon":        "memory_threshold_percent 85\n",
		"stray indent":         "  crashloop: high\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(writeConfig(t, content)); err == nil {
				t.Errorf("config %q should be rejected", content)
			}
		})
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Error("a missing config file should be an error, not silent defaults")
	}
}

func TestConfigLowersMemoryThreshold(t *testing.T) {
	const mi = 1024 * 1024
	s := &snapshot.Snapshot{Pods: []snapshot.Pod{{
		Name: "cache-1", Namespace: "shop", Phase: "Running",
		Containers: []snapshot.Container{{
			Name:      "cache",
			Resources: snapshot.Resources{Limits: snapshot.ResourceList{CPU: "1", Memory: "512Mi"}},
			Metrics:   snapshot.Metrics{MemoryUsageBytes: 410 * mi},
		}},
	}}}
	// 410 of 512 Mi is 80 percent: under the default 90, over a
	// configured 75.
	if got := findByDetector(Diagnose(s), "memory-close-to-limit"); len(got) != 0 {
		t.Fatalf("default config should not flag 80 percent, got %+v", got)
	}
	cfg := DefaultConfig()
	cfg.MemoryThresholdPercent = 75
	if got := findByDetector(DiagnoseWithConfig(s, cfg), "memory-close-to-limit"); len(got) != 1 {
		t.Errorf("threshold 75 should flag 80 percent, got %+v", got)
	}
}

func TestConfigRaisesMemoryThreshold(t *testing.T) {
	const mi = 1024 * 1024
	s := &snapshot.Snapshot{Pods: []snapshot.Pod{{
		Name: "cache-1", Namespace: "shop", Phase: "Running",
		Containers: []snapshot.Container{{
			Name:      "cache",
			Resources: snapshot.Resources{Limits: snapshot.ResourceList{CPU: "1", Memory: "512Mi"}},
			Metrics:   snapshot.Metrics{MemoryUsageBytes: 480 * mi},
		}},
	}}}
	cfg := DefaultConfig()
	cfg.MemoryThresholdPercent = 99
	if got := findByDetector(DiagnoseWithConfig(s, cfg), "memory-close-to-limit"); len(got) != 0 {
		t.Errorf("threshold 99 should not flag 94 percent, got %+v", got)
	}
}

func TestConfigSeverityOverrideResorts(t *testing.T) {
	s := &snapshot.Snapshot{Pods: []snapshot.Pod{{
		Name: "debug-1", Namespace: "tools", Phase: "Running",
		Containers: []snapshot.Container{{Name: "shell"}},
	}}}
	cfg := DefaultConfig()
	cfg.SeverityOverrides["no-resource-limits"] = SeverityCritical
	got := DiagnoseWithConfig(s, cfg)
	if len(got) != 1 || got[0].Severity != SeverityCritical {
		t.Fatalf("override should make the finding critical, got %+v", got)
	}
	if !ShouldFail(got, SeverityHigh) {
		t.Error("an overridden critical finding should fail a high threshold")
	}
}

func TestLoadConfigErrorNamesLine(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "memory_threshold_percent: 85\nseverities:\n  crashloop: urgent\n"))
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error should name line 3, got %v", err)
	}
}
