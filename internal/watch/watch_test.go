package watch

import (
	"bytes"
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
