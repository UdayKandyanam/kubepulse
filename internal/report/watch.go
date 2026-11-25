// Watch rounds in JSON mode, one object per round. Each object is a
// single compact line, so a watch run is a JSON Lines stream a log
// pipeline can read as it arrives instead of waiting for the run to
// end. The first round carries the full report, later rounds carry a
// diff against the round before them, the same split as the text
// output.
package report

import (
	"encoding/json"
	"io"

	"github.com/UdayKandyanam/kubepulse/internal/diagnose"
	"github.com/UdayKandyanam/kubepulse/internal/diff"
	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
)

// watchReportDocument is the JSON shape for the first round of a
// watch run in JSON mode.
type watchReportDocument struct {
	Round    int                `json:"round"`
	Kind     string             `json:"kind"`
	Cluster  string             `json:"cluster"`
	Summary  map[string]int     `json:"summary"`
	Findings []diagnose.Finding `json:"findings"`
}

// watchDiffDocument is the JSON shape for a later round of a watch
// run in JSON mode.
type watchDiffDocument struct {
	Round         int                `json:"round"`
	Kind          string             `json:"kind"`
	BeforeCluster string             `json:"beforeCluster"`
	AfterCluster  string             `json:"afterCluster"`
	Summary       diffSummary        `json:"summary"`
	New           []diagnose.Finding `json:"new"`
	Resolved      []diagnose.Finding `json:"resolved"`
	Unchanged     []diagnose.Finding `json:"unchanged"`
}

// WatchReportJSON writes one round of a watch run as a single line of
// JSON: the full report for the first round.
func WatchReportJSON(w io.Writer, round int, s *snapshot.Snapshot, findings []diagnose.Finding) error {
	sorted := append([]diagnose.Finding(nil), findings...)
	sortFindings(sorted)
	doc := watchReportDocument{
		Round:    round,
		Kind:     "report",
		Cluster:  s.Cluster,
		Summary:  countBySeverity(sorted),
		Findings: nonNil(sorted),
	}
	return json.NewEncoder(w).Encode(doc)
}

// WatchDiffJSON writes one round of a watch run as a single line of
// JSON: the diff against the round before it.
func WatchDiffJSON(w io.Writer, round int, before, after *snapshot.Snapshot, res diff.Result) error {
	doc := watchDiffDocument{
		Round:         round,
		Kind:          "diff",
		BeforeCluster: before.Cluster,
		AfterCluster:  after.Cluster,
		Summary: diffSummary{
			New:       len(res.New),
			Resolved:  len(res.Resolved),
			Unchanged: len(res.Unchanged),
		},
		New:       nonNil(res.New),
		Resolved:  nonNil(res.Resolved),
		Unchanged: nonNil(res.Unchanged),
	}
	return json.NewEncoder(w).Encode(doc)
}
