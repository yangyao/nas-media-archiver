package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestGIFUsesPhotoPipelineAndPreservesBytes(t *testing.T) {
	s, _, source, target := serviceFixture(t)
	// Minimal valid GIF with a graphic control extension; no transcoding occurs.
	payload := []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")
	path := filepath.Join(source, "20210114_145118_02_1.GIF")
	serviceWrite(t, path, payload)
	if classifyArchiveType(path) != "photos" {
		t.Fatal("GIF not classified as photo")
	}
	j, err := s.createJobWithOptions([]string{source}, target, false, true)
	if err != nil {
		t.Fatal(err)
	}
	ready := serviceWait(t, s, j.ID, "ready")
	if ready.UnsupportedFiles != 0 || ready.AttentionFiles != 0 || ready.TotalFiles != 1 {
		t.Fatalf("GIF rejected or date fell back: %+v", ready)
	}
	s.mu.Lock()
	preview := *s.jobs[j.ID].Files[0]
	s.mu.Unlock()
	if preview.MetadataSource != "filename" || !bytes.Contains([]byte(preview.TargetPath), []byte("/photos/2021/01/20210114_145118_")) {
		t.Fatalf("wrong GIF date/target: %+v", preview)
	}
	if _, err = s.jobAction(j.ID, "start"); err != nil {
		t.Fatal(err)
	}
	done := serviceWait(t, s, j.ID, "completed", "completed_with_errors", "failed")
	if done.CompletedFiles != 1 || done.FailedFiles != 0 {
		t.Fatal(done)
	}
	got, err := os.ReadFile(preview.TargetPath)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatal("GIF bytes changed", err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("verified GIF source remains")
	}
	// A second identical GIF must use the same candidate comparison as photos.
	serviceWrite(t, path, payload)
	next, err := s.createJobWithOptions([]string{source}, target, false, true)
	if err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, next.ID, "ready")
	s.jobAction(next.ID, "start")
	skipped := serviceWait(t, s, next.ID, "completed", "completed_with_errors", "failed")
	if skipped.SkippedFiles != 1 || skipped.CompletedFiles != 0 {
		t.Fatal("GIF duplicate copied again", skipped)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("skipped duplicate source removed", err)
	}
}
func TestBareCaptureDateFilenames(t *testing.T) {
	for _, name := range []string{"20200712175143.gif", "20200822095858.gif", "20200905_093713.jpg", "20210114_145118_02_1.gif"} {
		if _, ok := parseFilenameTime(name); !ok {
			t.Errorf("capture date missed: %s", name)
		}
	}
	for _, name := range []string{"20201332175143.gif", "202007121751430.gif", "12345678.gif"} {
		if _, ok := parseFilenameTime(name); ok {
			t.Errorf("invalid capture date accepted: %s", name)
		}
	}
}
