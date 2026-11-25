package watch

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/UdayKandyanam/kubepulse/internal/diagnose"
	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
)

func healthySnapshot() *snapshot.Snapshot {
	return &snapshot.Snapshot{Cluster: "demo-cluster", CapturedAt: "2026-11-23T12:00:00Z"}
}

// brokenSnapshot has one pod with no resource limits, which the
// no-resource-limits detector flags at low severity, and nothing
// else any detector reads.
func brokenSnapshot() *snapshot.Snapshot {
	s := healthySnapshot()
	s.Pods = []snapshot.Pod{{
		Name:      "api-abc",
		Namespace: "shop",
		Phase:     "Running",
		Containers: []snapshot.Container{{
			Name:  "api",
			Image: "registry.example.com/shop/api:v1.0.0",
			State: snapshot.ContainerState{Running: &snapshot.StateDetail{}},
		}},
	}}
	return s
}

// listSource returns the snapshots in order, then an error.
func listSource(snaps ...*snapshot.Snapshot) Source {
	next := 0
	return func() (*snapshot.Snapshot, error) {
		if next >= len(snaps) {
			return nil, errors.New("no more snapshots")
		}
		s := snaps[next]
		next++
		return s, nil
	}
}

func TestRunReportsFirstRoundThenDiffs(t *testing.T) {
	var out bytes.Buffer
	var sleeps []time.Duration
	res, err := Run(&out, listSource(healthySnapshot(), brokenSnapshot(), brokenSnapshot(), healthySnapshot()), Options{
		Interval: 45 * time.Second,
		Rounds:   4,
		Config:   diagnose.DefaultConfig(),
		Sleep:    func(d time.Duration) { sleeps = append(sleeps, d) },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Rounds != 4 {
		t.Errorf("Rounds = %d, want 4", res.Rounds)
	}
	if res.Failed {
		t.Error("Failed = true with no fail-on threshold set")
	}
	text := out.String()
	for _, want := range []string{
		"KubePulse report for demo-cluster",
		"No problems found in this snapshot.",
		"1 new, 0 resolved, 0 unchanged",
		"0 new, 0 resolved, 1 unchanged",
		"0 new, 1 resolved, 0 unchanged",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, text)
		}
	}
	if len(sleeps) != 3 {
		t.Fatalf("slept %d times, want 3 (between rounds, not before the first)", len(sleeps))
	}
	for _, d := range sleeps {
		if d != 45*time.Second {
			t.Errorf("slept %v, want 45s", d)
		}
	}
}

func TestRunFailsOnNewFindingAtThreshold(t *testing.T) {
	var out bytes.Buffer
	res, err := Run(&out, listSource(healthySnapshot(), brokenSnapshot()), Options{
		Rounds: 2,
		Config: diagnose.DefaultConfig(),
		FailOn: diagnose.SeverityLow,
		Sleep:  func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Failed {
		t.Error("Failed = false, want true: round 2 added a low finding and the threshold is low")
	}
}

func TestRunDoesNotFailOnUnchangedFinding(t *testing.T) {
	var out bytes.Buffer
	res, err := Run(&out, listSource(brokenSnapshot(), brokenSnapshot()), Options{
		Rounds: 2,
		Config: diagnose.DefaultConfig(),
		FailOn: diagnose.SeverityMedium,
		Sleep:  func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Failed {
		t.Error("Failed = true, want false: the only finding is low, below the medium threshold, and unchanged in round 2")
	}
}

func TestRunJSONModeWritesOneObjectPerRound(t *testing.T) {
	var out bytes.Buffer
	res, err := Run(&out, listSource(healthySnapshot(), brokenSnapshot(), brokenSnapshot()), Options{
		Rounds: 3,
		Config: diagnose.DefaultConfig(),
		Format: "json",
		Sleep:  func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Rounds != 3 {
		t.Errorf("Rounds = %d, want 3", res.Rounds)
	}
	text := out.String()
	if strings.Contains(text, "\n\n") {
		t.Errorf("output has a blank line, which breaks a one object per line stream:\n%s", text)
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("output has %d lines, want 3 (one per round):\n%s", len(lines), text)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("round 1 line is not valid JSON: %v\nline: %s", err, lines[0])
	}
	if first["round"] != float64(1) || first["kind"] != "report" {
		t.Errorf("round 1 = round %v kind %v, want round 1 kind report", first["round"], first["kind"])
	}
	summary, ok := first["summary"].(map[string]any)
	if !ok {
		t.Fatalf("round 1 summary is %T, want an object", first["summary"])
	}
	if summary["low"] != float64(0) {
		t.Errorf("round 1 summary low = %v, want 0", summary["low"])
	}
	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("round 2 line is not valid JSON: %v\nline: %s", err, lines[1])
	}
	if second["round"] != float64(2) || second["kind"] != "diff" {
		t.Errorf("round 2 = round %v kind %v, want round 2 kind diff", second["round"], second["kind"])
	}
	diffSummary, ok := second["summary"].(map[string]any)
	if !ok {
		t.Fatalf("round 2 summary is %T, want an object", second["summary"])
	}
	if diffSummary["new"] != float64(1) || diffSummary["resolved"] != float64(0) || diffSummary["unchanged"] != float64(0) {
		t.Errorf("round 2 summary = %v, want new 1, resolved 0, unchanged 0", diffSummary)
	}
	var third map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &third); err != nil {
		t.Fatalf("round 3 line is not valid JSON: %v\nline: %s", err, lines[2])
	}
	thirdSummary, ok := third["summary"].(map[string]any)
	if !ok {
		t.Fatalf("round 3 summary is %T, want an object", third["summary"])
	}
	if thirdSummary["new"] != float64(0) || thirdSummary["unchanged"] != float64(1) {
		t.Errorf("round 3 summary = %v, want new 0, unchanged 1", thirdSummary)
	}
}

func TestRunStopsWhenSourceFails(t *testing.T) {
	var out bytes.Buffer
	res, err := Run(&out, listSource(healthySnapshot()), Options{
		Rounds: 5,
		Config: diagnose.DefaultConfig(),
		Sleep:  func(time.Duration) {},
	})
	if err == nil {
		t.Fatal("Run returned no error, want the source error")
	}
	if !strings.Contains(err.Error(), "round 2") {
		t.Errorf("error = %q, want it to name round 2", err)
	}
	if res.Rounds != 1 {
		t.Errorf("Rounds = %d, want 1 completed round before the failure", res.Rounds)
	}
	if !strings.Contains(out.String(), "KubePulse report for demo-cluster") {
		t.Error("the first round report should still be in the output")
	}
}
