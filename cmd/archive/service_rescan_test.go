package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServiceEmptyPreviewCanRescanNewFiles(t *testing.T) {
	s, c, source, target := serviceFixture(t)
	job, err := s.createJob([]string{source}, target, false)
	if err != nil {
		t.Fatal(err)
	}
	ready := serviceWait(t, s, job.ID, "ready")
	if ready.TotalFiles != 0 || !ready.ScanComplete {
		t.Fatal(ready)
	}
	if _, err = s.jobAction(job.ID, "start"); err == nil {
		t.Fatal("empty task could start")
	}
	file := filepath.Join(source, "IMG_20260701_120000.jpg")
	serviceWrite(t, file, []byte("new photo"))
	scanning, err := s.jobAction(job.ID, "rescan")
	if err != nil {
		t.Fatal(err)
	}
	if scanning.Status != "scanning" || scanning.ScanComplete {
		t.Fatal("rescan retained a ready preview")
	}
	ready = serviceWait(t, s, job.ID, "ready")
	if ready.ID != job.ID || ready.TotalFiles != 1 || ready.Started || ready.CompletedFiles != 0 {
		t.Fatalf("rescan must update same preview: %+v", ready)
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal("rescan moved source")
	}
	// Removing a file and rescanning must remove its stale preview/DB record.
	os.Remove(file)
	if _, err = s.jobAction(job.ID, "rescan"); err != nil {
		t.Fatal(err)
	}
	ready = serviceWait(t, s, job.ID, "ready")
	if ready.TotalFiles != 0 {
		t.Fatal("stale preview file remains", ready)
	}
	s.Close()
	next, err := newArchiveService(c)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if j := next.jobs[job.ID]; len(j.Files) != 0 || j.TotalFiles != 0 || j.Started {
		t.Fatalf("stale checkpoint survived reset: %+v", j)
	}
}
func TestServiceRescanCannotDiscardStartedArchiveHistory(t *testing.T) {
	s, _, source, target := serviceFixture(t)
	serviceWrite(t, filepath.Join(source, "IMG_20260701_120000.jpg"), []byte("original"))
	job, _ := s.createJob([]string{source}, target, false)
	serviceWait(t, s, job.ID, "ready")
	s.mu.Lock()
	s.jobs[job.ID].Started = true
	s.mu.Unlock()
	if _, err := s.jobAction(job.ID, "rescan"); err == nil {
		t.Fatal("started task's journal was discarded")
	}
	s.mu.Lock()
	s.jobs[job.ID].Started = false
	s.mu.Unlock()
	if _, err := s.jobAction(job.ID, "start"); err != nil {
		t.Fatal(err)
	}
	completed := serviceWait(t, s, job.ID, "completed")
	if !completed.Started {
		t.Fatal("start was not recorded")
	}
	if _, err := s.jobAction(job.ID, "rescan"); err == nil {
		t.Fatal("completed history was overwritten")
	}
}
