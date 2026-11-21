package diagnose

import (
	"testing"

	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
)

func findByDetector(findings []Finding, detector string) []Finding {
	var out []Finding
	for _, f := range findings {
		if f.Detector == detector {
			out = append(out, f)
		}
	}
	return out
}

func TestCrashLoop(t *testing.T) {
	s := &snapshot.Snapshot{Pods: []snapshot.Pod{{
		Name: "api-1", Namespace: "shop", Phase: "Running",
		Containers: []snapshot.Container{{
			Name: "api", RestartCount: 9,
			State: snapshot.ContainerState{Waiting: &snapshot.StateDetail{Reason: "CrashLoopBackOff", Message: "back-off 5m0s restarting failed container"}},
		}},
	}}}
	got := findByDetector(Diagnose(s), "crashloop")
	if len(got) != 1 {
		t.Fatalf("want 1 crashloop finding, got %d", len(got))
	}
	if got[0].Severity != SeverityHigh || got[0].Object != "Pod shop/api-1" {
		t.Errorf("unexpected finding: %+v", got[0])
	}
}

func TestOOMKilled(t *testing.T) {
	s := &snapshot.Snapshot{Pods: []snapshot.Pod{{
		Name: "worker-1", Namespace: "batch", Phase: "Running",
		Containers: []snapshot.Container{{
			Name: "worker", RestartCount: 4,
			LastState: snapshot.ContainerState{Terminated: &snapshot.StateDetail{Reason: "OOMKilled", ExitCode: 137}},
			Resources: snapshot.Resources{Limits: snapshot.ResourceList{CPU: "1", Memory: "512Mi"}},
		}},
	}}}
	got := findByDetector(Diagnose(s), "oomkilled")
	if len(got) != 1 || got[0].Severity != SeverityHigh {
		t.Fatalf("want 1 high oomkilled finding, got %+v", got)
	}
}

func TestImagePull(t *testing.T) {
	for _, reason := range []string{"ImagePullBackOff", "ErrImagePull"} {
		s := &snapshot.Snapshot{Pods: []snapshot.Pod{{
			Name: "web-1", Namespace: "shop", Phase: "Pending",
			Containers: []snapshot.Container{{
				Name: "web", Image: "registry.example.com/shop/web:v2",
				State: snapshot.ContainerState{Waiting: &snapshot.StateDetail{Reason: reason, Message: "failed to pull image"}},
			}},
		}}}
		got := findByDetector(Diagnose(s), "image-pull")
		if len(got) != 1 {
			t.Errorf("reason %s: want 1 image-pull finding, got %d", reason, len(got))
		}
	}
}

func TestUnschedulableFromEvent(t *testing.T) {
	s := &snapshot.Snapshot{
		Pods: []snapshot.Pod{{Name: "job-1", Namespace: "batch", Phase: "Pending"}},
		Events: []snapshot.Event{
			{Type: "Warning", Reason: "FailedScheduling",
				Object:  snapshot.Object{Kind: "Pod", Namespace: "batch", Name: "job-1"},
				Message: "0/3 nodes are available: insufficient cpu"},
		},
	}
	got := findByDetector(Diagnose(s), "unschedulable")
	if len(got) != 1 || got[0].Severity != SeverityMedium {
		t.Fatalf("want 1 medium unschedulable finding, got %+v", got)
	}
	if got[0].Evidence != "0/3 nodes are available: insufficient cpu" {
		t.Errorf("evidence should come from the event, got %q", got[0].Evidence)
	}
}

func TestUnschedulableNeedsEvidence(t *testing.T) {
	// A Pending pod with no scheduling failure on record is often just
	// new. It should not produce a finding.
	s := &snapshot.Snapshot{Pods: []snapshot.Pod{{Name: "new-1", Namespace: "batch", Phase: "Pending"}}}
	if got := findByDetector(Diagnose(s), "unschedulable"); len(got) != 0 {
		t.Errorf("want no findings, got %+v", got)
	}
}

func TestRolloutStuck(t *testing.T) {
	s := &snapshot.Snapshot{Deployments: []snapshot.Deployment{{
		Name: "checkout", Namespace: "shop",
		Replicas: 3, UpdatedReplicas: 1, ReadyReplicas: 2, AvailableReplicas: 2,
		Conditions: []snapshot.Condition{
			{Type: "Progressing", Status: "False", Reason: "ProgressDeadlineExceeded", Message: "timed out progressing"},
		},
	}}}
	got := findByDetector(Diagnose(s), "rollout-stuck")
	if len(got) != 1 || got[0].Severity != SeverityHigh {
		t.Fatalf("want 1 high rollout finding, got %+v", got)
	}
}

func TestRolloutBehindButProgressing(t *testing.T) {
	s := &snapshot.Snapshot{Deployments: []snapshot.Deployment{{
		Name: "checkout", Namespace: "shop",
		Replicas: 3, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2,
		Conditions: []snapshot.Condition{
			{Type: "Progressing", Status: "True"},
		},
	}}}
	if got := findByDetector(Diagnose(s), "rollout-stuck"); len(got) != 0 {
		t.Errorf("a rollout that is still progressing is not stuck, got %+v", got)
	}
}

func TestNoResourceLimits(t *testing.T) {
	s := &snapshot.Snapshot{Pods: []snapshot.Pod{{
		Name: "debug-1", Namespace: "tools", Phase: "Running",
		Containers: []snapshot.Container{
			{Name: "shell"},
			{Name: "app", Resources: snapshot.Resources{Limits: snapshot.ResourceList{CPU: "500m", Memory: "256Mi"}}},
		},
	}}}
	got := findByDetector(Diagnose(s), "no-resource-limits")
	if len(got) != 1 || got[0].Severity != SeverityLow {
		t.Fatalf("want 1 low finding, got %+v", got)
	}
	if got[0].Summary != "containers without resource limits: shell" {
		t.Errorf("summary should name only the unlimited container, got %q", got[0].Summary)
	}
}

func TestMemoryCloseToLimit(t *testing.T) {
	const mi = 1024 * 1024
	s := &snapshot.Snapshot{Pods: []snapshot.Pod{{
		Name: "cache-1", Namespace: "shop", Phase: "Running",
		Containers: []snapshot.Container{{
			Name:      "cache",
			Resources: snapshot.Resources{Limits: snapshot.ResourceList{CPU: "1", Memory: "512Mi"}},
			Metrics:   snapshot.Metrics{MemoryUsageBytes: 480 * mi},
		}},
	}}}
	got := findByDetector(Diagnose(s), "memory-close-to-limit")
	if len(got) != 1 || got[0].Severity != SeverityMedium {
		t.Fatalf("want 1 medium finding, got %+v", got)
	}
}

func TestMemoryBelowThreshold(t *testing.T) {
	const mi = 1024 * 1024
	s := &snapshot.Snapshot{Pods: []snapshot.Pod{{
		Name: "cache-1", Namespace: "shop", Phase: "Running",
		Containers: []snapshot.Container{{
			Name:      "cache",
			Resources: snapshot.Resources{Limits: snapshot.ResourceList{CPU: "1", Memory: "512Mi"}},
			Metrics:   snapshot.Metrics{MemoryUsageBytes: 200 * mi},
		}},
	}}}
	if got := findByDetector(Diagnose(s), "memory-close-to-limit"); len(got) != 0 {
		t.Errorf("usage at 39 percent should not be flagged, got %+v", got)
	}
}

func TestNodePressure(t *testing.T) {
	s := &snapshot.Snapshot{Nodes: []snapshot.Node{
		{Name: "node-1", Conditions: []snapshot.Condition{{Type: "MemoryPressure", Status: "True", Message: "System memory is low"}}},
		{Name: "node-2", Conditions: []snapshot.Condition{{Type: "DiskPressure", Status: "True"}}},
		{Name: "node-3", Conditions: []snapshot.Condition{{Type: "MemoryPressure", Status: "False"}}},
	}}
	got := findByDetector(Diagnose(s), "node-pressure")
	if len(got) != 2 {
		t.Fatalf("want 2 node pressure findings, got %d", len(got))
	}
	for _, f := range got {
		if f.Severity != SeverityCritical {
			t.Errorf("node pressure should be critical, got %s", f.Severity)
		}
	}
}

func TestSortedAndShouldFail(t *testing.T) {
	s := &snapshot.Snapshot{
		Pods: []snapshot.Pod{{
			Name: "debug-1", Namespace: "tools", Phase: "Running",
			Containers: []snapshot.Container{{Name: "shell"}},
		}},
		Nodes: []snapshot.Node{
			{Name: "node-1", Conditions: []snapshot.Condition{{Type: "DiskPressure", Status: "True"}}},
		},
	}
	findings := Diagnose(s)
	if len(findings) != 2 {
		t.Fatalf("want 2 findings, got %d", len(findings))
	}
	if findings[0].Severity != SeverityCritical || findings[1].Severity != SeverityLow {
		t.Errorf("findings should be sorted by severity, got %s then %s", findings[0].Severity, findings[1].Severity)
	}
	if !ShouldFail(findings, SeverityHigh) {
		t.Error("a critical finding should fail a high threshold")
	}
	lowOnly := []Finding{{Severity: SeverityLow}}
	if ShouldFail(lowOnly, SeverityHigh) {
		t.Error("a low finding should not fail a high threshold")
	}
	if _, err := ParseSeverity("urgent"); err == nil {
		t.Error("ParseSeverity should reject unknown values")
	}
}

func TestJobFailedFromCondition(t *testing.T) {
	s := &snapshot.Snapshot{Jobs: []snapshot.Job{{
		Name: "nightly-report", Namespace: "batch", Completions: 1, Failed: 3,
		Conditions: []snapshot.Condition{
			{Type: "Failed", Status: "True", Reason: "BackoffLimitExceeded", Message: "Job has reached the specified backoff limit"},
		},
	}}}
	got := findByDetector(Diagnose(s), "job-failed")
	if len(got) != 1 || got[0].Severity != SeverityHigh || got[0].Object != "Job batch/nightly-report" {
		t.Fatalf("want 1 high job-failed finding, got %+v", got)
	}
}

func TestJobFailedFromCountsWithoutCondition(t *testing.T) {
	s := &snapshot.Snapshot{Jobs: []snapshot.Job{{
		Name: "import", Namespace: "batch", Failed: 2, Active: 0, Succeeded: 0,
	}}}
	got := findByDetector(Diagnose(s), "job-failed")
	if len(got) != 1 {
		t.Fatalf("want 1 job-failed finding, got %+v", got)
	}
}

func TestJobStillRunningIsNotFailed(t *testing.T) {
	s := &snapshot.Snapshot{Jobs: []snapshot.Job{{
		Name: "import", Namespace: "batch", Failed: 1, Active: 1,
	}}}
	if got := findByDetector(Diagnose(s), "job-failed"); len(got) != 0 {
		t.Errorf("want no findings for a running job, got %+v", got)
	}
}

func TestCompletedJobIsNotFailed(t *testing.T) {
	s := &snapshot.Snapshot{Jobs: []snapshot.Job{{
		Name: "nightly-report", Namespace: "batch", Succeeded: 1,
		Conditions: []snapshot.Condition{{Type: "Complete", Status: "True"}},
	}}}
	if got := findByDetector(Diagnose(s), "job-failed"); len(got) != 0 {
		t.Errorf("want no findings for a completed job, got %+v", got)
	}
}

func TestCronJobSuspended(t *testing.T) {
	s := &snapshot.Snapshot{CronJobs: []snapshot.CronJob{{
		Name: "cleanup", Namespace: "batch", Schedule: "0 2 * * *", Suspend: true, LastScheduleTime: "2026-11-10T02:00:00Z",
	}}}
	got := findByDetector(Diagnose(s), "cronjob-suspended")
	if len(got) != 1 || got[0].Severity != SeverityMedium || got[0].Object != "CronJob batch/cleanup" {
		t.Fatalf("want 1 medium cronjob-suspended finding, got %+v", got)
	}
	if got := findByDetector(Diagnose(s), "cronjob-never-ran"); len(got) != 0 {
		t.Errorf("suspended cronjob should not also be flagged as never ran, got %+v", got)
	}
}

func TestCronJobNeverRan(t *testing.T) {
	s := &snapshot.Snapshot{CronJobs: []snapshot.CronJob{{
		Name: "hourly-sync", Namespace: "shop", Schedule: "0 * * * *",
	}}}
	got := findByDetector(Diagnose(s), "cronjob-never-ran")
	if len(got) != 1 || got[0].Severity != SeverityMedium {
		t.Fatalf("want 1 medium cronjob-never-ran finding, got %+v", got)
	}
}

func TestCronJobStaleHourly(t *testing.T) {
	s := &snapshot.Snapshot{
		CapturedAt: "2026-11-13T14:02:00Z",
		CronJobs: []snapshot.CronJob{{
			Name: "hourly-sync", Namespace: "shop", Schedule: "0 * * * *", LastScheduleTime: "2026-11-13T08:00:00Z",
		}},
	}
	got := findByDetector(Diagnose(s), "cronjob-stale")
	if len(got) != 1 || got[0].Severity != SeverityMedium || got[0].Object != "CronJob shop/hourly-sync" {
		t.Fatalf("want 1 medium cronjob-stale finding, got %+v", got)
	}
}

func TestCronJobStaleDaily(t *testing.T) {
	s := &snapshot.Snapshot{
		CapturedAt: "2026-11-13T14:02:00Z",
		CronJobs: []snapshot.CronJob{{
			Name: "cleanup", Namespace: "batch", Schedule: "0 2 * * *", LastScheduleTime: "2026-11-10T02:00:00Z",
		}},
	}
	got := findByDetector(Diagnose(s), "cronjob-stale")
	if len(got) != 1 {
		t.Fatalf("a daily cronjob last run 3 days ago should be stale, got %+v", got)
	}
}

func TestCronJobRecentRunIsNotStale(t *testing.T) {
	s := &snapshot.Snapshot{
		CapturedAt: "2026-11-13T14:02:00Z",
		CronJobs: []snapshot.CronJob{
			{Name: "cleanup", Namespace: "batch", Schedule: "0 2 * * *", LastScheduleTime: "2026-11-13T02:00:00Z"},
			{Name: "weekly-report", Namespace: "batch", Schedule: "0 9 * * 1", LastScheduleTime: "2026-11-09T09:00:00Z"},
		},
	}
	if got := findByDetector(Diagnose(s), "cronjob-stale"); len(got) != 0 {
		t.Errorf("daily run 12h ago and weekly run 4 days ago should not be stale, got %+v", got)
	}
}

func TestCronJobStaleSkipsUnjudgeable(t *testing.T) {
	s := &snapshot.Snapshot{
		CapturedAt: "2026-11-13T14:02:00Z",
		CronJobs: []snapshot.CronJob{
			{Name: "paused", Namespace: "batch", Schedule: "0 * * * *", Suspend: true, LastScheduleTime: "2026-11-01T00:00:00Z"},
			{Name: "odd", Namespace: "batch", Schedule: "not a schedule", LastScheduleTime: "2026-11-01T00:00:00Z"},
			{Name: "bad-time", Namespace: "batch", Schedule: "0 * * * *", LastScheduleTime: "yesterday"},
		},
	}
	if got := findByDetector(Diagnose(s), "cronjob-stale"); len(got) != 0 {
		t.Errorf("suspended, unparseable, and bad time cronjobs should not be flagged stale, got %+v", got)
	}
	noCapture := &snapshot.Snapshot{CronJobs: []snapshot.CronJob{
		{Name: "hourly-sync", Namespace: "shop", Schedule: "0 * * * *", LastScheduleTime: "2026-11-01T00:00:00Z"},
	}}
	if got := findByDetector(Diagnose(noCapture), "cronjob-stale"); len(got) != 0 {
		t.Errorf("a snapshot with no capturedAt should not flag stale, got %+v", got)
	}
}

func TestCronJobThatRanIsClean(t *testing.T) {
	s := &snapshot.Snapshot{CronJobs: []snapshot.CronJob{{
		Name: "cleanup", Namespace: "batch", Schedule: "0 2 * * *", LastScheduleTime: "2026-11-13T02:00:00Z", LastSuccessfulTime: "2026-11-13T02:01:00Z",
	}}}
	findings := Diagnose(s)
	if len(findings) != 0 {
		t.Errorf("want no findings, got %+v", findings)
	}
}
