package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/thkhxm/tgf/v2/internal/scaffold"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tgfctl:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "init":
		return runInit(ctx, args[1:])
	case "verify":
		return runVerify(ctx, args[1:])
	case "version":
		if len(args) != 1 {
			return fmt.Errorf("version accepts no arguments")
		}
		fmt.Println("tgfctl " + scaffold.GeneratorVersion)
		return nil
	case "help", "-h", "--help":
		return usageError()
	default:
		return fmt.Errorf("unknown subcommand %q; expected init, verify, or version", args[0])
	}
}

func runInit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tgfctl init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var cfg scaffold.Config
	var jsonOutput bool
	fs.StringVar(&cfg.Module, "module", "", "Go module path (required)")
	fs.StringVar(&cfg.Dir, "dir", "", "destination directory (required)")
	fs.StringVar(&cfg.Profile, "profile", "single", "single|distributed|http-rest|http-rpc")
	fs.StringVar(&cfg.Data, "data", "", "none|redis|redis-mysql (distributed defaults to redis)")
	fs.StringVar(&cfg.Protocol, "protocol", "", "none|tcp|websocket|kcp|all (HTTP defaults to none)")
	fs.StringVar(&cfg.Deploy, "deploy", "local", "local|compose|k8s")
	fs.StringVar(&cfg.Security, "security", "dev-generated", "dev-generated|external")
	fs.StringVar(&cfg.TGFVersion, "tgf-version", "latest", "latest, a v2 tag, or a commit")
	fs.BoolVar(&cfg.Force, "force", false, "allow an existing empty destination only")
	fs.BoolVar(&cfg.GitInit, "git-init", false, "initialize a Git repository after checks pass")
	fs.BoolVar(&cfg.SkipChecks, "skip-checks", false, "skip go mod tidy/build/vet/provenance checks")
	fs.BoolVar(&jsonOutput, "json", false, "emit machine-readable JSON (never includes secrets)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	result, err := scaffold.Generate(ctx, cfg)
	if err != nil {
		return err
	}
	return printResult(result, jsonOutput)
}

func runVerify(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tgfctl verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("dir", ".", "generated project directory")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	result, err := scaffold.Verify(ctx, *dir)
	if err != nil {
		return err
	}
	return printResult(result, *jsonOutput)
}

func printResult(value any, asJSON bool) error {
	if asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(value)
	}
	switch result := value.(type) {
	case scaffold.Result:
		fmt.Printf("generated %s (%s) at %s with tgf %s\n", result.Module, result.Profile, result.Dir, result.TGFVersion)
	case scaffold.Verification:
		fmt.Printf("verified %s at %s with tgf %s\n", result.Module, result.Dir, result.TGFVersion)
	default:
		return fmt.Errorf("unsupported output type %T", value)
	}
	return nil
}

func usageError() error {
	return fmt.Errorf("usage: tgfctl <init|verify|version> [flags]")
}
