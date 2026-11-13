// Package diagnose runs a set of small detectors over a cluster
// snapshot and returns findings. A detector reads only the snapshot.
// Nothing in this package talks to a cluster, and nothing mutates one.
package diagnose

import (
	"fmt"
	"sort"

	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
)

// Severity ranks how urgently a finding needs a human. The order here
// is the order used for grouping, sorting, and fail-on checks.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

// rank maps a severity to a number so thresholds can be compared.
// Unknown values rank below low.
func rank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	}
	return 0
}

// ParseSeverity validates a severity string, for the fail-on flag.
func ParseSeverity(s string) (Severity, error) {
	sev := Severity(s)
	if rank(sev) == 0 {
		return "", fmt.Errorf("invalid severity %q (use critical, high, medium, or low)", s)
	}
	return sev, nil
}

// AtOrAbove reports whether sev is at least as severe as threshold.
func AtOrAbove(sev, threshold Severity) bool {
	return rank(sev) >= rank(threshold)
}

// Finding is one problem, with the evidence that produced it.
type Finding struct {
	Severity Severity `json:"severity"`
	Detector string   `json:"detector"`
	Object   string   `json:"object"`
	Summary  string   `json:"summary"`
	Evidence string   `json:"evidence"`
	NextStep string   `json:"nextStep"`
}

// Diagnose runs every detector and returns findings sorted by severity,
// then object, then detector, so output is stable between runs.
func Diagnose(s *snapshot.Snapshot) []Finding {
	var findings []Finding
	findings = append(findings, detectContainerStates(s)...)
	findings = append(findings, detectPendingPods(s)...)
	findings = append(findings, detectRollouts(s)...)
	findings = append(findings, detectResourceLimits(s)...)
	findings = append(findings, detectMemoryPressurePods(s)...)
	findings = append(findings, detectNodePressure(s)...)
	sortFindings(findings)
	return findings
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if rank(findings[i].Severity) != rank(findings[j].Severity) {
			return rank(findings[i].Severity) > rank(findings[j].Severity)
		}
		if findings[i].Object != findings[j].Object {
			return findings[i].Object < findings[j].Object
		}
		return findings[i].Detector < findings[j].Detector
	})
}

// ShouldFail reports whether any finding meets the fail-on threshold.
func ShouldFail(findings []Finding, threshold Severity) bool {
	for _, f := range findings {
		if AtOrAbove(f.Severity, threshold) {
			return true
		}
	}
	return false
}

// detectContainerStates covers the waiting and last terminated reasons
// that show up in almost every incident: CrashLoopBackOff, OOMKilled,
// and image pull failures.
func detectContainerStates(s *snapshot.Snapshot) []Finding {
	var out []Finding
	for _, pod := range s.Pods {
		obj := "Pod " + pod.Ref()
		for _, c := range pod.Containers {
			if c.State.Waiting != nil {
				switch c.State.Waiting.Reason {
				case "CrashLoopBackOff":
					out = append(out, Finding{
						Severity: SeverityHigh,
						Detector: "crashloop",
						Object:   obj,
						Summary:  fmt.Sprintf("container %s is in CrashLoopBackOff after %d restarts", c.Name, c.RestartCount),
						Evidence: evidenceOr(c.State.Waiting.Message, "waiting reason CrashLoopBackOff"),
						NextStep: "Read the previous container logs (kubectl logs with the previous flag) and fix the startup failure before raising restart counts further.",
					})
				case "ImagePullBackOff", "ErrImagePull":
					out = append(out, Finding{
						Severity: SeverityHigh,
						Detector: "image-pull",
						Object:   obj,
						Summary:  fmt.Sprintf("container %s cannot pull image %s (%s)", c.Name, c.Image, c.State.Waiting.Reason),
						Evidence: evidenceOr(c.State.Waiting.Message, "waiting reason "+c.State.Waiting.Reason),
						NextStep: "Check the image name and tag, then confirm the node can reach the registry and that the pull secret is present in this namespace.",
					})
				}
			}
			if c.LastState.Terminated != nil && c.LastState.Terminated.Reason == "OOMKilled" {
				out = append(out, Finding{
					Severity: SeverityHigh,
					Detector: "oomkilled",
					Object:   obj,
					Summary:  fmt.Sprintf("container %s was OOMKilled (exit code %d) and has restarted %d times", c.Name, c.LastState.Terminated.ExitCode, c.RestartCount),
					Evidence: fmt.Sprintf("last terminated state reason OOMKilled, memory limit %s", orNone(c.Resources.Limits.Memory)),
					NextStep: "Compare memory usage with the limit. Raise the limit if the workload genuinely needs it, or find the growth in the application if it does not.",
				})
			}
		}
	}
	return out
}

// detectPendingPods flags pods stuck in Pending with a scheduling
// failure on record, either from a FailedScheduling event or from the
// PodScheduled condition.
func detectPendingPods(s *snapshot.Snapshot) []Finding {
	var out []Finding
	for _, pod := range s.Pods {
		if pod.Phase != "Pending" {
			continue
		}
		evidence := ""
		for _, ev := range s.Events {
			if ev.Reason == "FailedScheduling" && ev.Object.Kind == "Pod" &&
				ev.Object.Name == pod.Name && ev.Object.Namespace == pod.Namespace {
				evidence = ev.Message
				break
			}
		}
		if evidence == "" {
			for _, cond := range pod.Conditions {
				if cond.Type == "PodScheduled" && cond.Status == "False" {
					evidence = evidenceOr(cond.Message, "PodScheduled condition is False")
					break
				}
			}
		}
		if evidence == "" {
			continue
		}
		out = append(out, Finding{
			Severity: SeverityMedium,
			Detector: "unschedulable",
			Object:   "Pod " + pod.Ref(),
			Summary:  "pod has been Pending and cannot be scheduled",
			Evidence: evidence,
			NextStep: "Check node capacity, taints and tolerations, and the pod resource requests. A request no node can satisfy will sit here indefinitely.",
		})
	}
	return out
}

// detectRollouts flags deployments where the updated replicas are
// behind the desired count and the rollout has reported no progress.
func detectRollouts(s *snapshot.Snapshot) []Finding {
	var out []Finding
	for _, d := range s.Deployments {
		if d.UpdatedReplicas >= d.Replicas {
			continue
		}
		stuck := false
		evidence := fmt.Sprintf("updated replicas %d of %d desired, ready %d, available %d",
			d.UpdatedReplicas, d.Replicas, d.ReadyReplicas, d.AvailableReplicas)
		for _, cond := range d.Conditions {
			if cond.Type == "Progressing" && cond.Status == "False" {
				stuck = true
				evidence += "; Progressing is False"
				if cond.Reason != "" {
					evidence += " (" + cond.Reason + ")"
				}
				if cond.Message != "" {
					evidence += ": " + cond.Message
				}
			}
		}
		if !stuck {
			continue
		}
		out = append(out, Finding{
			Severity: SeverityHigh,
			Detector: "rollout-stuck",
			Object:   "Deployment " + d.Ref(),
			Summary:  fmt.Sprintf("rollout is stuck at %d of %d updated replicas", d.UpdatedReplicas, d.Replicas),
			Evidence: evidence,
			NextStep: "Look at the newest ReplicaSet pods first. A rollout usually stalls because the new pods are failing, and those pod findings are in this same report.",
		})
	}
	return out
}

// detectResourceLimits flags pods where a container has no CPU or
// memory limit. One finding per pod, naming the containers.
func detectResourceLimits(s *snapshot.Snapshot) []Finding {
	var out []Finding
	for _, pod := range s.Pods {
		var names []string
		for _, c := range pod.Containers {
			if c.Resources.Limits.CPU == "" || c.Resources.Limits.Memory == "" {
				names = append(names, c.Name)
			}
		}
		if len(names) == 0 {
			continue
		}
		out = append(out, Finding{
			Severity: SeverityLow,
			Detector: "no-resource-limits",
			Object:   "Pod " + pod.Ref(),
			Summary:  fmt.Sprintf("containers without resource limits: %s", joinComma(names)),
			Evidence: "limits block is missing cpu or memory for the named containers",
			NextStep: "Set limits from measured usage, then watch for a week. Limits set from a guess become the next incident.",
		})
	}
	return out
}

// detectMemoryPressurePods flags containers whose recorded memory usage
// is at or above 90 percent of their limit.
func detectMemoryPressurePods(s *snapshot.Snapshot) []Finding {
	var out []Finding
	for _, pod := range s.Pods {
		for _, c := range pod.Containers {
			if c.Metrics.MemoryUsageBytes <= 0 || c.Resources.Limits.Memory == "" {
				continue
			}
			limit, err := snapshot.ParseMemory(c.Resources.Limits.Memory)
			if err != nil || limit <= 0 {
				continue
			}
			pct := float64(c.Metrics.MemoryUsageBytes) / float64(limit) * 100
			if pct < 90 {
				continue
			}
			out = append(out, Finding{
				Severity: SeverityMedium,
				Detector: "memory-close-to-limit",
				Object:   "Pod " + pod.Ref(),
				Summary:  fmt.Sprintf("container %s is using %.0f%% of its memory limit", c.Name, pct),
				Evidence: fmt.Sprintf("usage %s of limit %s at snapshot time", formatBytes(c.Metrics.MemoryUsageBytes), c.Resources.Limits.Memory),
				NextStep: "Check whether usage is still climbing. A flat line near the limit is a sizing question; a climb is a leak until proven otherwise.",
			})
		}
	}
	return out
}

// detectNodePressure flags nodes reporting MemoryPressure or
// DiskPressure. Pressure conditions put the whole node at risk, so
// these are the only critical findings kubepulse raises.
func detectNodePressure(s *snapshot.Snapshot) []Finding {
	var out []Finding
	for _, node := range s.Nodes {
		for _, cond := range node.Conditions {
			if (cond.Type == "MemoryPressure" || cond.Type == "DiskPressure") && cond.Status == "True" {
				out = append(out, Finding{
					Severity: SeverityCritical,
					Detector: "node-pressure",
					Object:   "Node " + node.Name,
					Summary:  fmt.Sprintf("node is reporting %s", cond.Type),
					Evidence: evidenceOr(cond.Message, cond.Type+" condition is True"),
					NextStep: "Cordon the node if workloads are being evicted, then find what is consuming the resource. For disk pressure, image and log growth are the usual causes.",
				})
			}
		}
	}
	return out
}

func evidenceOr(message, fallback string) string {
	if message != "" {
		return message
	}
	return fallback
}

func orNone(s string) string {
	if s == "" {
		return "not set"
	}
	return s
}

func joinComma(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ", "
		}
		out += item
	}
	return out
}

func formatBytes(b int64) string {
	const (
		mi = 1024 * 1024
		gi = 1024 * mi
	)
	switch {
	case b >= gi:
		return fmt.Sprintf("%.1fGi", float64(b)/float64(gi))
	case b >= mi:
		return fmt.Sprintf("%.0fMi", float64(b)/float64(mi))
	default:
		return fmt.Sprintf("%dB", b)
	}
}
