// Package collect builds a snapshot from a live cluster by running
// kubectl with read only verbs: get pods, deployments, nodes, events,
// jobs, and cronjobs, plus a raw get against the metrics API for pod
// usage. It never changes anything in the cluster. Convert is the
// pure part: it turns the kubectl JSON into the snapshot type, and
// the tests drive it with fixture files.
package collect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
)

// Options controls a collection run.
type Options struct {
	Kubeconfig string
	Namespace  string
}

// Runner runs kubectl with the given arguments. It exists so Collect
// can be tested without a cluster.
type Runner interface {
	Run(args ...string) ([]byte, error)
}

// ExecRunner runs the real kubectl binary.
type ExecRunner struct{}

// Run executes kubectl and folds its stderr into the error.
func (ExecRunner) Run(args ...string) ([]byte, error) {
	cmd := exec.Command("kubectl", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return nil, fmt.Errorf("%w: %s", err, detail)
		}
		return nil, err
	}
	return out, nil
}

// lookPath finds the kubectl binary. It is a variable so tests can
// stand in for machines with or without kubectl installed.
var lookPath = exec.LookPath

// Collect runs kubectl and converts the results into a snapshot.
func Collect(r Runner, opts Options) (*snapshot.Snapshot, error) {
	if _, err := lookPath("kubectl"); err != nil {
		return nil, fmt.Errorf("kubectl not found in PATH: install kubectl or add it to PATH, then try again")
	}
	global := []string{}
	if opts.Kubeconfig != "" {
		global = append(global, "--kubeconfig", opts.Kubeconfig)
	}
	run := func(extra ...string) ([]byte, error) {
		return r.Run(append(append([]string{}, global...), extra...)...)
	}

	cluster := ""
	if out, err := run("config", "current-context"); err == nil {
		cluster = strings.TrimSpace(string(out))
	}

	get := func(resource string, namespaced bool) ([]byte, error) {
		args := []string{"get", resource}
		if namespaced {
			args = append(args, scopeArgs(opts.Namespace)...)
		}
		args = append(args, "-o", "json")
		return run(args...)
	}
	pods, err := get("pods", true)
	if err != nil {
		return nil, fmt.Errorf("kubectl get pods: %w", err)
	}
	deployments, err := get("deployments", true)
	if err != nil {
		return nil, fmt.Errorf("kubectl get deployments: %w", err)
	}
	nodes, err := get("nodes", false)
	if err != nil {
		return nil, fmt.Errorf("kubectl get nodes: %w", err)
	}
	events, err := get("events", true)
	if err != nil {
		return nil, fmt.Errorf("kubectl get events: %w", err)
	}
	jobs, err := get("jobs", true)
	if err != nil {
		return nil, fmt.Errorf("kubectl get jobs: %w", err)
	}
	cronjobs, err := get("cronjobs", true)
	if err != nil {
		return nil, fmt.Errorf("kubectl get cronjobs: %w", err)
	}

	// Metrics are optional. Clusters without metrics-server answer
	// this with an error, and a snapshot without usage numbers is
	// still useful, so a failed metrics read is ignored on purpose.
	metricsPath := "/apis/metrics.k8s.io/v1beta1/pods"
	if opts.Namespace != "" {
		metricsPath = "/apis/metrics.k8s.io/v1beta1/namespaces/" + opts.Namespace + "/pods"
	}
	top, err := run("get", "--raw", metricsPath)
	if err != nil {
		top = nil
	}

	capturedAt := time.Now().UTC().Format(time.RFC3339)
	return Convert(cluster, capturedAt, pods, deployments, nodes, events, jobs, cronjobs, top)
}

// scopeArgs returns the namespace arguments for a namespaced get:
// all namespaces by default, or one namespace when one was asked for.
func scopeArgs(namespace string) []string {
	if namespace != "" {
		return []string{"-n", namespace}
	}
	return []string{"-A"}
}

// Convert turns kubectl JSON output into a snapshot. The top argument
// is the metrics API pod list and may be nil. The jobs and cronjobs
// arguments may be nil or empty, for callers that do not collect
// them: the snapshot then carries no jobs or cronjobs, and the job
// detectors stay silent, the same as a hand written snapshot without
// those fields.
func Convert(cluster, capturedAt string, podsJSON, deploymentsJSON, nodesJSON, eventsJSON, jobsJSON, cronJobsJSON, topJSON []byte) (*snapshot.Snapshot, error) {
	usage, err := parseUsage(topJSON)
	if err != nil {
		return nil, err
	}

	var podList podListJSON
	if err := json.Unmarshal(podsJSON, &podList); err != nil {
		return nil, fmt.Errorf("parse kubectl pods output: %w", err)
	}
	var deploymentList deploymentListJSON
	if err := json.Unmarshal(deploymentsJSON, &deploymentList); err != nil {
		return nil, fmt.Errorf("parse kubectl deployments output: %w", err)
	}
	var nodeList nodeListJSON
	if err := json.Unmarshal(nodesJSON, &nodeList); err != nil {
		return nil, fmt.Errorf("parse kubectl nodes output: %w", err)
	}
	var eventList eventListJSON
	if err := json.Unmarshal(eventsJSON, &eventList); err != nil {
		return nil, fmt.Errorf("parse kubectl events output: %w", err)
	}
	var jobList jobListJSON
	if len(jobsJSON) > 0 {
		if err := json.Unmarshal(jobsJSON, &jobList); err != nil {
			return nil, fmt.Errorf("parse kubectl jobs output: %w", err)
		}
	}
	var cronJobList cronJobListJSON
	if len(cronJobsJSON) > 0 {
		if err := json.Unmarshal(cronJobsJSON, &cronJobList); err != nil {
			return nil, fmt.Errorf("parse kubectl cronjobs output: %w", err)
		}
	}

	snap := &snapshot.Snapshot{
		CapturedAt: capturedAt,
		Cluster:    cluster,
	}
	for _, item := range podList.Items {
		snap.Pods = append(snap.Pods, convertPod(item, usage))
	}
	for _, item := range deploymentList.Items {
		snap.Deployments = append(snap.Deployments, snapshot.Deployment{
			Name:              item.Metadata.Name,
			Namespace:         item.Metadata.Namespace,
			Replicas:          item.Spec.Replicas,
			UpdatedReplicas:   item.Status.UpdatedReplicas,
			ReadyReplicas:     item.Status.ReadyReplicas,
			AvailableReplicas: item.Status.AvailableReplicas,
			Conditions:        item.Status.Conditions,
		})
	}
	for _, item := range nodeList.Items {
		snap.Nodes = append(snap.Nodes, snapshot.Node{
			Name:       item.Metadata.Name,
			Conditions: item.Status.Conditions,
		})
	}
	for _, item := range eventList.Items {
		lastSeen := item.LastTimestamp
		if lastSeen == "" {
			lastSeen = item.EventTime
		}
		snap.Events = append(snap.Events, snapshot.Event{
			Type:   item.Type,
			Reason: item.Reason,
			Object: snapshot.Object{
				Kind:      item.InvolvedObject.Kind,
				Namespace: item.InvolvedObject.Namespace,
				Name:      item.InvolvedObject.Name,
			},
			Message:  item.Message,
			Count:    item.Count,
			LastSeen: lastSeen,
		})
	}
	for _, item := range jobList.Items {
		snap.Jobs = append(snap.Jobs, snapshot.Job{
			Name:        item.Metadata.Name,
			Namespace:   item.Metadata.Namespace,
			Completions: item.Spec.Completions,
			Active:      item.Status.Active,
			Succeeded:   item.Status.Succeeded,
			Failed:      item.Status.Failed,
			Conditions:  item.Status.Conditions,
		})
	}
	for _, item := range cronJobList.Items {
		// The API lists active jobs as object references, the
		// snapshot stores only how many there are.
		snap.CronJobs = append(snap.CronJobs, snapshot.CronJob{
			Name:               item.Metadata.Name,
			Namespace:          item.Metadata.Namespace,
			Schedule:           item.Spec.Schedule,
			Suspend:            item.Spec.Suspend,
			LastScheduleTime:   item.Status.LastScheduleTime,
			LastSuccessfulTime: item.Status.LastSuccessfulTime,
			Active:             len(item.Status.Active),
		})
	}
	return snap, nil
}

func convertPod(item podItemJSON, usage map[string]int64) snapshot.Pod {
	pod := snapshot.Pod{
		Name:       item.Metadata.Name,
		Namespace:  item.Metadata.Namespace,
		Node:       item.Spec.NodeName,
		Phase:      item.Status.Phase,
		Labels:     item.Metadata.Labels,
		Conditions: item.Status.Conditions,
	}
	statusByName := make(map[string]containerStatusJSON, len(item.Status.ContainerStatuses))
	for _, cs := range item.Status.ContainerStatuses {
		statusByName[cs.Name] = cs
	}
	for _, spec := range item.Spec.Containers {
		c := snapshot.Container{
			Name:      spec.Name,
			Image:     spec.Image,
			Resources: spec.Resources,
		}
		if cs, ok := statusByName[spec.Name]; ok {
			c.RestartCount = cs.RestartCount
			c.State = cs.State
			c.LastState = cs.LastState
		}
		key := item.Metadata.Namespace + "/" + item.Metadata.Name + "/" + spec.Name
		if bytes, ok := usage[key]; ok {
			c.Metrics.MemoryUsageBytes = bytes
		}
		pod.Containers = append(pod.Containers, c)
	}
	return pod
}

// parseUsage reads the metrics API pod list into a lookup keyed by
// namespace/pod/container. Empty input gives an empty lookup.
func parseUsage(topJSON []byte) (map[string]int64, error) {
	usage := map[string]int64{}
	if len(topJSON) == 0 {
		return usage, nil
	}
	var list topListJSON
	if err := json.Unmarshal(topJSON, &list); err != nil {
		return nil, fmt.Errorf("parse kubectl top output: %w", err)
	}
	for _, item := range list.Items {
		for _, c := range item.Containers {
			if c.Usage.Memory == "" {
				continue
			}
			bytes, err := snapshot.ParseMemory(c.Usage.Memory)
			if err != nil {
				continue
			}
			key := item.Metadata.Namespace + "/" + item.Metadata.Name + "/" + c.Name
			usage[key] = bytes
		}
	}
	return usage, nil
}
