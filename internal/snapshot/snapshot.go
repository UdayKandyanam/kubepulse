// Package snapshot defines the cluster snapshot file format and loads it.
package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
)

// Snapshot is a point in time copy of the parts of cluster state that
// kubepulse reasons about. It is a plain JSON document so it can be
// captured with kubectl, a small script, or by hand in a test.
type Snapshot struct {
	CapturedAt  string       `json:"capturedAt"`
	Cluster     string       `json:"cluster"`
	Pods        []Pod        `json:"pods"`
	Deployments []Deployment `json:"deployments"`
	Nodes       []Node       `json:"nodes"`
	Events      []Event      `json:"events"`
	Jobs        []Job        `json:"jobs,omitempty"`
	CronJobs    []CronJob    `json:"cronJobs,omitempty"`
}

type Pod struct {
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace"`
	Node       string            `json:"node,omitempty"`
	Phase      string            `json:"phase"`
	Labels     map[string]string `json:"labels,omitempty"`
	Containers []Container       `json:"containers"`
	Conditions []Condition       `json:"conditions,omitempty"`
}

type Container struct {
	Name         string         `json:"name"`
	Image        string         `json:"image"`
	RestartCount int            `json:"restartCount"`
	State        ContainerState `json:"state"`
	LastState    ContainerState `json:"lastState,omitempty"`
	Resources    Resources      `json:"resources"`
	Metrics      Metrics        `json:"metrics,omitempty"`
}

type ContainerState struct {
	Waiting    *StateDetail `json:"waiting,omitempty"`
	Running    *StateDetail `json:"running,omitempty"`
	Terminated *StateDetail `json:"terminated,omitempty"`
}

type StateDetail struct {
	Reason   string `json:"reason,omitempty"`
	Message  string `json:"message,omitempty"`
	ExitCode int    `json:"exitCode,omitempty"`
}

// Resources mirrors the parts of a container resources block that the
// detectors use. Memory and CPU are kept as the original quantity
// strings, the same way they appear in a manifest.
type Resources struct {
	Requests ResourceList `json:"requests,omitempty"`
	Limits   ResourceList `json:"limits,omitempty"`
}

type ResourceList struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// Metrics holds usage numbers captured at snapshot time, in bytes.
type Metrics struct {
	MemoryUsageBytes int64 `json:"memoryUsageBytes,omitempty"`
}

type Deployment struct {
	Name              string      `json:"name"`
	Namespace         string      `json:"namespace"`
	Replicas          int         `json:"replicas"`
	UpdatedReplicas   int         `json:"updatedReplicas"`
	ReadyReplicas     int         `json:"readyReplicas"`
	AvailableReplicas int         `json:"availableReplicas"`
	Conditions        []Condition `json:"conditions,omitempty"`
}

type Job struct {
	Name        string      `json:"name"`
	Namespace   string      `json:"namespace"`
	Completions int         `json:"completions,omitempty"`
	Active      int         `json:"active"`
	Succeeded   int         `json:"succeeded"`
	Failed      int         `json:"failed"`
	Conditions  []Condition `json:"conditions,omitempty"`
}

type CronJob struct {
	Name               string `json:"name"`
	Namespace          string `json:"namespace"`
	Schedule           string `json:"schedule,omitempty"`
	Suspend            bool   `json:"suspend,omitempty"`
	LastScheduleTime   string `json:"lastScheduleTime,omitempty"`
	LastSuccessfulTime string `json:"lastSuccessfulTime,omitempty"`
	Active             int    `json:"active"`
}

type Node struct {
	Name       string      `json:"name"`
	Conditions []Condition `json:"conditions,omitempty"`
}

type Condition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// Event is a Kubernetes event, trimmed to the fields the detectors read.
type Event struct {
	Type     string `json:"type"`
	Reason   string `json:"reason"`
	Object   Object `json:"object"`
	Message  string `json:"message"`
	Count    int    `json:"count,omitempty"`
	LastSeen string `json:"lastSeen,omitempty"`
}

type Object struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// Ref returns the object in namespace/name form for pod style objects.
func (p Pod) Ref() string {
	return p.Namespace + "/" + p.Name
}

func (d Deployment) Ref() string {
	return d.Namespace + "/" + d.Name
}

func (j Job) Ref() string {
	return j.Namespace + "/" + j.Name
}

func (c CronJob) Ref() string {
	return c.Namespace + "/" + c.Name
}

// Load reads and parses a snapshot file. Unknown fields are ignored on
// purpose: snapshots produced by different collectors differ in detail.
func Load(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse snapshot %s: %w", path, err)
	}
	return &s, nil
}
