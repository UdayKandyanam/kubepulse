// Package report renders findings as grouped text or as JSON.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/UdayKandyanam/kubepulse/internal/diagnose"
	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
)

// severityOrder is the display order for text groups.
var severityOrder = []diagnose.Severity{
	diagnose.SeverityCritical,
	diagnose.SeverityHigh,
	diagnose.SeverityMedium,
	diagnose.SeverityLow,
}

// document is the JSON shape. Field order is fixed by the struct, and
// findings are sorted before rendering, so the output is stable.
type document struct {
	Cluster  string             `json:"cluster"`
	Summary  map[string]int     `json:"summary"`
	Findings []diagnose.Finding `json:"findings"`
}

// JSON writes findings as indented JSON with a per severity count.
func JSON(w io.Writer, s *snapshot.Snapshot, findings []diagnose.Finding) error {
	sorted := append([]diagnose.Finding(nil), findings...)
	sortFindings(sorted)
	doc := document{
		Cluster:  s.Cluster,
		Summary:  countBySeverity(sorted),
		Findings: sorted,
	}
	if doc.Findings == nil {
		doc.Findings = []diagnose.Finding{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// Text writes a report grouped by severity, highest first. Groups with
// no findings are skipped.
func Text(w io.Writer, s *snapshot.Snapshot, findings []diagnose.Finding) error {
	sorted := append([]diagnose.Finding(nil), findings...)
	sortFindings(sorted)

	cluster := s.Cluster
	if cluster == "" {
		cluster = "unknown cluster"
	}
	if _, err := fmt.Fprintf(w, "KubePulse report for %s\n", cluster); err != nil {
		return err
	}
	if len(sorted) == 0 {
		_, err := fmt.Fprintln(w, "No problems found in this snapshot.")
		return err
	}
	counts := countBySeverity(sorted)
	if _, err := fmt.Fprintf(w, "%d findings: %d critical, %d high, %d medium, %d low\n\n",
		len(sorted),
		counts[string(diagnose.SeverityCritical)],
		counts[string(diagnose.SeverityHigh)],
		counts[string(diagnose.SeverityMedium)],
		counts[string(diagnose.SeverityLow)]); err != nil {
		return err
	}
	for _, sev := range severityOrder {
		group := filterSeverity(sorted, sev)
		if len(group) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(w, "%s (%d)\n", upper(string(sev)), len(group)); err != nil {
			return err
		}
		for _, f := range group {
			if _, err := fmt.Fprintf(w, "  %s: %s\n", f.Object, f.Summary); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "    Evidence: %s\n", f.Evidence); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "    Next step: %s\n", f.NextStep); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func sortFindings(findings []diagnose.Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		ri, rj := severityRank(findings[i].Severity), severityRank(findings[j].Severity)
		if ri != rj {
			return ri > rj
		}
		if findings[i].Object != findings[j].Object {
			return findings[i].Object < findings[j].Object
		}
		return findings[i].Detector < findings[j].Detector
	})
}

func severityRank(s diagnose.Severity) int {
	for i, sev := range severityOrder {
		if s == sev {
			return len(severityOrder) - i
		}
	}
	return 0
}

func countBySeverity(findings []diagnose.Finding) map[string]int {
	counts := map[string]int{
		string(diagnose.SeverityCritical): 0,
		string(diagnose.SeverityHigh):     0,
		string(diagnose.SeverityMedium):   0,
		string(diagnose.SeverityLow):      0,
	}
	for _, f := range findings {
		counts[string(f.Severity)]++
	}
	return counts
}

func filterSeverity(findings []diagnose.Finding, sev diagnose.Severity) []diagnose.Finding {
	var out []diagnose.Finding
	for _, f := range findings {
		if f.Severity == sev {
			out = append(out, f)
		}
	}
	return out
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}
