// Command trippad-memory accesses the same scratchtools as trippad without
// loading a model or GPU. Use -value-file - to read note content from stdin.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/NimbleMarkets/ds4go-apps/internal/trippad/memory"
)

func run() error {
	dir := flag.String("dir", "", "memory root (default $DS4_DIR/scratch/trippad or ~/.ds4/scratch/trippad)")
	scope := flag.String("scope", "global", "global or session")
	session := flag.String("session", "codex", "session ID; coding agent default: codex")
	key := flag.String("key", "", "scratch key")
	file := flag.String("value-file", "", "set/append content file; - reads stdin")
	head := flag.Int("head", 0, "get first N bytes (0 reads whole note)")
	flag.Parse()
	if flag.NArg() != 1 {
		return fmt.Errorf("usage: trippad-memory [flags] list|get|set|append|delete|brief")
	}
	op := flag.Arg(0)
	switch op {
	case "list", "get", "set", "append", "delete", "brief":
	default:
		return fmt.Errorf("unknown operation %q", op)
	}
	if *scope != "global" && *scope != "session" {
		return fmt.Errorf("scope must be global or session")
	}
	args := map[string]any{"key": *key, "head": *head}
	if op == "set" || op == "append" {
		if *file == "" {
			return fmt.Errorf("set/append requires -value-file (- for stdin)")
		}
		var reader io.Reader = os.Stdin
		if *file != "-" {
			f, err := os.Open(*file)
			if err != nil {
				return err
			}
			defer f.Close()
			reader = f
		}
		data, err := io.ReadAll(io.LimitReader(reader, (16<<10)+1))
		if err != nil {
			return err
		}
		if len(data) > 16<<10 {
			return fmt.Errorf("note exceeds 16KiB")
		}
		args["value"] = string(data)
	}
	m, err := memory.Open(*dir, *session)
	if err != nil {
		return err
	}
	defer m.Close()
	var out string
	if op == "brief" {
		out, err = m.Brief(context.Background())
	} else {
		raw, _ := json.Marshal(args)
		out, err = m.Call(context.Background(), *scope, op, raw)
	}
	if err != nil {
		return err
	}
	if strings.HasPrefix(out, "ERROR:") {
		return fmt.Errorf("%s", strings.TrimSpace(out))
	}
	fmt.Print(out)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
