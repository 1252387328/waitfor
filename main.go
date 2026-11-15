// waitfor blocks until TCP ports, HTTP endpoints or files are ready, then optionally runs a command.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var version = "0.1.0"

func usage() {
	fmt.Fprint(os.Stderr, `waitfor - wait until dependencies are ready

usage: waitfor [flags] TARGET [TARGET ...] [-- command args...]

targets:
  host:port | tcp://host:port   TCP port accepts connections
  http://... | https://...      URL answers with status < 400
  file:///path                  file or directory exists

examples:
  waitfor db:5432 redis:6379 -- ./server
  waitfor -t 2m http://localhost:8080/healthz
  waitfor -any primary:5432 replica:5432

flags:
`)
	flag.PrintDefaults()
}

func splitCommand(args []string) (targets, cmd []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

func main() {
	timeout := flag.Duration("t", 60*time.Second, "give up after this long (0 = forever)")
	interval := flag.Duration("i", time.Second, "delay between attempts")
	attempt := flag.Duration("a", 5*time.Second, "timeout for a single attempt")
	anyMode := flag.Bool("any", false, "continue when ANY target is ready (default: all)")
	quiet := flag.Bool("q", false, "no progress output")
	insecure := flag.Bool("k", false, "skip TLS verification for https targets")
	showVersion := flag.Bool("version", false, "print version")
	flag.Usage = usage

	targetArgs, command := splitCommand(os.Args[1:])
	// allow flags anywhere before "--"
	var flags, positional []string
	for i := 0; i < len(targetArgs); i++ {
		a := targetArgs[i]
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			name := a[1:]
			if f := flag.Lookup(name); f != nil {
				if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); !(ok && bf.IsBoolFlag()) && i+1 < len(targetArgs) {
					flags = append(flags, targetArgs[i+1])
					i++
				}
			}
			continue
		}
		positional = append(positional, a)
	}
	flag.CommandLine.Parse(flags)
	if *showVersion {
		fmt.Println("waitfor", version)
		return
	}
	if len(positional) == 0 {
		usage()
		os.Exit(2)
	}

	var probes []Probe
	for _, t := range positional {
		p, err := ParseTarget(t, *insecure)
		if err != nil {
			fmt.Fprintln(os.Stderr, "waitfor:", err)
			os.Exit(2)
		}
		probes = append(probes, p)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	logf := func(format string, a ...any) {
		if !*quiet {
			fmt.Fprintf(os.Stderr, "waitfor: "+format+"\n", a...)
		}
	}

	start := time.Now()
	if err := waitAll(ctx, probes, *anyMode, *interval, *attempt, logf); err != nil {
		fmt.Fprintln(os.Stderr, "waitfor:", err)
		os.Exit(1)
	}
	logf("ready after %s", time.Since(start).Round(time.Millisecond))

	if len(command) > 0 {
		os.Exit(run(command))
	}
}

func waitAll(ctx context.Context, probes []Probe, anyMode bool, interval, attempt time.Duration, logf func(string, ...any)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		p   Probe
		err error
	}
	results := make(chan result, len(probes))
	var wg sync.WaitGroup
	for _, p := range probes {
		wg.Add(1)
		go func(p Probe) {
			defer wg.Done()
			first := true
			err := WaitOne(ctx, p, interval, attempt, func(err error) {
				if first {
					logf("waiting for %s (%v)", p, err)
					first = false
				}
			})
			results <- result{p, err}
		}(p)
	}
	go func() { wg.Wait(); close(results) }()

	var errs []error
	for r := range results {
		if r.err == nil {
			logf("%s is up", r.p)
			if anyMode {
				return nil
			}
			continue
		}
		if !errors.Is(r.err, context.Canceled) || ctx.Err() == nil {
			errs = append(errs, r.err)
		}
	}
	if anyMode || len(errs) > 0 {
		if len(errs) == 0 {
			return errors.New("no target became ready")
		}
		return errors.Join(errs...)
	}
	return nil
}

// run executes the command, forwarding signals, and returns its exit code.
func run(argv []string) int {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "waitfor:", err)
		return 127
	}
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for s := range sigs {
			_ = cmd.Process.Signal(s)
		}
	}()
	err := cmd.Wait()
	signal.Stop(sigs)
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}
