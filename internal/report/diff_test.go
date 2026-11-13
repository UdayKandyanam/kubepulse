package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/UdayKandyanam/kubepulse/internal/diagnose"
	"github.com/UdayKandyanam/kubepulse/internal/diff"
	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
)

func TestDiffTextSections(t *testing.T) {
	before := &snapshot.Snapshot{Cluster: "demo-cluster"}
	after := &snapshot.Snapshot{Cluster: "demo-cluster"}
	res := diff.Result{
		New: []diagnose.Finding{{
			Severity: diagnose.SeverityHigh, Detector: "crashloop",
			Object: "Pod shop/api-1", Summary: "container api is in CrashLoopBackOff after 9 restarts",
		}},
	}
	var buf bytes.Buffer
	if err := DiffText(&buf, before, after, res); err != nil {
		t.Fatalf("DiffText: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"1 new, 0 resolved, 0 unchanged", "NEW (1)", "RESOLVED (0)", "UNCHANGED (0)", "Pod shop/api-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("text diff output should contain %q, got:\n%s", want, out)
		}
	}
}

func TestDiffJSONShape(t *testing.T) {
	before := &snapshot.Snapshot{Cluster: "demo-cluster"}
	after := &snapshot.Snapshot{Cluster: "demo-cluster"}
	res := diff.Result{
		Resolved: []diagnose.Finding{{
			Severity: diagnose.SeverityLow, Detector: "no-resource-limits",
			Object: "Pod tools/debug-1", Summary: "containers without resource limits: shell",
		}},
	}
	var buf bytes.Buffer
	if err := DiffJSON(&buf, before, after, res); err != nil {
		t.Fatalf("DiffJSON: %v", err)
	}
	var doc struct {
		Summary struct {
			New       int `json:"new"`
			Resolved  int `json:"resolved"`
			Unchanged int `json:"unchanged"`
		} `json:"summary"`
		New       []diagnose.Finding `json:"new"`
		Resolved  []diagnose.Finding `json:"resolved"`
		Unchanged []diagnose.Finding `json:"unchanged"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("diff JSON should parse: %v", err)
	}
	if doc.Summary.Resolved != 1 || len(doc.Resolved) != 1 {
		t.Errorf("want 1 resolved finding, got summary %+v and %d entries", doc.Summary, len(doc.Resolved))
	}
	if doc.New == nil || doc.Unchanged == nil {
		t.Error("empty groups should render as empty arrays, not null")
	}
}
