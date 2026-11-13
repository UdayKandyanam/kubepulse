// Command kubepulse reports the problems it finds in a cluster
// snapshot and compares snapshots over time. The collect subcommand
// builds a snapshot by reading cluster state through kubectl get
// calls. Nothing in kubepulse changes a cluster.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/UdayKandyanam/kubepulse/internal/collect"
	"github.com/UdayKandyanam/kubepulse/internal/diagnose"
	"github.com/UdayKandyanam/kubepulse/internal/diff"
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
	case "diff":
		return runDiff(args[1:])
	case "collect":
		return runCollect(args[1:])
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
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  kubepulse diagnose [--format text|json] [--fail-on critical|high|medium|low] <snapshot.json>")
	fmt.Fprintln(os.Stderr, "  kubepulse diff [--format text|json] [--fail-on critical|high|medium|low] <before.json> <after.json>")
	fmt.Fprintln(os.Stderr, "  kubepulse collect [--kubeconfig <path>] [--namespace <ns>] [--output <file>]")
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

func runDiff(args []string) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	format := fs.String("format", "text", "output format: text or json")
	failOn := fs.String("fail-on", "", "exit with code 1 when a new finding at or above this severity exists")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
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

	before, err := snapshot.Load(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubepulse: %v\n", err)
		return 2
	}
	after, err := snapshot.Load(fs.Arg(1))
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubepulse: %v\n", err)
		return 2
	}
	res := diff.Compare(diagnose.Diagnose(before), diagnose.Diagnose(after))

	switch *format {
	case "json":
		err = report.DiffJSON(os.Stdout, before, after, res)
	default:
		err = report.DiffText(os.Stdout, before, after, res)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubepulse: write report: %v\n", err)
		return 2
	}

	if threshold != "" && diff.ShouldFailNew(res, threshold) {
		return 1
	}
	return 0
}

func runCollect(args []string) int {
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	kubeconfig := fs.String("kubeconfig", "", "path to the kubeconfig file (default: kubectl's own resolution)")
	namespace := fs.String("namespace", "", "collect only this namespace (default: all namespaces)")
	output := fs.String("output", "", "write the snapshot to this file (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		usage()
		return 2
	}

	snap, err := collect.Collect(collect.ExecRunner{}, collect.Options{
		Kubeconfig: *kubeconfig,
		Namespace:  *namespace,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubepulse: %v\n", err)
		return 2
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubepulse: encode snapshot: %v\n", err)
		return 2
	}
	data = append(data, '\n')
	if *output == "" {
		if _, err := os.Stdout.Write(data); err != nil {
			fmt.Fprintf(os.Stderr, "kubepulse: write snapshot: %v\n", err)
			return 2
		}
		return 0
	}
	if err := os.WriteFile(*output, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "kubepulse: write snapshot: %v\n", err)
		return 2
	}
	return 0
}
