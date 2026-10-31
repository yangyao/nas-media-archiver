package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDryRunThenRun(t *testing.T) {
	root := t.TempDir()
	input, state, dest := filepath.Join(root, "input"), filepath.Join(root, "state"), filepath.Join(root, "archive")
	if err := os.MkdirAll(filepath.Join(input, ".@__thumb"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"IMG_20260601_120000.WEBP", ".@__thumb/thumb.jpg"} {
		if err := os.WriteFile(filepath.Join(input, p), []byte("sample media"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := runScan([]string{"--path", input, "--state-dir", state}); err != nil {
		t.Fatal(err)
	}
	store := NewStore(state)
	ids, err := store.ListJobs()
	if err != nil || len(ids) != 1 {
		t.Fatalf("jobs: %v %v", ids, err)
	}
	job, err := store.LoadJob(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(job.Tasks) != 1 {
		t.Fatalf("thumbnail included or WebP omitted: %d", len(job.Tasks))
	}
	args := []string{"--job", ids[0], "--archive-base", dest, "--state-dir", state}
	if err := runRun(append(args, "--dry-run")); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(input, "IMG_20260601_120000.WEBP")
	if _, err := os.Stat(source); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("dry run created destination: %v", err)
	}
	if err := runRun(args); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source remains: %v", err)
	}
	target := filepath.Join(dest, "photos", "2026", "06", "20260601_120000_12.webp")
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "sample media" {
		t.Fatalf("target: %q %v", data, err)
	}
}
