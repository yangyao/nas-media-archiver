package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func serviceFixture(t *testing.T) (*archiveService, ServiceConfig, string, string) {
	t.Helper()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "sources", "batch")
	target := filepath.Join(root, "archive")
	for _, p := range []string{source, target} {
		if err = os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	c := ServiceConfig{StateDir: filepath.Join(root, "state"), ArchiveBase: target, Roots: []string{root}, Name: "Test NAS"}
	s, err := newArchiveService(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, c, source, target
}
func serviceWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}
func serviceWait(t *testing.T, s *archiveService, id string, want ...string) ServiceJob {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		j := serviceJobSnapshot(s.jobs[id])
		s.mu.Unlock()
		for _, state := range want {
			if j.Status == state {
				return j
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("task did not reach %v: %+v", want, s.listJobs())
	return ServiceJob{}
}
func TestServiceArchivePreservesUnsupportedAndExisting(t *testing.T) {
	s, _, source, target := serviceFixture(t)
	payload := []byte("test media")
	name := "IMG_20260701_120000.jpg"
	serviceWrite(t, filepath.Join(source, name), payload)
	serviceWrite(t, filepath.Join(source, "notes.txt"), []byte("keep"))
	j, err := s.createJob([]string{source}, target, true)
	if err != nil {
		t.Fatal(err)
	}
	j = serviceWait(t, s, j.ID, "ready")
	if !j.ScanComplete || j.UnsupportedFiles != 1 || j.UnsupportedBytes != 4 {
		t.Fatalf("preview counters: %+v", j)
	}
	s.mu.Lock()
	dest := s.jobs[j.ID].Files[0].TargetPath
	s.mu.Unlock()
	if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	serviceWrite(t, dest, []byte("existing"))
	if _, err = s.jobAction(j.ID, "start"); err != nil {
		t.Fatal(err)
	}
	j = serviceWait(t, s, j.ID, "completed", "completed_with_errors", "failed")
	if j.Status != "completed" || j.CompletedFiles != 1 || j.CleanupStatus != "residual" {
		t.Fatalf("result: %+v", j)
	}
	if got, _ := os.ReadFile(dest); string(got) != "existing" {
		t.Fatal("overwrote existing destination")
	}
	s.mu.Lock()
	f := *s.jobs[j.ID].Files[0]
	s.mu.Unlock()
	hash := sha256.Sum256(payload)
	if f.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatalf("missing hash: %+v", f)
	}
	if got, _ := os.ReadFile(f.TargetPath); string(got) != string(payload) {
		t.Fatal("bad archive bytes")
	}
	if _, err = os.Stat(filepath.Join(source, name)); !os.IsNotExist(err) {
		t.Fatal("source media remains")
	}
	if _, err = os.Stat(filepath.Join(source, "notes.txt")); err != nil {
		t.Fatal("unsupported source removed")
	}
}
func TestServiceStateLock(t *testing.T) {
	s, c, _, _ := serviceFixture(t)
	if second, err := newArchiveService(c); err == nil {
		second.Close()
		t.Fatal("same state directory opened twice")
	}
	s.Close()
	next, err := newArchiveService(c)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
}
func TestServiceInterruptedScanRequiresRescan(t *testing.T) {
	s, c, source, target := serviceFixture(t)
	serviceWrite(t, filepath.Join(source, "IMG_20260701_120000.jpg"), []byte("one"))
	j, err := s.createJob([]string{source}, target, false)
	if err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, j.ID, "ready")
	s.mu.Lock()
	stored := s.jobs[j.ID]
	stored.ScanComplete = false
	stored.Status = "scanning"
	if err = s.changed(stored, nil); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	s.Close()
	next, err := newArchiveService(c)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, err = next.jobAction(j.ID, "start"); err == nil {
		t.Fatal("incomplete scan allowed to move")
	}
	serviceWrite(t, filepath.Join(source, "IMG_20260702_120000.jpg"), []byte("two"))
	if _, err = next.jobAction(j.ID, "retry"); err != nil {
		t.Fatal(err)
	}
	ready := serviceWait(t, next, j.ID, "ready")
	if !ready.ScanComplete || ready.TotalFiles != 2 {
		t.Fatalf("rescan lost files: %+v", ready)
	}
}
func TestServiceRecoveryVerifiesPublishedDestination(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "corrupt"}[corrupt], func(t *testing.T) {
			s, c, source, target := serviceFixture(t)
			payload := []byte("recover this media")
			path := filepath.Join(source, "IMG_20260701_120000.jpg")
			serviceWrite(t, path, payload)
			j, err := s.createJob([]string{source}, target, false)
			if err != nil {
				t.Fatal(err)
			}
			serviceWait(t, s, j.ID, "ready")
			s.mu.Lock()
			stored := s.jobs[j.ID]
			f := stored.Files[0]
			if err = os.MkdirAll(filepath.Dir(f.TargetPath), 0755); err != nil {
				t.Fatal(err)
			}
			data := payload
			if corrupt {
				data = []byte("corrupt destination")
			}
			serviceWrite(t, f.TargetPath, data)
			if err = os.Remove(path); err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(payload)
			f.SHA256 = hex.EncodeToString(hash[:])
			f.Status = "copying"
			f.Published = true
			stored.Status = "running"
			if err = s.changed(stored, f); err != nil {
				t.Fatal(err)
			}
			s.mu.Unlock()
			s.Close()
			next, err := newArchiveService(c)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			if _, err = next.jobAction(j.ID, "start"); err != nil {
				t.Fatal(err)
			}
			result := serviceWait(t, next, j.ID, "completed", "completed_with_errors", "failed")
			if !corrupt && (result.Status != "completed" || result.CompletedFiles != 1) {
				t.Fatalf("verified journal not recovered: %+v", result)
			}
			if corrupt && result.FailedFiles != 1 {
				t.Fatalf("corrupt target accepted: %+v", result)
			}
		})
	}
}
func TestServiceRetryRetainsPublishedJournal(t *testing.T) {
	s, _, source, target := serviceFixture(t)
	payload := []byte("journal")
	path := filepath.Join(source, "IMG_20260701_120000.jpg")
	serviceWrite(t, path, payload)
	j, err := s.createJob([]string{source}, target, false)
	if err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, j.ID, "ready")
	s.mu.Lock()
	stored := s.jobs[j.ID]
	f := stored.Files[0]
	os.MkdirAll(filepath.Dir(f.TargetPath), 0755)
	serviceWrite(t, f.TargetPath, payload)
	os.Remove(path)
	hash := sha256.Sum256(payload)
	f.SHA256 = hex.EncodeToString(hash[:])
	f.Published = true
	f.Status = "failed"
	stored.Status = "completed_with_errors"
	if err = s.changed(stored, f); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	if _, err = s.jobAction(j.ID, "retry"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.jobAction(j.ID, "start"); err != nil {
		t.Fatal(err)
	}
	result := serviceWait(t, s, j.ID, "completed", "completed_with_errors")
	if result.CompletedFiles != 1 {
		t.Fatalf("retry bypassed journal: %+v", result)
	}
}
func TestServiceRechecksClaimsOnRestart(t *testing.T) {
	s, _, source, target := serviceFixture(t)
	serviceWrite(t, filepath.Join(source, "IMG_20260701_120000.jpg"), []byte("one"))
	j, err := s.createJob([]string{source}, target, false)
	if err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, j.ID, "ready")
	s.mu.Lock()
	old := s.jobs[j.ID]
	old.Status = "completed_with_errors"
	s.changed(old, nil)
	s.mu.Unlock()
	newer, err := s.createJob([]string{source}, target, false)
	if err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, newer.ID, "ready")
	if _, err = s.jobAction(j.ID, "retry"); err == nil {
		t.Fatal("older task reclaimed active source")
	}
	nested := filepath.Join(target, "another-source")
	os.MkdirAll(nested, 0755)
	if _, err = s.createJob([]string{nested}, filepath.Join(filepath.Dir(target), "other-archive"), false); err == nil {
		t.Fatal("source within active destination accepted")
	}
}
func TestServiceAuthenticatedBrowseRejectsEscapingSymlink(t *testing.T) {
	s, _, source, _ := serviceFixture(t)
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(source, "escape"))
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	for _, test := range []struct {
		path   string
		auth   bool
		status int
	}{{"/api/v1/info", false, 401}, {"/api/v1/browse?path=" + filepath.Join(source, "escape"), true, 403}, {"/healthz", false, 200}} {
		req, _ := http.NewRequest("GET", server.URL+test.path, nil)
		if test.auth {
			req.Header.Set("Authorization", "Bearer "+s.token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != test.status {
			t.Errorf("%s: got %d", test.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/info", nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var info map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&info)
	if info["name"] != "Test NAS" {
		t.Fatal(info)
	}
}

// Stop the scheduler before enqueueing so the queued-pause transition is tested
// deterministically, without relying on a second large transfer to win a race.
func TestServiceQueuedPauseAndResume(t *testing.T) {
	s, _, source, target := serviceFixture(t)
	close(s.stop)
	s.wg.Wait()
	s.stop = make(chan struct{})
	path := filepath.Join(source, "IMG_20260701_120000.jpg")
	serviceWrite(t, path, []byte("queued media"))
	job, err := s.createJob([]string{source}, target, false)
	if err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, job.ID, "ready")
	queued, err := s.jobAction(job.ID, "start")
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != "queued" {
		t.Fatalf("expected queued state, got %+v", queued)
	}
	paused, err := s.jobAction(job.ID, "pause")
	if err != nil {
		t.Fatal(err)
	}
	if paused.Status != "paused" || paused.CompletedFiles != 0 {
		t.Fatalf("queued pause moved files: %+v", paused)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "queued media" {
		t.Fatalf("queued source changed: %q %v", got, err)
	}
	if _, err = s.jobAction(job.ID, "start"); err != nil {
		t.Fatal(err)
	}
	s.wg.Add(1)
	go s.worker()
	completed := serviceWait(t, s, job.ID, "completed", "completed_with_errors", "failed")
	if completed.Status != "completed" || completed.CompletedFiles != 1 {
		t.Fatalf("queued resume failed: %+v", completed)
	}
}

func TestServiceActivePauseFinishesCurrentFileBeforeResume(t *testing.T) {
	s, _, source, target := serviceFixture(t)
	first := filepath.Join(source, "IMG_20260701_120000.jpg")
	second := filepath.Join(source, "IMG_20260702_120000.jpg")
	// A sparse fixture gives the real copy/checksum pipeline a visible in-flight
	// interval without allocating a large byte slice in the test process.
	file, err := os.Create(first)
	if err != nil {
		t.Fatal(err)
	}
	const size = int64(128 * 1024 * 1024)
	if err = file.Truncate(size); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	serviceWrite(t, second, []byte("next file must remain until resume"))
	job, err := s.createJob([]string{source}, target, false)
	if err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, job.ID, "ready")
	if _, err = s.jobAction(job.ID, "start"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.mu.Lock()
		current := serviceJobSnapshot(s.jobs[job.ID])
		s.mu.Unlock()
		if current.Status == "running" && current.CurrentFile == first && current.CurrentFileBytes > 0 {
			break
		}
		if serviceTerminal(current.Status) || time.Now().After(deadline) {
			t.Fatalf("missed active transfer: %+v", current)
		}
		time.Sleep(time.Millisecond)
	}
	pausing, err := s.jobAction(job.ID, "pause")
	if err != nil {
		t.Fatal(err)
	}
	if pausing.Status != "pausing" {
		t.Fatalf("active pause did not wait at file boundary: %+v", pausing)
	}
	paused := serviceWait(t, s, job.ID, "paused", "completed", "completed_with_errors", "failed")
	if paused.Status != "paused" || paused.CompletedFiles != 1 || paused.CompletedBytes != size {
		t.Fatalf("pause was not at first file boundary: %+v", paused)
	}
	if _, err = os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("completed first file remained at source")
	}
	if got, err := os.ReadFile(second); err != nil || string(got) != "next file must remain until resume" {
		t.Fatalf("pause consumed next source: %q %v", got, err)
	}
	s.mu.Lock()
	firstRecord := *s.jobs[job.ID].Files[0]
	secondRecord := *s.jobs[job.ID].Files[1]
	s.mu.Unlock()
	if firstRecord.Status != "completed" || firstRecord.SHA256 == "" {
		t.Fatalf("current file was not safely verified: %+v", firstRecord)
	}
	if secondRecord.Status != "planned" {
		t.Fatalf("next file started while paused: %+v", secondRecord)
	}
	if info, err := os.Stat(firstRecord.TargetPath); err != nil || info.Size() != size {
		t.Fatalf("first target invalid: %v %v", info, err)
	}
	if _, err = s.jobAction(job.ID, "start"); err != nil {
		t.Fatal(err)
	}
	completed := serviceWait(t, s, job.ID, "completed", "completed_with_errors", "failed")
	if completed.Status != "completed" || completed.CompletedFiles != 2 {
		t.Fatalf("active resume failed: %+v", completed)
	}
}

func TestServicePartialScanRescanReleasesClaimAfterCompletion(t *testing.T) {
	s, c, source, target := serviceFixture(t)
	serviceWrite(t, filepath.Join(source, "IMG_20260701_120000.jpg"), []byte("rescan then archive"))
	job, err := s.createJob([]string{source}, target, false)
	if err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, job.ID, "ready")
	s.mu.Lock()
	stored := s.jobs[job.ID]
	stored.ScanComplete = false
	stored.Status = "scanning"
	err = s.changed(stored, nil)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	next, err := newArchiveService(c)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, err = next.jobAction(job.ID, "retry"); err != nil {
		t.Fatal(err)
	}
	serviceWait(t, next, job.ID, "ready")
	if _, err = next.jobAction(job.ID, "start"); err != nil {
		t.Fatal(err)
	}
	result := serviceWait(t, next, job.ID, "completed", "completed_with_errors", "failed")
	if result.Status != "completed" {
		t.Fatalf("rescanned task did not finish: %+v", result)
	}
	// The completed rescanned task must not permanently reserve its source root.
	serviceWrite(t, filepath.Join(source, "IMG_20260702_120000.jpg"), []byte("new batch at same path"))
	later, err := next.createJob([]string{source}, target, false)
	if err != nil {
		t.Fatalf("rescan left stale source claim: %v", err)
	}
	ready := serviceWait(t, next, later.ID, "ready")
	if ready.TotalFiles != 1 {
		t.Fatalf("unexpected new preview: %+v", ready)
	}
}
