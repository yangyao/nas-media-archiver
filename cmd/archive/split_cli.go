package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/yangyao/nas-media-archiver/internal/batchsplit"
)

func runSplitDirectory(args []string) error {
	f := flag.NewFlagSet("split-directory", flag.ContinueOnError)
	source := f.String("path", "", "source directory")
	prefix := f.String("prefix", "", "batch name prefix (defaults to source directory name)")
	limit := f.Int64("max-bytes", batchsplit.DefaultLimit, "batch size in bytes (8 GB by default)")
	apply := f.Bool("apply", false, "move files; default only previews")
	planFile := f.String("plan-file", "", "durable recovery plan file; required with --apply")
	resume := f.Bool("resume", false, "resume the saved plan instead of planning again")
	if e := f.Parse(args); e != nil {
		return e
	}
	var p batchsplit.Plan
	if *resume {
		if !*apply || *planFile == "" {
			return errors.New("--resume requires --apply and --plan-file")
		}
		b, e := os.ReadFile(*planFile)
		if e != nil {
			return e
		}
		if e = json.Unmarshal(b, &p); e != nil {
			return e
		}
	} else {
		if *source == "" {
			return errors.New("--path is required")
		}
		var e error
		p, e = batchsplit.New(*source, *prefix, *limit)
		if e != nil {
			return e
		}
	}
	if *apply {
		if *planFile == "" {
			return errors.New("--apply requires --plan-file for interruption recovery")
		}
		if !*resume {
			out, e := os.OpenFile(*planFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return e
			}
			e = json.NewEncoder(out).Encode(p)
			if e == nil {
				e = out.Sync()
			}
			e = errors.Join(e, out.Close())
			if e != nil {
				return e
			}
			if e = p.Reserve(); e != nil {
				return e
			}
		}
		if e := p.Execute(func() bool { return false }, func(_ int, _ int64) {}); e != nil {
			return e
		}
	}
	summary := map[string]any{"sourcePath": p.Source, "totalFiles": p.TotalFiles, "totalBytes": p.TotalBytes, "retainedEntries": p.RetainedEntries, "batches": p.Batches, "applied": *apply}
	if p.TotalFiles == 0 {
		fmt.Fprintln(os.Stderr, "没有可拆分的文件，目录保持不变。")
	}
	return json.NewEncoder(os.Stdout).Encode(summary)
}
