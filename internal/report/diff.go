package report

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/UdayKandyanam/kubepulse/internal/diagnose"
	"github.com/UdayKandyanam/kubepulse/internal/diff"
	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
)

// diffDocument is the JSON shape for a diff report. Field order is
// fixed by the struct, so the output is stable between runs.
type diffDocument struct {
	BeforeCluster string             `json:"beforeCluster"`
	AfterCluster  string             `json:"afterCluster"`
	Summary       diffSummary        `json:"summary"`
	New           []diagnose.Finding `json:"new"`
	Resolved      []diagnose.Finding `json:"resolved"`
	Unchanged     []diagnose.Finding `json:"unchanged"`
}

type diffSummary struct {
	New       int `json:"new"`
	Resolved  int `json:"resolved"`
	Unchanged int `json:"unchanged"`
}

// DiffJSON writes a diff result as indented JSON.
func DiffJSON(w io.Writer, before, after *snapshot.Snapshot, res diff.Result) error {
	doc := diffDocument{
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
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// DiffText writes a diff result in three sections: new, resolved, and
// unchanged findings, each with a count in its heading.
func DiffText(w io.Writer, before, after *snapshot.Snapshot, res diff.Result) error {
	cluster := after.Cluster
	if cluster == "" {
		cluster = before.Cluster
	}
	if cluster == "" {
		cluster = "unknown cluster"
	}
	if _, err := fmt.Fprintf(w, "KubePulse diff for %s\n", cluster); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%d new, %d resolved, %d unchanged\n\n",
		len(res.New), len(res.Resolved), len(res.Unchanged)); err != nil {
		return err
	}
	sections := []struct {
		name     string
		findings []diagnose.Finding
	}{
		{"NEW", res.New},
		{"RESOLVED", res.Resolved},
		{"UNCHANGED", res.Unchanged},
	}
	for _, sec := range sections {
		if _, err := fmt.Fprintf(w, "%s (%d)\n", sec.name, len(sec.findings)); err != nil {
			return err
		}
		if len(sec.findings) == 0 {
			if _, err := fmt.Fprintln(w, "  None"); err != nil {
				return err
			}
		}
		for _, f := range sec.findings {
			if _, err := fmt.Fprintf(w, "  %s: %s\n", f.Object, f.Summary); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func nonNil(findings []diagnose.Finding) []diagnose.Finding {
	if findings == nil {
		return []diagnose.Finding{}
	}
	return findings
}
