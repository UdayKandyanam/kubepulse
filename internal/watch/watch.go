// Package watch runs collect and diagnose as one loop: the first
// round prints a full report, and each later round prints a diff
// against the round before it, so a change in the cluster shows up
// as what appeared and what went away instead of another full list.
// The snapshot source and the sleep between rounds are function
// values so the loop can be tested without a cluster or a clock.
package watch

import (
	"fmt"
	"io"
	"time"

	"github.com/UdayKandyanam/kubepulse/internal/diagnose"
	"github.com/UdayKandyanam/kubepulse/internal/diff"
	"github.com/UdayKandyanam/kubepulse/internal/report"
	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
)

// Source returns the next snapshot. In the CLI it wraps collect, in
// tests it returns snapshots from a list.
type Source func() (*snapshot.Snapshot, error)

// Options controls a watch run.
type Options struct {
	// Interval is the pause between rounds.
	Interval time.Duration
	// Rounds is how many rounds to run. Zero or less runs until the
	// source returns an error or the process is stopped.
	Rounds int
	// Config is the diagnose config used for every round, so a
	// finding cannot appear or vanish because the rules changed
	// between rounds.
	Config diagnose.Config
	// FailOn, when set, marks the run as failed when the first
	// round has a finding at or above it, or a later round has a
	// new finding at or above it. Empty means never failed.
	FailOn diagnose.Severity
	// Sleep pauses between rounds. Nil uses time.Sleep.
	Sleep func(time.Duration)
}

// Result reports what a finished watch run saw.
type Result struct {
	Rounds int
	Failed bool
}

// Run collects and reports until the round count is reached or the
// source fails. A source error ends the watch and is returned: the
// rounds already printed stay printed, and the caller decides the
// exit code.
func Run(w io.Writer, src Source, opts Options) (Result, error) {
	sleep := opts.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	var res Result
	var prevSnap *snapshot.Snapshot
	var prevFindings []diagnose.Finding
	for opts.Rounds <= 0 || res.Rounds < opts.Rounds {
		if res.Rounds > 0 {
			sleep(opts.Interval)
		}
		snap, err := src()
		if err != nil {
			return res, fmt.Errorf("round %d: %w", res.Rounds+1, err)
		}
		findings := diagnose.DiagnoseWithConfig(snap, opts.Config)
		if prevSnap == nil {
			if err := report.Text(w, snap, findings); err != nil {
				return res, fmt.Errorf("write report: %w", err)
			}
			if opts.FailOn != "" && diagnose.ShouldFail(findings, opts.FailOn) {
				res.Failed = true
			}
		} else {
			d := diff.Compare(prevFindings, findings)
			if err := report.DiffText(w, prevSnap, snap, d); err != nil {
				return res, fmt.Errorf("write report: %w", err)
			}
			if opts.FailOn != "" && diff.ShouldFailNew(d, opts.FailOn) {
				res.Failed = true
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return res, fmt.Errorf("write report: %w", err)
		}
		prevSnap = snap
		prevFindings = findings
		res.Rounds++
	}
	return res, nil
}
