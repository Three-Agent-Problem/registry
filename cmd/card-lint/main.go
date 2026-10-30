// Command card-lint grades an A2A agent card against A2A 1.0.
//
//	card-lint https://agent.example.com        # fetches /.well-known/agent-card.json
//	card-lint ./agent-card.json                # checks a local file, for CI
//
// It exits with status 1 when the card has errors, so it can gate a deploy.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/three-agent-problem/registry/lint"
)

func main() {
	asJSON := flag.Bool("json", false, "Print the report as JSON.")
	strict := flag.Bool("strict", false, "Also fail on warnings.")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: card-lint [-json] [-strict] <agent URL | card URL | file>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	target := flag.Arg(0)

	report, source, err := run(target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "card-lint:", err)
		os.Exit(2)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"source": source, "report": report})
	} else {
		fmt.Printf("%s: %s\n", source, report.Grade)
		for _, f := range report.Findings {
			fmt.Printf("  %-7s %-40s %s\n", f.Severity, f.Path, f.Message)
		}
	}

	if report.Count(lint.SeverityError) > 0 || (*strict && report.Count(lint.SeverityWarning) > 0) {
		os.Exit(1)
	}
}

func run(target string) (lint.Report, string, error) {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		fetched, err := lint.Fetch(ctx, nil, target)
		if err != nil {
			return lint.Report{}, "", err
		}
		return fetched.Report, fetched.CardURL, nil
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return lint.Report{}, "", err
	}
	return lint.Check(raw), target, nil
}
