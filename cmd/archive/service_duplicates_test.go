package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServiceSkipDuplicatesRetainsSource(t *testing.T) {
	for _, kind := range []string{"identical", "different", "suffix-identical", "disabled"} {
		t.Run(kind, func(t *testing.T) {
			s, _, source, target := serviceFixture(t)
			payload := []byte("original")
			path := filepath.Join(source, "IMG_20260701_120000.jpg")
			serviceWrite(t, path, payload)
			base := filepath.Join(target, "photos", "2026", "07", "20260701_120000_8.jpg")
			if err := os.MkdirAll(filepath.Dir(base), 0755); err != nil {
				t.Fatal(err)
			}
			existing := payload
			if kind == "different" || kind == "suffix-identical" {
				existing = []byte("changed!")
			}
			serviceWrite(t, base, existing)
			matching := base
			if kind == "suffix-identical" {
				matching = filepath.Join(filepath.Dir(base), "20260701_120000_8_12.jpg")
				serviceWrite(t, matching, payload)
			}
			job, err := s.createJobWithOptions([]string{source}, target, true, kind != "disabled")
			if err != nil {
				t.Fatal(err)
			}
			serviceWait(t, s, job.ID, "ready")
			if _, err = s.jobAction(job.ID, "start"); err != nil {
				t.Fatal(err)
			}
			result := serviceWait(t, s, job.ID, "completed", "completed_with_errors", "failed")
			skipped := kind == "identical" || kind == "suffix-identical"
			if skipped {
				if result.SkippedFiles != 1 || result.SkippedBytes != 8 || result.CompletedFiles != 0 || result.CleanupStatus != "residual" {
					t.Fatalf("bad skipped counters: %+v", result)
				}
				if got, err := os.ReadFile(path); err != nil || string(got) != string(payload) {
					t.Fatal("duplicate source removed or altered")
				}
				s.mu.Lock()
				f := *s.jobs[job.ID].Files[0]
				s.mu.Unlock()
				if f.Status != "skipped" || f.DuplicateOf != matching || f.SHA256 != "" {
					t.Fatalf("not a byte-confirmed skip: %+v", f)
				}
				entries, _ := os.ReadDir(filepath.Dir(base))
				want := 1
				if kind == "suffix-identical" {
					want = 2
				}
				if len(entries) != want {
					t.Fatal("duplicate comparison wrote an extra archive file")
				}
			} else {
				if result.CompletedFiles != 1 || result.SkippedFiles != 0 {
					t.Fatalf("different/disabled file skipped: %+v", result)
				}
				if _, err = os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("new archive source remains")
				}
			}
			if got, err := os.ReadFile(base); err != nil || string(got) != string(existing) {
				t.Fatal("existing archive overwritten")
			}
		})
	}
}
func TestServiceSkipDuplicateCreatedEarlierInSameJob(t *testing.T) {
	s, _, source, target := serviceFixture(t)
	for _, name := range []string{"IMG_20260701_120000_A.jpg", "IMG_20260701_120000_B.jpg"} {
		serviceWrite(t, filepath.Join(source, name), []byte("identical"))
	}
	job, err := s.createJobWithOptions([]string{source}, target, false, true)
	if err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, job.ID, "ready")
	s.jobAction(job.ID, "start")
	result := serviceWait(t, s, job.ID, "completed", "completed_with_errors", "failed")
	if result.CompletedFiles != 1 || result.SkippedFiles != 1 {
		t.Fatalf("same-job duplicate copied twice: %+v", result)
	}
	if _, err = os.Stat(filepath.Join(source, "IMG_20260701_120000_B.jpg")); err != nil {
		t.Fatal("skipped source removed")
	}
}
func TestServiceDuplicateComparisonRejectsChangesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	serviceWrite(t, a, []byte("same"))
	serviceWrite(t, b, []byte("same"))
	info, _ := os.Stat(a)
	same, err := serviceSameContents(a, b, 4, info.ModTime(), nil)
	if err != nil || !same {
		t.Fatal(same, err)
	}
	if err = os.Chtimes(a, time.Now(), info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = serviceSameContents(a, b, 4, info.ModTime(), nil); err == nil {
		t.Fatal("changed source accepted")
	}
	info, _ = os.Stat(a)
	link := filepath.Join(root, "link")
	os.Symlink(b, link)
	same, err = serviceSameContents(a, link, 4, info.ModTime(), nil)
	if err != nil || same {
		t.Fatal("symlink accepted as duplicate", err)
	}
}
func TestServiceDuplicateOptionOnlyChangesAtSafeBoundary(t *testing.T) {
	s, _, source, target := serviceFixture(t)
	serviceWrite(t, filepath.Join(source, "IMG_20260701_120000.jpg"), []byte("test"))
	job, err := s.createJob([]string{source}, target, false)
	if err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, job.ID, "ready")
	updated, err := s.setSkipDuplicates(job.ID, true)
	if err != nil || !updated.SkipDuplicates {
		t.Fatal(updated, err)
	}
	s.mu.Lock()
	s.jobs[job.ID].Status = "running"
	s.mu.Unlock()
	if _, err = s.setSkipDuplicates(job.ID, false); err == nil {
		t.Fatal("option changed during active processing")
	}
}
