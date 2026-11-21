# KubePulse

KubePulse reads a JSON snapshot of a Kubernetes cluster and tells you what is wrong with it. It can also build that snapshot for you with read only kubectl calls, and compare two snapshots to show what changed. No agents to install, and nothing in it changes a cluster.

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
./kubepulse diagnose --config examples/thresholds.yaml examples/broken.json
./kubepulse diagnose --print-config --config examples/thresholds.yaml
./kubepulse diff examples/healthy.json examples/broken.json
./kubepulse collect --output snapshot.json
```

The healthy example prints `No problems found in this snapshot.` The broken example is a small invented cluster with several seeded problems. Its report looks like this:

```text
KubePulse report for demo-cluster
12 findings: 2 critical, 5 high, 4 medium, 1 low

CRITICAL (2)
  Node node-02: node is reporting MemoryPressure
    Evidence: System memory is low
    Next step: Cordon the node if workloads are being evicted, then find what is consuming the resource. For disk pressure, image and log growth are the usual causes.
  Node node-03: node is reporting DiskPressure
    Evidence: Attempting to reclaim inodes
    Next step: Cordon the node if workloads are being evicted, then find what is consuming the resource. For disk pressure, image and log growth are the usual causes.

HIGH (5)
  Deployment shop/checkout: rollout is stuck at 1 of 3 updated replicas
    Evidence: updated replicas 1 of 3 desired, ready 2, available 2; Progressing is False (ProgressDeadlineExceeded): Deployment has failed to progress: timed out after 10m0s
    Next step: Look at the newest ReplicaSet pods first. A rollout usually stalls because the new pods are failing, and those pod findings are in this same report.
  Job batch/nightly-report: job has failed and will not run again without a change
    Evidence: BackoffLimitExceeded: Job has reached the specified backoff limit; failed pods 3, succeeded 0
    Next step: Read the logs of a failed pod from this job, fix the cause, then delete and recreate the job. A failed job does not retry on its own.
  Pod batch/worker-5c7d8e9f6-m4n8q: container worker was OOMKilled (exit code 137) and has restarted 6 times
    Evidence: last terminated state reason OOMKilled, memory limit 512Mi
    Next step: Compare memory usage with the limit. Raise the limit if the workload genuinely needs it, or find the growth in the application if it does not.
  Pod shop/api-7849c6f5d9-xk2lp: container api is in CrashLoopBackOff after 14 restarts
    Evidence: back-off 5m0s restarting failed container api in pod api-7849c6f5d9-xk2lp_shop
    Next step: Read the previous container logs (kubectl logs with the previous flag) and fix the startup failure before raising restart counts further.
  Pod shop/web-6b9d7c4f8-p7r3s: container web cannot pull image registry.example.com/shop/web:v2.4.1 (ImagePullBackOff)
    Evidence: Back-off pulling image "registry.example.com/shop/web:v2.4.1": manifest unknown
    Next step: Check the image name and tag, then confirm the node can reach the registry and that the pull secret is present in this namespace.

MEDIUM (4)
  CronJob batch/cleanup: cronjob is suspended and is not creating jobs
    Evidence: suspend is true for schedule 0 2 * * *; last scheduled at 2026-11-10T02:00:00Z
    Next step: Check whether the suspension was on purpose. If it was a pause during an incident, set suspend back to false and confirm the next scheduled run creates a job.
  CronJob shop/hourly-sync: cronjob has never been scheduled
    Evidence: lastScheduleTime is empty for schedule 0 * * * *
    Next step: Check that the cronjob is in the namespace I expect and that its schedule is valid. If it was just created, wait for the first scheduled time and check again.
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

## Collect a snapshot from a cluster

`collect` builds a snapshot from a live cluster so the file does not have to be assembled by hand:

```sh
./kubepulse collect --output snapshot.json
./kubepulse collect --namespace shop --kubeconfig ~/.kube/config
./kubepulse diagnose snapshot.json
```

It runs kubectl with read only verbs: `get` for pods, deployments, nodes, events, jobs, and cronjobs, plus a raw `get` against the metrics API for pod memory usage. Jobs and cronjobs are collected the same way as the other resources, so the job and cronjob detectors run on collected snapshots too, not only on the examples or a snapshot assembled by hand. A cronjob lists its active jobs as object references in the API; the snapshot stores only the count, which is all the detectors read. The cluster name in the snapshot is the current context name, and the capture time is stamped when the command runs. Pod usage is optional: on a cluster without metrics-server that one read fails, and the snapshot is still written, just without usage numbers. Without `--output` the snapshot goes to stdout. If kubectl is not installed the command stops and says so: `kubepulse: kubectl not found in PATH: install kubectl or add it to PATH, then try again`.

## Compare two snapshots

`diff` diagnoses two snapshots and compares the findings. A finding matches across snapshots on its detector, object, and summary. The report has three sections:

```sh
./kubepulse diff examples/healthy.json examples/broken.json
```

```text
KubePulse diff for demo-cluster
12 new, 0 resolved, 0 unchanged

NEW (12)
  Node node-02: node is reporting MemoryPressure
  Node node-03: node is reporting DiskPressure
  Deployment shop/checkout: rollout is stuck at 1 of 3 updated replicas
  Job batch/nightly-report: job has failed and will not run again without a change
  Pod batch/worker-5c7d8e9f6-m4n8q: container worker was OOMKilled (exit code 137) and has restarted 6 times
  Pod shop/api-7849c6f5d9-xk2lp: container api is in CrashLoopBackOff after 14 restarts
  Pod shop/web-6b9d7c4f8-p7r3s: container web cannot pull image registry.example.com/shop/web:v2.4.1 (ImagePullBackOff)
  CronJob batch/cleanup: cronjob is suspended and is not creating jobs
  CronJob shop/hourly-sync: cronjob has never been scheduled
  Pod batch/report-job-8f6e5d4c3-t9w2z: pod has been Pending and cannot be scheduled
  Pod shop/cache-7f8e6d5c4-j6k9m: container cache is using 94% of its memory limit
  Pod tools/debug-shell: containers without resource limits: shell

RESOLVED (0)
  None

UNCHANGED (0)
  None
```

Run the same pair in reverse and the 12 findings land under RESOLVED instead. The JSON format has the same three groups as arrays named `new`, `resolved`, and `unchanged`, with counts in a summary block. `--fail-on` works here too, but it only looks at new findings: a diff where everything resolved exits `0` even at `--fail-on low`, and the healthy to broken diff above exits `1` at `--fail-on high`. That is the shape I want for a scheduled job: collect on an interval, diff against the last snapshot, and page only when something new crosses the line.

## Tune thresholds with a config file

My defaults will not fit every cluster. A config file changes three things: the percent of its memory limit at which a container gets flagged, the severity any detector reports, and whether a detector runs at all. Both `diagnose` and `diff` take one with `--config`.

```yaml
# examples/thresholds.yaml
memory_threshold_percent: 80

severities:
  no-resource-limits: medium
```

The format is a small subset of YAML that I parse with the standard library, so the tool still has no dependencies. Anything left out keeps its default. Unknown keys, unknown detector names, and bad severities are errors with the line number named, and a missing file is an error too: silently falling back to defaults would hide the mistake. Running the broken example with this file moves the no resource limits finding from low to medium:

```text
KubePulse report for demo-cluster
12 findings: 2 critical, 5 high, 5 medium, 0 low

MEDIUM (5)
  CronJob batch/cleanup: cronjob is suspended and is not creating jobs
    Evidence: suspend is true for schedule 0 2 * * *; last scheduled at 2026-11-10T02:00:00Z
    Next step: Check whether the suspension was on purpose. If it was a pause during an incident, set suspend back to false and confirm the next scheduled run creates a job.
  CronJob shop/hourly-sync: cronjob has never been scheduled
    Evidence: lastScheduleTime is empty for schedule 0 * * * *
    Next step: Check that the cronjob is in the namespace I expect and that its schedule is valid. If it was just created, wait for the first scheduled time and check again.
  Pod batch/report-job-8f6e5d4c3-t9w2z: pod has been Pending and cannot be scheduled
    Evidence: 0/3 nodes are available: 3 Insufficient cpu.
    Next step: Check node capacity, taints and tolerations, and the pod resource requests. A request no node can satisfy will sit here indefinitely.
  Pod shop/cache-7f8e6d5c4-j6k9m: container cache is using 94% of its memory limit
    Evidence: usage 480Mi of limit 512Mi at snapshot time
    Next step: Check whether usage is still climbing. A flat line near the limit is a sizing question; a climb is a leak until proven otherwise.
  Pod tools/debug-shell: containers without resource limits: shell
    Evidence: limits block is missing cpu or memory for the named containers
    Next step: Set limits from measured usage, then watch for a week. Limits set from a guess become the next incident.
```

That last finding is the same pod the default report puts under LOW (1). Overrides apply before sorting and before the `--fail-on` check, so raising a detector to critical makes `--fail-on high` exit 1 on it as well.

A detector can also be turned off. That is the right call when a team has already accepted a finding class and the noise is hiding new problems:

```yaml
disabled_detectors:
  no-resource-limits: true
```

A detector set to `false`, or not listed, stays on. Unknown detector names and values other than `true` or `false` are errors with the line number named, the same as the other settings. Running the broken example with only the file above gives:

```text
KubePulse report for demo-cluster
11 findings: 2 critical, 5 high, 4 medium, 0 low
```

The low finding is gone, not moved: a disabled detector contributes no findings, so it cannot trigger `--fail-on` either. In `diff`, disabling a detector removes it from both sides, so it shows up as neither new nor resolved.

To see the settings a run will use, add `--print-config`. It prints the effective config and exits without reading a snapshot, so I can check the merged result of a file and the defaults before I trust a report. Both `diagnose` and `diff` take it. With the example file it prints:

```text
$ ./kubepulse diagnose --print-config --config examples/thresholds.yaml
Effective config
memory_threshold_percent: 80
detectors:
  crashloop: high, enabled
  cronjob-never-ran: medium, enabled
  cronjob-stale: medium, enabled
  cronjob-suspended: medium, enabled
  image-pull: high, enabled
  job-failed: high, enabled
  memory-close-to-limit: medium, enabled
  no-resource-limits: medium (override), enabled
  node-pressure: critical, enabled
  oomkilled: high, enabled
  rollout-stuck: high, enabled
  unschedulable: medium, enabled
```

Without a `--config` file the same command lists the defaults: memory threshold 90, every detector at the severity in the detector list below, and every detector enabled. A bad config file is still an error here, with the line number named, instead of a listing of defaults I did not ask for.

## Snapshot format

A snapshot is one JSON object with pods, deployments, nodes, events, jobs, and cronjobs. The jobs and cronjobs fields are optional: a snapshot without them still loads, and the job detectors stay silent. Both files in `examples/` are complete, valid snapshots, and all the names in them are invented.

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
  ],
  "jobs": [
    { "name": "nightly-report", "namespace": "batch", "completions": 1, "active": 0, "succeeded": 0, "failed": 3, "conditions": [ { "type": "Failed", "status": "True", "reason": "BackoffLimitExceeded", "message": "Job has reached the specified backoff limit" } ] }
  ],
  "cronJobs": [
    { "name": "cleanup", "namespace": "batch", "schedule": "0 2 * * *", "suspend": true, "lastScheduleTime": "2026-11-10T02:00:00Z", "active": 0 }
  ]
}
```

Field notes:

* `state` and `lastState` follow the Kubernetes container status shape. Only one of `waiting`, `running`, or `terminated` is set at a time.
* Resource quantities stay as strings (`512Mi`, `1`, `500m`), the way they appear in a manifest. Memory metrics are plain byte counts in `metrics.memoryUsageBytes`.
* Conditions use the usual `type`, `status`, `reason`, `message` fields.
* Unknown fields are ignored, so a collector can include extra data without breaking the tool.
* Job counts are plain numbers (`active`, `succeeded`, `failed`). A cronjob with no runs yet has an empty `lastScheduleTime`; that is the signal the never ran detector reads.

## Detectors

Each detector is a small function over the snapshot, with its own tests. The severities below are the defaults: a config file can override any of them, or turn a detector off, as the config section above describes. Node pressure defaults to critical because an unhealthy node puts every pod on it at risk.

* `crashloop` (high): a container waiting with reason CrashLoopBackOff.
* `oomkilled` (high): a container whose last terminated state has reason OOMKilled.
* `image-pull` (high): a container waiting with reason ImagePullBackOff or ErrImagePull.
* `unschedulable` (medium): a Pending pod with a FailedScheduling event, or a PodScheduled condition set to False. A Pending pod with neither is left alone; new pods look like that too.
* `rollout-stuck` (high): updated replicas behind the desired count and a Progressing condition set to False. A rollout that is behind but still progressing is not flagged.
* `no-resource-limits` (low): a pod with a container missing a CPU or memory limit. One finding per pod, naming the containers.
* `memory-close-to-limit` (medium): memory usage at or above 90 percent of the container limit by default, from the metrics fields. A config file can move that percent.
* `node-pressure` (critical): a node reporting MemoryPressure or DiskPressure.
* `job-failed` (high): a job with a Failed condition set to True, or with failed pods, nothing still active, and no Complete condition. A job that is still running is left alone, because its retries may still succeed.
* `cronjob-suspended` (medium): a cronjob with `suspend` set to true. It is not also flagged as never ran, because the suspension explains the missing runs.
* `cronjob-never-ran` (medium): a cronjob that is not suspended and has an empty `lastScheduleTime`.
* `cronjob-stale` (medium): a cronjob that ran before but has stopped running on schedule. I compare `lastScheduleTime` with the snapshot `capturedAt` and work out the interval from the cron schedule itself: a cronjob counts as stale when the time since its last run is more than twice that interval, so at least one full scheduled run has been missed. A snapshot with an hourly cronjob last run at 08:00 and captured at 14:02 gives:

```text
KubePulse report for demo-cluster
1 findings: 0 critical, 0 high, 1 medium, 0 low

MEDIUM (1)
  CronJob shop/hourly-sync: cronjob has not run on schedule, last run 6h ago
    Evidence: last scheduled at 2026-11-13T08:00:00Z, snapshot captured at 2026-11-13T14:02:00Z, schedule "0 * * * *" runs every 1h
    Next step: Check the cronjob controller and the job history for this cronjob. A run that stopped on schedule usually means the controller is down, the schedule stopped matching, or new jobs are being blocked.
```

## Design choices

Why a snapshot file instead of a live cluster connection: a file works anywhere, including from a laptop with no cluster access, and it can be attached to an incident ticket so the report can be checked later against exactly the state that produced it. It also makes the detectors trivial to test. The collector follows the same split: it only gathers state through read only kubectl calls, and the detectors never know where the file came from, so the two halves can change independently.

Why diagnose only: the moment a tool can change things, every run needs a permissions conversation, and the tool stops being something you reach for casually at 3 AM. Reading state and suggesting next steps keeps it safe to run anywhere. The next step is advice for a human, not an action the tool takes.

## Limits

* The report is only as good as the snapshot. Stale input gives a confident looking report about a cluster that has already moved on.
* The memory detector needs usage numbers in the snapshot. Without `metrics` fields it stays silent rather than guessing.
* The config file covers the memory threshold, severity overrides, and disabled detectors only. Its parser accepts the three settings shown in the config section, not general YAML: no anchors, no inline maps, no lists.
* KubePulse does not read logs, so for a crashloop it can point at the container and the restart count, not at the stack trace.
* Diff matches findings on their summary text. A finding whose summary changes between snapshots, a crashloop restart count climbing from 9 to 14, shows up as one resolved and one new finding rather than one unchanged finding. I know about this one and accept it for now: the alternative is matching on less, and calling two different problems the same finding is worse.
* The cronjob never ran detector cannot tell a cronjob created a minute ago from one that has been broken for a week: both have an empty `lastScheduleTime`. I flag both and let the next step sort it out.
* The stale cronjob detector stays silent when it cannot judge: a suspended cronjob, a schedule it cannot parse, or a snapshot with no parseable `capturedAt` or `lastScheduleTime`. Its interval comes from the first two runs the schedule produces after the last run, so a schedule whose interval changes, such as a monthly one, is judged on its next gap, not an average.
* Collect needs kubectl and a working context, and it reads the whole cluster (or the one namespace asked for) on every run. It is a capture tool, not a watcher.

## Roadmap

* A watch mode that collects on an interval and diffs against the previous snapshot, so the collect and diff halves run as one loop.

## License

MIT, see `LICENSE`.
