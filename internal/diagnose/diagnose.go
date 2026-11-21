// Package diagnose runs a set of small detectors over a cluster
// snapshot and returns findings. A detector reads only the snapshot.
// Nothing in this package talks to a cluster, and nothing mutates one.
package diagnose

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

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

// Diagnose runs every detector with the default config and returns
// findings sorted by severity, then object, then detector, so output
// is stable between runs.
func Diagnose(s *snapshot.Snapshot) []Finding {
	return DiagnoseWithConfig(s, DefaultConfig())
}

// DiagnoseWithConfig runs every detector with the given config. The
// config sets the memory threshold the memory detector uses, any
// severity override replaces the severity a detector assigned before
// the findings are sorted, and a detector listed as disabled in the
// config contributes no findings at all.
func DiagnoseWithConfig(s *snapshot.Snapshot, cfg Config) []Finding {
	var findings []Finding
	findings = append(findings, detectContainerStates(s)...)
	findings = append(findings, detectPendingPods(s)...)
	findings = append(findings, detectRollouts(s)...)
	findings = append(findings, detectResourceLimits(s)...)
	findings = append(findings, detectMemoryPressurePods(s, cfg.MemoryThresholdPercent)...)
	findings = append(findings, detectNodePressure(s)...)
	findings = append(findings, detectJobs(s)...)
	findings = append(findings, detectCronJobs(s)...)
	var kept []Finding
	for _, f := range findings {
		if cfg.IsDisabled(f.Detector) {
			continue
		}
		if sev, ok := cfg.SeverityOverrides[f.Detector]; ok {
			f.Severity = sev
		}
		kept = append(kept, f)
	}
	sortFindings(kept)
	return kept
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
// is at or above thresholdPercent of their limit. The default
// threshold is 90 percent, set in DefaultConfig.
func detectMemoryPressurePods(s *snapshot.Snapshot, thresholdPercent float64) []Finding {
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
			if pct < thresholdPercent {
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

// detectJobs flags jobs that have failed for good. A job counts as
// failed when it carries a Failed condition set to True, or when it
// has failed pods, nothing still active, and no Complete condition.
// A job that is still running with some failed pods is left alone:
// its retries may still succeed.
func detectJobs(s *snapshot.Snapshot) []Finding {
	var out []Finding
	for _, j := range s.Jobs {
		failed := false
		complete := false
		evidence := ""
		for _, cond := range j.Conditions {
			if cond.Type == "Failed" && cond.Status == "True" {
				failed = true
				evidence = evidenceOr(cond.Message, "Failed condition is True")
				if cond.Reason != "" {
					evidence = cond.Reason + ": " + evidence
				}
			}
			if cond.Type == "Complete" && cond.Status == "True" {
				complete = true
			}
		}
		if !failed && !complete && j.Failed > 0 && j.Active == 0 {
			failed = true
			evidence = fmt.Sprintf("failed pods %d, succeeded %d, active %d, with no Complete condition", j.Failed, j.Succeeded, j.Active)
		}
		if !failed {
			continue
		}
		if evidence == "" {
			evidence = fmt.Sprintf("failed pods %d, succeeded %d, active %d", j.Failed, j.Succeeded, j.Active)
		} else if j.Failed > 0 {
			evidence += fmt.Sprintf("; failed pods %d, succeeded %d", j.Failed, j.Succeeded)
		}
		out = append(out, Finding{
			Severity: SeverityHigh,
			Detector: "job-failed",
			Object:   "Job " + j.Ref(),
			Summary:  "job has failed and will not run again without a change",
			Evidence: evidence,
			NextStep: "Read the logs of a failed pod from this job, fix the cause, then delete and recreate the job. A failed job does not retry on its own.",
		})
	}
	return out
}

// detectCronJobs flags cronjobs that are suspended, cronjobs that
// have never been scheduled, and cronjobs that ran before but have
// stopped running on schedule. A suspended cronjob is not also
// flagged as never ran or stale: the suspension already explains
// the missing runs.
func detectCronJobs(s *snapshot.Snapshot) []Finding {
	var out []Finding
	for _, cj := range s.CronJobs {
		if cj.Suspend {
			evidence := "suspend is true"
			if cj.Schedule != "" {
				evidence += " for schedule " + cj.Schedule
			}
			if cj.LastScheduleTime != "" {
				evidence += "; last scheduled at " + cj.LastScheduleTime
			}
			out = append(out, Finding{
				Severity: SeverityMedium,
				Detector: "cronjob-suspended",
				Object:   "CronJob " + cj.Ref(),
				Summary:  "cronjob is suspended and is not creating jobs",
				Evidence: evidence,
				NextStep: "Check whether the suspension was on purpose. If it was a pause during an incident, set suspend back to false and confirm the next scheduled run creates a job.",
			})
			continue
		}
		if cj.LastScheduleTime == "" {
			evidence := "lastScheduleTime is empty"
			if cj.Schedule != "" {
				evidence += " for schedule " + cj.Schedule
			}
			out = append(out, Finding{
				Severity: SeverityMedium,
				Detector: "cronjob-never-ran",
				Object:   "CronJob " + cj.Ref(),
				Summary:  "cronjob has never been scheduled",
				Evidence: evidence,
				NextStep: "Check that the cronjob is in the namespace I expect and that its schedule is valid. If it was just created, wait for the first scheduled time and check again.",
			})
			continue
		}
		if age, interval, ok := staleAge(s.CapturedAt, cj); ok {
			out = append(out, Finding{
				Severity: SeverityMedium,
				Detector: "cronjob-stale",
				Object:   "CronJob " + cj.Ref(),
				Summary:  fmt.Sprintf("cronjob has not run on schedule, last run %s ago", formatDuration(age)),
				Evidence: fmt.Sprintf("last scheduled at %s, snapshot captured at %s, schedule %q runs every %s", cj.LastScheduleTime, s.CapturedAt, cj.Schedule, formatDuration(interval)),
				NextStep: "Check the cronjob controller and the job history for this cronjob. A run that stopped on schedule usually means the controller is down, the schedule stopped matching, or new jobs are being blocked.",
			})
		}
	}
	return out
}

// staleAge reports whether a cronjob has missed its schedule. It
// returns the time since the last scheduled run and the interval
// between scheduled runs. A cronjob counts as stale when the time
// since its last run is more than twice the interval, so at least
// one full scheduled run has been missed. Times or schedules that
// cannot be parsed return ok as false: without them there is no
// honest way to judge.
func staleAge(capturedAt string, cj snapshot.CronJob) (time.Duration, time.Duration, bool) {
	last, err := time.Parse(time.RFC3339, cj.LastScheduleTime)
	if err != nil {
		return 0, 0, false
	}
	captured, err := time.Parse(time.RFC3339, capturedAt)
	if err != nil {
		return 0, 0, false
	}
	interval, ok := scheduleInterval(cj.Schedule, last)
	if !ok {
		return 0, 0, false
	}
	age := captured.Sub(last)
	if age <= 2*interval {
		return 0, 0, false
	}
	return age, interval, true
}

// scheduleInterval returns the gap between the first two scheduled
// times after from, by scanning forward minute by minute. Cron
// schedules repeat at least once a year, so the scan stops after
// 366 days.
type cronSchedule struct {
	fields  []map[int]bool
	domStar bool
	dowStar bool
}

func scheduleInterval(schedule string, from time.Time) (time.Duration, bool) {
	parsed, ok := parseCron(schedule)
	if !ok {
		return 0, false
	}
	first, ok := nextCronTime(parsed, from)
	if !ok {
		return 0, false
	}
	second, ok := nextCronTime(parsed, first)
	if !ok {
		return 0, false
	}
	return second.Sub(first), true
}

func nextCronTime(schedule cronSchedule, from time.Time) (time.Time, bool) {
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := from.Add(366 * 24 * time.Hour)
	for !t.After(limit) {
		if cronMatches(schedule, t) {
			return t, true
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, false
}

// parseCron parses a standard five field cron schedule. Each field
// supports a star, a step such as */15, a single value, a range, and
// comma separated lists of those. It returns one set of matching
// values per field, in minute, hour, day of month, month, and day
// of week order.
func parseCron(schedule string) (cronSchedule, bool) {
	parts := strings.Fields(schedule)
	if len(parts) != 5 {
		return cronSchedule{}, false
	}
	bounds := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	fields := make([]map[int]bool, 5)
	for i, part := range parts {
		values, ok := parseCronField(part, bounds[i][0], bounds[i][1])
		if !ok {
			return cronSchedule{}, false
		}
		fields[i] = values
	}
	if fields[4][7] {
		fields[4][0] = true
		delete(fields[4], 7)
	}
	parsed := cronSchedule{
		fields:  fields,
		domStar: parts[2] == "*" || strings.HasPrefix(parts[2], "*/"),
		dowStar: parts[4] == "*" || strings.HasPrefix(parts[4], "*/"),
	}
	return parsed, true
}

func parseCronField(field string, min, max int) (map[int]bool, bool) {
	values := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		if part == "" {
			return nil, false
		}
		base := part
		step := 1
		if slash := strings.Index(part, "/"); slash >= 0 {
			base = part[:slash]
			n, err := strconv.Atoi(part[slash+1:])
			if err != nil || n < 1 {
				return nil, false
			}
			step = n
		}
		lo, hi := min, max
		if base != "*" {
			if dash := strings.Index(base, "-"); dash >= 0 {
				var err error
				lo, err = strconv.Atoi(base[:dash])
				if err != nil {
					return nil, false
				}
				hi, err = strconv.Atoi(base[dash+1:])
				if err != nil {
					return nil, false
				}
			} else {
				v, err := strconv.Atoi(base)
				if err != nil {
					return nil, false
				}
				lo, hi = v, v
				if step != 1 {
					hi = max
				}
			}
		}
		if lo < min || hi > max || lo > hi {
			return nil, false
		}
		for v := lo; v <= hi; v += step {
			values[v] = true
		}
	}
	if len(values) == 0 {
		return nil, false
	}
	return values, true
}

func cronMatches(schedule cronSchedule, t time.Time) bool {
	fields := schedule.fields
	if !fields[0][t.Minute()] || !fields[1][t.Hour()] || !fields[3][int(t.Month())] {
		return false
	}
	dom := fields[2][t.Day()]
	dow := fields[4][int(t.Weekday())]
	switch {
	case schedule.domStar && schedule.dowStar:
		return true
	case schedule.domStar:
		return dow
	case schedule.dowStar:
		return dom
	default:
		return dom || dow
	}
}

// formatDuration prints a duration the way I would say it in a
// report: whole days, hours, or minutes, whichever is the largest
// unit that fits without a fraction.
func formatDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
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
