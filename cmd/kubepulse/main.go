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
	"time"

	"github.com/UdayKandyanam/kubepulse/internal/collect"
	"github.com/UdayKandyanam/kubepulse/internal/diagnose"
	"github.com/UdayKandyanam/kubepulse/internal/diff"
	"github.com/UdayKandyanam/kubepulse/internal/report"
	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
	"github.com/UdayKandyanam/kubepulse/internal/watch"
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
	case "watch":
		return runWatch(args[1:])
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
	fmt.Fprintln(os.Stderr, "  kubepulse diagnose [--format text|json] [--fail-on critical|high|medium|low] [--config <file>] [--print-config] <snapshot.json>")
	fmt.Fprintln(os.Stderr, "  kubepulse diff [--format text|json] [--fail-on critical|high|medium|low] [--config <file>] [--print-config] <before.json> <after.json>")
	fmt.Fprintln(os.Stderr, "  kubepulse collect [--kubeconfig <path>] [--namespace <ns>] [--output <file>]")
	fmt.Fprintln(os.Stderr, "  kubepulse watch [--format text|json] [--interval <duration>] [--rounds <n>] [--kubeconfig <path>] [--namespace <ns>] [--config <file>] [--fail-on critical|high|medium|low]")
}

// loadConfig returns the default config when no file is named, and
// the file settings when one is. A bad file is a hard error: running
// with defaults the user did not ask for would hide the mistake.
func loadConfig(path string) (diagnose.Config, error) {
	if path == "" {
		return diagnose.DefaultConfig(), nil
	}
	return diagnose.LoadConfig(path)
}

func runDiagnose(args []string) int {
	fs := flag.NewFlagSet("diagnose", flag.ContinueOnError)
	format := fs.String("format", "text", "output format: text or json")
	failOn := fs.String("fail-on", "", "exit with code 1 when a finding at or above this severity exists")
	configPath := fs.String("config", "", "config file with detector thresholds and severity overrides")
	printConfig := fs.Bool("print-config", false, "print the effective config and exit, without reading a snapshot")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *printConfig {
		if fs.NArg() > 1 {
			usage()
			return 2
		}
		cfg, err := loadConfig(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kubepulse: %v\n", err)
			return 2
		}
		fmt.Fprint(os.Stdout, cfg.Describe())
		return 0
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

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubepulse: %v\n", err)
		return 2
	}
	snap, err := snapshot.Load(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubepulse: %v\n", err)
		return 2
	}
	findings := diagnose.DiagnoseWithConfig(snap, cfg)

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
	configPath := fs.String("config", "", "config file with detector thresholds and severity overrides")
	printConfig := fs.Bool("print-config", false, "print the effective config and exit, without reading snapshots")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *printConfig {
		if fs.NArg() != 0 && fs.NArg() != 2 {
			usage()
			return 2
		}
		cfg, err := loadConfig(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kubepulse: %v\n", err)
			return 2
		}
		fmt.Fprint(os.Stdout, cfg.Describe())
		return 0
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

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubepulse: %v\n", err)
		return 2
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
	res := diff.Compare(diagnose.DiagnoseWithConfig(before, cfg), diagnose.DiagnoseWithConfig(after, cfg))

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

// runWatch collects on an interval. The first round prints a full
// report and each later round prints a diff against the round before
// it. With no round count it runs until a collect fails or the
// process is stopped. The fail-on exit code is only reached when the
// watch ends on its own, so it is meant for counted runs in scripts.
func runWatch(args []string) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	format := fs.String("format", "text", "output format: text or json (json writes one object per round)")
	interval := fs.Duration("interval", 30*time.Second, "pause between rounds")
	rounds := fs.Int("rounds", 0, "rounds to run before exiting (default: run until a collect fails or the process is stopped)")
	kubeconfig := fs.String("kubeconfig", "", "path to the kubeconfig file (default: kubectl's own resolution)")
	namespace := fs.String("namespace", "", "collect only this namespace (default: all namespaces)")
	failOn := fs.String("fail-on", "", "exit with code 1 when a round adds a finding at or above this severity")
	configPath := fs.String("config", "", "config file with detector thresholds and severity overrides")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		usage()
		return 2
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(os.Stderr, "kubepulse: invalid --format %q (use text or json)\n", *format)
		return 2
	}
	if *interval <= 0 {
		fmt.Fprintf(os.Stderr, "kubepulse: invalid --interval %v (use a positive duration such as 30s or 2m)\n", *interval)
		return 2
	}
	if *rounds < 0 {
		fmt.Fprintf(os.Stderr, "kubepulse: invalid --rounds %d (use 0 or more)\n", *rounds)
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
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubepulse: %v\n", err)
		return 2
	}
	src := func() (*snapshot.Snapshot, error) {
		return collect.Collect(collect.ExecRunner{}, collect.Options{
			Kubeconfig: *kubeconfig,
			Namespace:  *namespace,
		})
	}
	res, err := watch.Run(os.Stdout, src, watch.Options{
		Interval: *interval,
		Rounds:   *rounds,
		Config:   cfg,
		FailOn:   threshold,
		Format:   *format,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubepulse: watch: %v\n", err)
		return 2
	}
	if res.Failed {
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
