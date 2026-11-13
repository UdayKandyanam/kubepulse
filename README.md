# KubePulse

KubePulse reads a JSON snapshot of a Kubernetes cluster and tells you what is wrong with it. One command, no cluster connection, no agents to install.

## Why I built it

The first questions during an incident are always the same. What is crashlooping, what got OOMKilled, which rollout is stuck, is any node under pressure. Answering them usually means a dozen kubectl commands and a lot of scrolling, right when nobody has time for either.

I kept writing the same throwaway checks during incidents, so I put them in one place. KubePulse takes a snapshot of the cluster state, runs a set of small detectors over it, and prints a short report: the problem, the evidence from the snapshot, and the next step I would actually take. It answers the first round of questions in seconds, and then I dig into the one or two findings that matter.

It only diagnoses. It never changes anything in a cluster, and the report says so by construction: there are no write paths in the code at all.

## Install

You need Go 1.23 or newer.

```sh
git clone https://github.com/UdayKandyanam/kubepulse.git
cd kubepulse
make build
```

That produces a `kubepulse` binary in the current directory. `make test` runs the tests and `make vet` runs go vet.

## Quick start

```sh
./kubepulse diagnose examples/healthy.json
./kubepulse diagnose examples/broken.json
./kubepulse diagnose --format json examples/broken.json
./kubepulse diagnose --fail-on high examples/broken.json; echo $?
```

The healthy example prints `No problems found in this snapshot.` The broken example is a small invented cluster with several seeded problems. Its report looks like this:

```text
KubePulse report for demo-cluster
9 findings: 2 critical, 4 high, 2 medium, 1 low

CRITICAL (2)
  Node node-02: node is reporting MemoryPressure
    Evidence: System memory is low
    Next step: Cordon the node if workloads are being evicted, then find what is consuming the resource. For disk pressure, image and log growth are the usual causes.
  Node node-03: node is reporting DiskPressure
    Evidence: Attempting to reclaim inodes
    Next step: Cordon the node if workloads are being evicted, then find what is consuming the resource. For disk pressure, image and log growth are the usual causes.

HIGH (4)
  Deployment shop/checkout: rollout is stuck at 1 of 3 updated replicas
    Evidence: updated replicas 1 of 3 desired, ready 2, available 2; Progressing is False (ProgressDeadlineExceeded): Deployment has failed to progress: timed out after 10m0s
    Next step: Look at the newest ReplicaSet pods first. A rollout usually stalls because the new pods are failing, and those pod findings are in this same report.
  Pod batch/worker-5c7d8e9f6-m4n8q: container worker was OOMKilled (exit code 137) and has restarted 6 times
    Evidence: last terminated state reason OOMKilled, memory limit 512Mi
    Next step: Compare memory usage with the limit. Raise the limit if the workload genuinely needs it, or find the growth in the application if it does not.
  Pod shop/api-7849c6f5d9-xk2lp: container api is in CrashLoopBackOff after 14 restarts
    Evidence: back-off 5m0s restarting failed container api in pod api-7849c6f5d9-xk2lp_shop
    Next step: Read the previous container logs (kubectl logs with the previous flag) and fix the startup failure before raising restart counts further.
  Pod shop/web-6b9d7c4f8-p7r3s: container web cannot pull image registry.example.com/shop/web:v2.4.1 (ImagePullBackOff)
    Evidence: Back-off pulling image "registry.example.com/shop/web:v2.4.1": manifest unknown
    Next step: Check the image name and tag, then confirm the node can reach the registry and that the pull secret is present in this namespace.

MEDIUM (2)
  Pod batch/report-job-8f6e5d4c3-t9w2z: pod has been Pending and cannot be scheduled
    Evidence: 0/3 nodes are available: 3 Insufficient cpu.
    Next step: Check node capacity, taints and tolerations, and the pod resource requests. A request no node can satisfy will sit here indefinitely.
  Pod shop/cache-7f8e6d5c4-j6k9m: container cache is using 94% of its memory limit
    Evidence: usage 480Mi of limit 512Mi at snapshot time
    Next step: Check whether usage is still climbing. A flat line near the limit is a sizing question; a climb is a leak until proven otherwise.

LOW (1)
  Pod tools/debug-shell: containers without resource limits: shell
    Evidence: limits block is missing cpu or memory for the named containers
    Next step: Set limits from measured usage, then watch for a week. Limits set from a guess become the next incident.
```

Findings are grouped by severity: critical, high, medium, low. The JSON format has the same findings in the same order, with a count per severity, so scripts get stable output they can diff or file against a ticket.

Exit codes: `0` normally, `1` when `--fail-on` is set and a finding at or above that severity exists, `2` for a bad file or bad flags. With `--fail-on high`, the broken example exits `1` and the healthy example exits `0`. That makes the command usable as a gate in a script that captures snapshots on a schedule.

## Snapshot format

A snapshot is one JSON object with pods, deployments, nodes, and events. Both files in `examples/` are complete, valid snapshots, and all the names in them are invented.

```json
{
  "capturedAt": "2026-11-13T14:02:00Z",
  "cluster": "demo-cluster",
  "pods": [
    {
      "name": "api-7849c6f5d9-xk2lp",
      "namespace": "shop",
      "node": "node-01",
      "phase": "Running",
      "containers": [
        {
          "name": "api",
          "image": "registry.example.com/shop/api:v1.18.0",
          "restartCount": 14,
          "state": { "waiting": { "reason": "CrashLoopBackOff", "message": "..." } },
          "lastState": { "terminated": { "reason": "Error", "exitCode": 1 } },
          "resources": {
            "requests": { "cpu": "250m", "memory": "256Mi" },
            "limits": { "cpu": "1", "memory": "512Mi" }
          },
          "metrics": { "memoryUsageBytes": 125829120 }
        }
      ]
    }
  ],
  "deployments": [
    { "name": "checkout", "namespace": "shop", "replicas": 3, "updatedReplicas": 1, "readyReplicas": 2, "availableReplicas": 2, "conditions": [] }
  ],
  "nodes": [
    { "name": "node-02", "conditions": [ { "type": "MemoryPressure", "status": "True", "message": "System memory is low" } ] }
  ],
  "events": [
    { "type": "Warning", "reason": "FailedScheduling", "object": { "kind": "Pod", "namespace": "batch", "name": "report-job-8f6e5d4c3-t9w2z" }, "message": "0/3 nodes are available: 3 Insufficient cpu." }
  ]
}
```

Field notes:

* `state` and `lastState` follow the Kubernetes container status shape. Only one of `waiting`, `running`, or `terminated` is set at a time.
* Resource quantities stay as strings (`512Mi`, `1`, `500m`), the way they appear in a manifest. Memory metrics are plain byte counts in `metrics.memoryUsageBytes`.
* Conditions use the usual `type`, `status`, `reason`, `message` fields.
* Unknown fields are ignored, so a collector can include extra data without breaking the tool.

## Detectors

Each detector is a small function over the snapshot, with its own tests. Severities are fixed per detector, apart from node pressure, which is critical because an unhealthy node puts every pod on it at risk.

* `crashloop` (high): a container waiting with reason CrashLoopBackOff.
* `oomkilled` (high): a container whose last terminated state has reason OOMKilled.
* `image-pull` (high): a container waiting with reason ImagePullBackOff or ErrImagePull.
* `unschedulable` (medium): a Pending pod with a FailedScheduling event, or a PodScheduled condition set to False. A Pending pod with neither is left alone; new pods look like that too.
* `rollout-stuck` (high): updated replicas behind the desired count and a Progressing condition set to False. A rollout that is behind but still progressing is not flagged.
* `no-resource-limits` (low): a pod with a container missing a CPU or memory limit. One finding per pod, naming the containers.
* `memory-close-to-limit` (medium): memory usage at or above 90 percent of the container limit, from the metrics fields.
* `node-pressure` (critical): a node reporting MemoryPressure or DiskPressure.

## Design choices

Why a snapshot file instead of a live cluster connection: a file works anywhere, including from a laptop with no cluster access, and it can be attached to an incident ticket so the report can be checked later against exactly the state that produced it. It also makes the detectors trivial to test. Live capture is a collector problem, and collectors can be swapped without touching the diagnosis code.

Why diagnose only: the moment a tool can change things, every run needs a permissions conversation, and the tool stops being something you reach for casually at 3 AM. Reading state and suggesting next steps keeps it safe to run anywhere. The next step is advice for a human, not an action the tool takes.

## Limits

* The report is only as good as the snapshot. Stale input gives a confident looking report about a cluster that has already moved on.
* The memory detector needs usage numbers in the snapshot. Without `metrics` fields it stays silent rather than guessing.
* The 90 percent memory threshold and the severity assignments are fixed in code. Reasonable defaults, but they are not tuned to any particular cluster.
* KubePulse does not read logs, so for a crashloop it can point at the container and the restart count, not at the stack trace.

## Roadmap

* A collector subcommand that builds a snapshot from kubectl output, so the file does not have to be assembled by hand.
* A diff mode that compares two snapshots and reports what changed between them.
* A small config file for thresholds and severity overrides, for teams that disagree with my defaults.

## License

MIT, see `LICENSE`.
