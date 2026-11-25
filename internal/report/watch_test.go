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

// decodeLine parses one line of JSON output and fails the test when
// the line is not valid JSON, which is the whole contract for a log
// pipeline reading the stream.
func decodeLine(t *testing.T, line string) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		t.Fatalf("line is not valid JSON: %v\nline: %s", err, line)
	}
	return obj
}

func TestWatchReportJSONIsOneLine(t *testing.T) {
	snap := &snapshot.Snapshot{Cluster: "demo-cluster"}
	findings := []diagnose.Finding{{
		Severity: diagnose.SeverityLow,
		Detector: "no-resource-limits",
		Object:   "Pod shop/api-abc",
		Summary:  "containers without resource limits: api",
		Evidence: "container api has no limits set",
		NextStep: "set cpu and memory limits on the container",
	}}
	var out bytes.Buffer
	if err := WatchReportJSON(&out, 1, snap, findings); err != nil {
		t.Fatalf("WatchReportJSON: %v", err)
	}
	text := out.String()
	if strings.Count(text, "\n") != 1 {
		t.Errorf("output has %d newlines, want exactly 1 (one object on one line)", strings.Count(text, "\n"))
	}
	obj := decodeLine(t, strings.TrimSpace(text))
	if obj["round"] != float64(1) {
		t.Errorf("round = %v, want 1", obj["round"])
	}
	if obj["kind"] != "report" {
		t.Errorf("kind = %v, want report", obj["kind"])
	}
	if obj["cluster"] != "demo-cluster" {
		t.Errorf("cluster = %v, want demo-cluster", obj["cluster"])
	}
	summary, ok := obj["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary is %T, want an object", obj["summary"])
	}
	if summary["low"] != float64(1) {
		t.Errorf("summary low = %v, want 1", summary["low"])
	}
	list, ok := obj["findings"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("findings = %v, want one finding", obj["findings"])
	}
	first, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("finding is %T, want an object", list[0])
	}
	if first["detector"] != "no-resource-limits" {
		t.Errorf("finding detector = %v, want no-resource-limits", first["detector"])
	}
}

func TestWatchReportJSONEmptyFindingsIsArray(t *testing.T) {
	var out bytes.Buffer
	if err := WatchReportJSON(&out, 1, &snapshot.Snapshot{Cluster: "demo-cluster"}, nil); err != nil {
		t.Fatalf("WatchReportJSON: %v", err)
	}
	obj := decodeLine(t, strings.TrimSpace(out.String()))
	if _, ok := obj["findings"].([]any); !ok {
		t.Errorf("findings = %v, want an empty array, not null", obj["findings"])
	}
}

func TestWatchDiffJSONIsOneLine(t *testing.T) {
	before := &snapshot.Snapshot{Cluster: "demo-cluster"}
	after := &snapshot.Snapshot{Cluster: "demo-cluster"}
	finding := diagnose.Finding{
		Severity: diagnose.SeverityLow,
		Detector: "no-resource-limits",
		Object:   "Pod shop/api-abc",
		Summary:  "containers without resource limits: api",
		Evidence: "container api has no limits set",
		NextStep: "set cpu and memory limits on the container",
	}
	res := diff.Result{New: []diagnose.Finding{finding}}
	var out bytes.Buffer
	if err := WatchDiffJSON(&out, 2, before, after, res); err != nil {
		t.Fatalf("WatchDiffJSON: %v", err)
	}
	text := out.String()
	if strings.Count(text, "\n") != 1 {
		t.Errorf("output has %d newlines, want exactly 1 (one object on one line)", strings.Count(text, "\n"))
	}
	obj := decodeLine(t, strings.TrimSpace(text))
	if obj["round"] != float64(2) {
		t.Errorf("round = %v, want 2", obj["round"])
	}
	if obj["kind"] != "diff" {
		t.Errorf("kind = %v, want diff", obj["kind"])
	}
	summary, ok := obj["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary is %T, want an object", obj["summary"])
	}
	if summary["new"] != float64(1) || summary["resolved"] != float64(0) || summary["unchanged"] != float64(0) {
		t.Errorf("summary = %v, want new 1, resolved 0, unchanged 0", summary)
	}
	if list, ok := obj["new"].([]any); !ok || len(list) != 1 {
		t.Errorf("new = %v, want one finding", obj["new"])
	}
	if _, ok := obj["resolved"].([]any); !ok {
		t.Errorf("resolved = %v, want an empty array, not null", obj["resolved"])
	}
}
