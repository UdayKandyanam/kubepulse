package collect

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/UdayKandyanam/kubepulse/internal/diagnose"
	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func convertFixtures(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	snap, err := Convert("demo-cluster", "2026-11-13T14:02:00Z",
		fixture(t, "pods.json"), fixture(t, "deployments.json"),
		fixture(t, "nodes.json"), fixture(t, "events.json"), fixture(t, "top.json"))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	return snap
}

func TestConvertPods(t *testing.T) {
	snap := convertFixtures(t)
	if snap.Cluster != "demo-cluster" || snap.CapturedAt != "2026-11-13T14:02:00Z" {
		t.Errorf("header fields lost: cluster %q capturedAt %q", snap.Cluster, snap.CapturedAt)
	}
	if len(snap.Pods) != 2 {
		t.Fatalf("want 2 pods, got %d", len(snap.Pods))
	}
	web := snap.Pods[0]
	if web.Name != "web-5d6c7b8f9-h3j5k" || web.Namespace != "shop" || web.Node != "node-01" || web.Phase != "Running" {
		t.Errorf("web pod header wrong: %+v", web)
	}
	if len(web.Containers) != 1 {
		t.Fatalf("web pod should have 1 container, got %d", len(web.Containers))
	}
	wc := web.Containers[0]
	if wc.State.Running == nil {
		t.Error("web container should be running")
	}
	if wc.Resources.Limits.Memory != "256Mi" || wc.Resources.Requests.CPU != "100m" {
		t.Errorf("web resources wrong: %+v", wc.Resources)
	}
	// 102400Ki from the metrics fixture is exactly 100Mi in bytes.
	if wc.Metrics.MemoryUsageBytes != 100*1024*1024 {
		t.Errorf("web memory usage should come from the metrics output, got %d bytes", wc.Metrics.MemoryUsageBytes)
	}

	api := snap.Pods[1].Containers[0]
	if api.State.Waiting == nil || api.State.Waiting.Reason != "CrashLoopBackOff" {
		t.Errorf("api container should be waiting with CrashLoopBackOff, got %+v", api.State)
	}
	if api.LastState.Terminated == nil || api.LastState.Terminated.ExitCode != 1 {
		t.Errorf("api last state should be terminated with exit code 1, got %+v", api.LastState)
	}
	if api.RestartCount != 14 {
		t.Errorf("api restart count should be 14, got %d", api.RestartCount)
	}
}

func TestConvertDeploymentsNodesEvents(t *testing.T) {
	snap := convertFixtures(t)
	if len(snap.Deployments) != 2 {
		t.Fatalf("want 2 deployments, got %d", len(snap.Deployments))
	}
	checkout := snap.Deployments[1]
	if checkout.Name != "checkout" || checkout.Replicas != 3 || checkout.UpdatedReplicas != 1 {
		t.Errorf("checkout deployment wrong: %+v", checkout)
	}
	if len(snap.Nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(snap.Nodes))
	}
	if len(snap.Events) != 2 {
		t.Fatalf("want 2 events, got %d", len(snap.Events))
	}
	failed := snap.Events[1]
	if failed.Reason != "FailedScheduling" || failed.Object.Kind != "Pod" ||
		failed.Object.Namespace != "batch" || failed.LastSeen != "2026-11-13T14:01:00Z" {
		t.Errorf("scheduling event converted wrong: %+v", failed)
	}
}

func TestConvertedSnapshotDiagnoses(t *testing.T) {
	snap := convertFixtures(t)
	findings := diagnose.Diagnose(snap)
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.Detector]++
	}
	if counts["crashloop"] != 1 {
		t.Errorf("want 1 crashloop finding from the converted snapshot, got %v", counts)
	}
	if counts["rollout-stuck"] != 1 {
		t.Errorf("want 1 rollout finding from the converted snapshot, got %v", counts)
	}
	if counts["node-pressure"] != 1 {
		t.Errorf("want 1 node pressure finding from the converted snapshot, got %v", counts)
	}
}

func TestConvertWithoutMetrics(t *testing.T) {
	snap, err := Convert("demo-cluster", "2026-11-13T14:02:00Z",
		fixture(t, "pods.json"), fixture(t, "deployments.json"),
		fixture(t, "nodes.json"), fixture(t, "events.json"), nil)
	if err != nil {
		t.Fatalf("Convert without metrics: %v", err)
	}
	if snap.Pods[0].Containers[0].Metrics.MemoryUsageBytes != 0 {
		t.Error("usage should be zero when the metrics output is missing")
	}
}

func TestConvertRejectsBadJSON(t *testing.T) {
	if _, err := Convert("demo-cluster", "now", []byte("{not json"),
		fixture(t, "deployments.json"), fixture(t, "nodes.json"),
		fixture(t, "events.json"), nil); err == nil {
		t.Error("bad pods JSON should produce an error")
	}
}

// fakeRunner answers kubectl calls from the fixture files and records
// the argument lists it was given.
type fakeRunner struct {
	calls [][]string
	fail  map[string]bool
}

func (f *fakeRunner) Run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{}, args...))
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "current-context"):
		return []byte("demo-cluster\n"), nil
	case strings.Contains(joined, "--raw"):
		if f.fail["metrics"] {
			return nil, fmt.Errorf("the server could not find the requested resource")
		}
		return fixtureData("top.json")
	case strings.Contains(joined, "get pods"):
		return fixtureData("pods.json")
	case strings.Contains(joined, "get deployments"):
		return fixtureData("deployments.json")
	case strings.Contains(joined, "get nodes"):
		return fixtureData("nodes.json")
	case strings.Contains(joined, "get events"):
		return fixtureData("events.json")
	}
	return nil, fmt.Errorf("unexpected kubectl call: %s", joined)
}

func fixtureData(name string) ([]byte, error) {
	return os.ReadFile("testdata/" + name)
}

// stubKubectlFound makes the kubectl binary check pass for the
// duration of a test, whatever the machine really has installed.
func stubKubectlFound(t *testing.T) {
	t.Helper()
	real := lookPath
	lookPath = func(string) (string, error) { return "/usr/local/bin/kubectl", nil }
	t.Cleanup(func() { lookPath = real })
}

func TestCollectUsesReadOnlyGets(t *testing.T) {
	stubKubectlFound(t)
	runner := &fakeRunner{}
	snap, err := Collect(runner, Options{Namespace: "shop"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if snap.Cluster != "demo-cluster" {
		t.Errorf("cluster should come from the current context, got %q", snap.Cluster)
	}
	if len(snap.Pods) != 2 {
		t.Errorf("want 2 pods from the fake cluster, got %d", len(snap.Pods))
	}
	for _, call := range runner.calls {
		joined := strings.Join(call, " ")
		if !strings.Contains(joined, "get") && !strings.Contains(joined, "current-context") {
			t.Errorf("kubectl call should be a get or a config read, got %q", joined)
		}
		if strings.Contains(joined, "-n shop") == false && strings.Contains(joined, "nodes") == false &&
			!strings.Contains(joined, "current-context") && !strings.Contains(joined, "--raw") {
			t.Errorf("namespaced get should carry the namespace flag, got %q", joined)
		}
	}
}

func TestCollectToleratesMissingMetrics(t *testing.T) {
	stubKubectlFound(t)
	runner := &fakeRunner{fail: map[string]bool{"metrics": true}}
	snap, err := Collect(runner, Options{})
	if err != nil {
		t.Fatalf("Collect should tolerate a failed metrics read: %v", err)
	}
	if snap.Pods[0].Containers[0].Metrics.MemoryUsageBytes != 0 {
		t.Error("usage should be empty when the metrics read failed")
	}
}

func TestCollectErrorsClearlyWithoutKubectl(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := Collect(ExecRunner{}, Options{})
	if err == nil {
		t.Fatal("Collect should fail when kubectl is not on PATH")
	}
	if !strings.Contains(err.Error(), "kubectl not found in PATH") {
		t.Errorf("error should name the problem plainly, got %q", err.Error())
	}
}
