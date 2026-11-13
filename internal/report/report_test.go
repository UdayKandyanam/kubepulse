package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/UdayKandyanam/kubepulse/internal/diagnose"
	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
)

func sampleFindings() []diagnose.Finding {
	return []diagnose.Finding{
		{Severity: diagnose.SeverityLow, Detector: "no-resource-limits", Object: "Pod tools/debug-1", Summary: "no limits", Evidence: "ev", NextStep: "set limits"},
		{Severity: diagnose.SeverityCritical, Detector: "node-pressure", Object: "Node node-1", Summary: "disk pressure", Evidence: "ev", NextStep: "cordon"},
		{Severity: diagnose.SeverityHigh, Detector: "crashloop", Object: "Pod shop/api-1", Summary: "crashlooping", Evidence: "ev", NextStep: "read logs"},
	}
}

func TestTextGroupsBySeverity(t *testing.T) {
	var buf bytes.Buffer
	s := &snapshot.Snapshot{Cluster: "lab-cluster"}
	if err := Text(&buf, s, sampleFindings()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	ci := strings.Index(out, "CRITICAL")
	hi := strings.Index(out, "HIGH")
	lo := strings.Index(out, "LOW")
	if ci < 0 || hi < 0 || lo < 0 || !(ci < hi && hi < lo) {
		t.Errorf("groups out of order in output:\n%s", out)
	}
	if !strings.Contains(out, "3 findings: 1 critical, 1 high, 0 medium, 1 low") {
		t.Errorf("missing counts line:\n%s", out)
	}
}

func TestTextNoFindings(t *testing.T) {
	var buf bytes.Buffer
	if err := Text(&buf, &snapshot.Snapshot{Cluster: "lab-cluster"}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No problems found") {
		t.Errorf("unexpected output: %q", buf.String())
	}
}

func TestJSONStableAndSorted(t *testing.T) {
	s := &snapshot.Snapshot{Cluster: "lab-cluster"}
	var first, second bytes.Buffer
	if err := JSON(&first, s, sampleFindings()); err != nil {
		t.Fatal(err)
	}
	if err := JSON(&second, s, sampleFindings()); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Error("JSON output should be identical across runs")
	}
	var doc struct {
		Cluster  string             `json:"cluster"`
		Summary  map[string]int     `json:"summary"`
		Findings []diagnose.Finding `json:"findings"`
	}
	if err := json.Unmarshal(first.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if doc.Findings[0].Severity != diagnose.SeverityCritical {
		t.Errorf("first finding should be critical, got %s", doc.Findings[0].Severity)
	}
	if doc.Summary["high"] != 1 || doc.Summary["low"] != 1 {
		t.Errorf("bad summary counts: %v", doc.Summary)
	}
}
