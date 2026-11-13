// Command kubepulse reads a cluster snapshot file and reports the
// problems it finds. It never connects to a cluster and never changes
// one. It only reads the snapshot and writes a report.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/UdayKandyanam/kubepulse/internal/diagnose"
	"github.com/UdayKandyanam/kubepulse/internal/report"
	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "diagnose":
		return runDiagnose(args[1:])
	case "help", "-h", "--help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "kubepulse: unknown command %q\n", args[0])
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: kubepulse diagnose [--format text|json] [--fail-on critical|high|medium|low] <snapshot.json>")
}

func runDiagnose(args []string) int {
	fs := flag.NewFlagSet("diagnose", flag.ContinueOnError)
	format := fs.String("format", "text", "output format: text or json")
	failOn := fs.String("fail-on", "", "exit with code 1 when a finding at or above this severity exists")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		usage()
		return 2
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(os.Stderr, "kubepulse: invalid --format %q (use text or json)\n", *format)
		return 2
	}
	var threshold diagnose.Severity
	if *failOn != "" {
		var err error
		threshold, err = diagnose.ParseSeverity(*failOn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kubepulse: %v\n", err)
			return 2
		}
	}

	snap, err := snapshot.Load(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubepulse: %v\n", err)
		return 2
	}
	findings := diagnose.Diagnose(snap)

	switch *format {
	case "json":
		err = report.JSON(os.Stdout, snap, findings)
	default:
		err = report.Text(os.Stdout, snap, findings)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubepulse: write report: %v\n", err)
		return 2
	}

	if threshold != "" && diagnose.ShouldFail(findings, threshold) {
		return 1
	}
	return 0
}
