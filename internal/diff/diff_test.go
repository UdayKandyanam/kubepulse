package diff

import (
	"testing"

	"github.com/UdayKandyanam/kubepulse/internal/diagnose"
	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
)

func loadExample(t *testing.T, name string) []diagnose.Finding {
	t.Helper()
	s, err := snapshot.Load("../../examples/" + name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return diagnose.Diagnose(s)
}

func TestIdenticalSnapshotsHaveNoChanges(t *testing.T) {
	findings := loadExample(t, "broken.json")
	res := Compare(findings, findings)
	if len(res.New) != 0 {
		t.Errorf("want 0 new findings, got %d", len(res.New))
	}
	if len(res.Resolved) != 0 {
		t.Errorf("want 0 resolved findings, got %d", len(res.Resolved))
	}
	if len(res.Unchanged) != len(findings) {
		t.Errorf("want %d unchanged findings, got %d", len(findings), len(res.Unchanged))
	}
}

func TestHealthyToBrokenReportsEverythingNew(t *testing.T) {
	before := loadExample(t, "healthy.json")
	after := loadExample(t, "broken.json")
	if len(before) != 0 {
		t.Fatalf("healthy example should have no findings, got %d", len(before))
	}
	res := Compare(before, after)
	if len(res.New) != len(after) {
		t.Errorf("want %d new findings, got %d", len(after), len(res.New))
	}
	if len(res.Resolved) != 0 || len(res.Unchanged) != 0 {
		t.Errorf("want nothing resolved or unchanged, got %d resolved and %d unchanged",
			len(res.Resolved), len(res.Unchanged))
	}
	if !ShouldFailNew(res, diagnose.SeverityHigh) {
		t.Error("new critical and high findings should fail a high threshold")
	}
}

func TestBrokenToHealthyReportsEverythingResolved(t *testing.T) {
	before := loadExample(t, "broken.json")
	after := loadExample(t, "healthy.json")
	res := Compare(before, after)
	if len(res.Resolved) != len(before) {
		t.Errorf("want %d resolved findings, got %d", len(before), len(res.Resolved))
	}
	if len(res.New) != 0 || len(res.Unchanged) != 0 {
		t.Errorf("want nothing new or unchanged, got %d new and %d unchanged",
			len(res.New), len(res.Unchanged))
	}
	if ShouldFailNew(res, diagnose.SeverityLow) {
		t.Error("a diff with no new findings should not fail any threshold")
	}
}

func TestMatchNeedsSameSummary(t *testing.T) {
	// A crashloop finding whose restart count moved has a different
	// summary, so it reads as one resolved and one new finding.
	before := []diagnose.Finding{{
		Severity: diagnose.SeverityHigh, Detector: "crashloop",
		Object: "Pod shop/api-1", Summary: "container api is in CrashLoopBackOff after 3 restarts",
	}}
	after := []diagnose.Finding{{
		Severity: diagnose.SeverityHigh, Detector: "crashloop",
		Object: "Pod shop/api-1", Summary: "container api is in CrashLoopBackOff after 9 restarts",
	}}
	res := Compare(before, after)
	if len(res.New) != 1 || len(res.Resolved) != 1 || len(res.Unchanged) != 0 {
		t.Errorf("want 1 new, 1 resolved, 0 unchanged, got %d, %d, %d",
			len(res.New), len(res.Resolved), len(res.Unchanged))
	}
}
