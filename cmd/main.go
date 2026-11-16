package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Sansero-ee/pingit/internal/tui"
)

const version = "1.0.0"

const usageText = `pingit — parallel ICMP ping monitor with JSON Lines logging

Usage:
  pingit              open the interactive TUI
  pingit --help       show this help and exit
  pingit --version    show version and exit

No other arguments or flags are accepted: any other argument prints
this usage and exits with status 2.

TUI fields:
  targets        one IPv4/IPv6 address or hostname per line
  duration       total probing time, e.g. 60s, 5m
  interval       delay between probes, e.g. 1s
  log file       JSONL output path (default: ~/pinglog-YYYYMMDD-HHMMSS.jsonl)
  config file    optional input file, loaded with ctrl+l

TUI keys:
  tab / shift+tab   next / previous field
  space             toggle "allow up to 100 targets"
  ctrl+l            load targets/duration/interval from the config file
  ctrl+s            start the run
  ctrl+c            stop the run gracefully (log is flushed) / quit

Raw ICMP needs cap_net_raw for unprivileged use:
  sudo setcap cap_net_raw=eip ./pingit
`

func usage(w io.Writer) {
	fmt.Fprint(w, usageText)
}

func main() {
	fs := flag.NewFlagSet("pingit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	help := fs.Bool("help", false, "print usage and exit")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() { usage(os.Stderr) }

	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			usage(os.Stdout)
			os.Exit(0)
		}
		os.Exit(2)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument: %q\n\n", fs.Arg(0))
		usage(os.Stderr)
		os.Exit(2)
	}
	switch {
	case *showVersion:
		fmt.Printf("pingit version %s\n", version)
	case *help:
		usage(os.Stdout)
	default:
		if err := tui.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "pingit:", err)
			os.Exit(1)
		}
	}
}
