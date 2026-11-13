// Package diff compares the findings from two snapshots and reports
// what appeared, what went away, and what stayed the same. A finding
// matches across snapshots on detector, object, and summary, so a pod
// that is still crashlooping with a higher restart count counts as
// unchanged only when its summary line is unchanged too.
package diff

import (
	"sort"

	"github.com/UdayKandyanam/kubepulse/internal/diagnose"
)

// Result holds the three groups a comparison produces. Every slice is
// sorted the same way diagnose sorts findings.
type Result struct {
	New       []diagnose.Finding
	Resolved  []diagnose.Finding
	Unchanged []diagnose.Finding
}

type key struct {
	detector string
	object   string
	summary  string
}

func keyOf(f diagnose.Finding) key {
	return key{detector: f.Detector, object: f.Object, summary: f.Summary}
}

// Compare matches the before findings against the after findings.
// Findings only in after are new, findings only in before are
// resolved, and findings in both are unchanged. The unchanged group
// carries the after version of each finding.
func Compare(before, after []diagnose.Finding) Result {
	beforeByKey := make(map[key]diagnose.Finding, len(before))
	for _, f := range before {
		beforeByKey[keyOf(f)] = f
	}
	afterByKey := make(map[key]diagnose.Finding, len(after))
	for _, f := range after {
		afterByKey[keyOf(f)] = f
	}

	var res Result
	for _, f := range after {
		if _, ok := beforeByKey[keyOf(f)]; ok {
			res.Unchanged = append(res.Unchanged, f)
		} else {
			res.New = append(res.New, f)
		}
	}
	for _, f := range before {
		if _, ok := afterByKey[keyOf(f)]; !ok {
			res.Resolved = append(res.Resolved, f)
		}
	}
	sortFindings(res.New)
	sortFindings(res.Resolved)
	sortFindings(res.Unchanged)
	return res
}

// ShouldFailNew reports whether any new finding meets the fail-on
// threshold. Resolved and unchanged findings never fail a diff.
func ShouldFailNew(res Result, threshold diagnose.Severity) bool {
	return diagnose.ShouldFail(res.New, threshold)
}

func sortFindings(findings []diagnose.Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return severityRank(findings[i].Severity) > severityRank(findings[j].Severity)
		}
		if findings[i].Object != findings[j].Object {
			return findings[i].Object < findings[j].Object
		}
		return findings[i].Detector < findings[j].Detector
	})
}

func severityRank(s diagnose.Severity) int {
	switch s {
	case diagnose.SeverityCritical:
		return 4
	case diagnose.SeverityHigh:
		return 3
	case diagnose.SeverityMedium:
		return 2
	case diagnose.SeverityLow:
		return 1
	}
	return 0
}
