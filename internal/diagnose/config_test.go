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
	if len(cfg.DisabledDetectors) != 0 {
		t.Errorf("default config should disable no detectors, got %v", cfg.DisabledDetectors)
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
	if cfg.MemoryThresholdPercent != 90 || len(cfg.SeverityOverrides) != 0 || len(cfg.DisabledDetectors) != 0 {
		t.Errorf("empty config should keep defaults, got %+v", cfg)
	}
}

func TestLoadConfigDisabledDetectors(t *testing.T) {
	path := writeConfig(t, "disabled_detectors:\n  no-resource-limits: true\n  node-pressure: false\n")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.IsDisabled("no-resource-limits") {
		t.Errorf("no-resource-limits should be disabled, got %v", cfg.DisabledDetectors)
	}
	if cfg.IsDisabled("node-pressure") {
		t.Errorf("node-pressure set to false should stay enabled, got %v", cfg.DisabledDetectors)
	}
	if cfg.IsDisabled("crashloop") {
		t.Errorf("crashloop was not listed and should stay enabled, got %v", cfg.DisabledDetectors)
	}
}

func TestLoadConfigRejectsBadFiles(t *testing.T) {
	cases := map[string]string{
		"unknown key":               "mem_threshold: 85\n",
		"bad severity":              "severities:\n  crashloop: urgent\n",
		"unknown detector":          "severities:\n  made-up-detector: high\n",
		"threshold too high":        "memory_threshold_percent: 140\n",
		"threshold zero":            "memory_threshold_percent: 0\n",
		"threshold not number":      "memory_threshold_percent: high\n",
		"missing colon":             "memory_threshold_percent 85\n",
		"stray indent":              "  crashloop: high\n",
		"disabled unknown detector": "disabled_detectors:\n  made-up-detector: true\n",
		"disabled bad value":        "disabled_detectors:\n  crashloop: yes\n",
		"disabled inline value":     "disabled_detectors: crashloop\n",
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

func TestConfigDisablesDetector(t *testing.T) {
	s := &snapshot.Snapshot{
		Pods: []snapshot.Pod{{
			Name: "debug-1", Namespace: "tools", Phase: "Running",
			Containers: []snapshot.Container{{Name: "shell"}},
		}},
		Nodes: []snapshot.Node{{
			Name:       "node-02",
			Conditions: []snapshot.Condition{{Type: "MemoryPressure", Status: "True", Message: "System memory is low"}},
		}},
	}
	if got := Diagnose(s); len(got) != 2 {
		t.Fatalf("default config should report both findings, got %+v", got)
	}
	cfg := DefaultConfig()
	cfg.DisabledDetectors["no-resource-limits"] = true
	got := DiagnoseWithConfig(s, cfg)
	if len(got) != 1 || got[0].Detector != "node-pressure" {
		t.Fatalf("disabling no-resource-limits should leave only node-pressure, got %+v", got)
	}
	cfg.DisabledDetectors["node-pressure"] = true
	if got := DiagnoseWithConfig(s, cfg); len(got) != 0 {
		t.Errorf("disabling both detectors should leave no findings, got %+v", got)
	}
}

func TestConfigDisabledFindingDoesNotFail(t *testing.T) {
	s := &snapshot.Snapshot{Nodes: []snapshot.Node{{
		Name:       "node-02",
		Conditions: []snapshot.Condition{{Type: "MemoryPressure", Status: "True", Message: "System memory is low"}},
	}}}
	cfg := DefaultConfig()
	cfg.DisabledDetectors["node-pressure"] = true
	got := DiagnoseWithConfig(s, cfg)
	if ShouldFail(got, SeverityLow) {
		t.Errorf("a disabled critical finding should not fail any threshold, got %+v", got)
	}
}

func TestDescribeDefaultConfig(t *testing.T) {
	got := DefaultConfig().Describe()
	want := "Effective config\n" +
		"memory_threshold_percent: 90\n" +
		"detectors:\n" +
		"  crashloop: high, enabled\n" +
		"  cronjob-never-ran: medium, enabled\n" +
		"  cronjob-suspended: medium, enabled\n" +
		"  image-pull: high, enabled\n" +
		"  job-failed: high, enabled\n" +
		"  memory-close-to-limit: medium, enabled\n" +
		"  no-resource-limits: low, enabled\n" +
		"  node-pressure: critical, enabled\n" +
		"  oomkilled: high, enabled\n" +
		"  rollout-stuck: high, enabled\n" +
		"  unschedulable: medium, enabled\n"
	if got != want {
		t.Errorf("default describe should list every detector with its default severity\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestDescribeShowsOverrideAndDisabled(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, "memory_threshold_percent: 80\nseverities:\n  no-resource-limits: medium\ndisabled_detectors:\n  node-pressure: true\n"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got := cfg.Describe()
	for _, line := range []string{
		"memory_threshold_percent: 80\n",
		"  no-resource-limits: medium (override), enabled\n",
		"  node-pressure: critical, disabled\n",
		"  crashloop: high, enabled\n",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("describe should contain %q, got:\n%s", line, got)
		}
	}
}

func TestEffectiveSeverity(t *testing.T) {
	cfg := DefaultConfig()
	if sev, ok := cfg.EffectiveSeverity("no-resource-limits"); !ok || sev != SeverityLow {
		t.Errorf("no-resource-limits should default to low, got %q, %v", sev, ok)
	}
	if sev, ok := cfg.EffectiveSeverity("node-pressure"); !ok || sev != SeverityCritical {
		t.Errorf("node-pressure should default to critical, got %q, %v", sev, ok)
	}
	cfg.SeverityOverrides["crashloop"] = SeverityCritical
	if sev, ok := cfg.EffectiveSeverity("crashloop"); !ok || sev != SeverityCritical {
		t.Errorf("crashloop override should win, got %q, %v", sev, ok)
	}
	if _, ok := cfg.EffectiveSeverity("made-up-detector"); ok {
		t.Error("an unknown detector should not have an effective severity")
	}
	if _, ok := DefaultSeverity("made-up-detector"); ok {
		t.Error("an unknown detector should not have a default severity")
	}
}

func TestLoadConfigErrorNamesLine(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "memory_threshold_percent: 85\nseverities:\n  crashloop: urgent\n"))
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error should name line 3, got %v", err)
	}
}
