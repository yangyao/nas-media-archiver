package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteTaskRecordPreservesFilesAndSurvivesRestart(t *testing.T) {
	s, c, src, target := serviceFixture(t)
	serviceWrite(t, filepath.Join(src, "keep.txt"), []byte("source"))
	serviceWrite(t, filepath.Join(target, "keep.jpg"), []byte("archive"))
	j, e := s.createJob([]string{src}, target, false)
	if e != nil {
		t.Fatal(e)
	}
	serviceWait(t, s, j.ID, "ready")
	r := httptest.NewRequest("DELETE", "/api/v1/jobs/"+j.ID, nil)
	r.Header.Set("Authorization", "Bearer "+s.token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(s.listJobs()) != 0 {
		t.Fatal("task remains in memory")
	}
	for p, want := range map[string]string{filepath.Join(src, "keep.txt"): "source", filepath.Join(target, "keep.jpg"): "archive"} {
		b, e := os.ReadFile(p)
		if e != nil || string(b) != want {
			t.Fatal("media changed", p, e)
		}
	}
	var files int
	if e = s.store.db.QueryRow(`SELECT count(*) FROM service_files WHERE job_id=?`, j.ID).Scan(&files); e != nil || files != 0 {
		t.Fatal("file records remain", files, e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	again, e := newArchiveService(c)
	if e != nil {
		t.Fatal(e)
	}
	defer again.Close()
	if len(again.listJobs()) != 0 {
		t.Fatal("deleted task resurrected")
	}
	if _, e = again.createJob([]string{src}, target, false); e != nil {
		t.Fatal("directory claim was not released", e)
	}
}
func TestDeleteTaskEligibilityAndServerEnforcement(t *testing.T) {
	for _, v := range []struct {
		status  string
		started bool
		want    bool
	}{
		{"ready", false, true}, {"completed", true, true}, {"completed_with_errors", true, true}, {"failed", false, true}, {"interrupted", false, true},
		{"scanning", false, false}, {"running", true, false}, {"queued", true, false}, {"pausing", true, false}, {"paused", true, false}, {"interrupted", true, false}, {"failed", true, false},
	} {
		j := &ServiceJob{Status: v.status, Started: v.started}
		if canDeleteServiceJob(j) != v.want {
			t.Fatal(v)
		}
	}
	j := &ServiceJob{Status: "completed_with_errors", Started: true, Files: []*ServiceFile{{Status: "failed", StagingPath: "/recover"}}}
	if canDeleteServiceJob(j) {
		t.Fatal("lost recovery journal")
	}
	s, _, _, _ := serviceFixture(t)
	s.mu.Lock()
	s.jobs["active"] = &ServiceJob{ID: "active", Status: "running", Started: true}
	s.mu.Unlock()
	r := httptest.NewRequest("DELETE", "/api/v1/jobs/active", nil)
	r.Header.Set("Authorization", "Bearer "+s.token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	if len(s.listJobs()) != 1 {
		t.Fatal("active task removed")
	}
}
func TestDeleteDiscardsFailedInflightCheckpointWithoutResurrection(t *testing.T) {
	store, e := openServiceStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	j := &ServiceJob{ID: "delete-race", Status: "ready"}
	if e = store.save(j, &ServiceFile{ID: "file"}); e != nil {
		t.Fatal(e)
	}
	if _, e = store.db.Exec(`CREATE TRIGGER fail_checkpoint BEFORE INSERT ON service_jobs BEGIN SELECT RAISE(FAIL,'injected failure'); END`); e != nil {
		t.Fatal(e)
	}
	conn, e := store.db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	flushed := make(chan error, 1)
	go func() { _, e := store.flushBatch(); flushed <- e }()
	deadline := time.Now().Add(time.Second)
	for {
		store.mu.Lock()
		n := store.inFlight
		store.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no in-flight write")
		}
		time.Sleep(time.Millisecond)
	}
	deleted := store.deleteJob(j.ID)
	conn.Close()
	if e = <-flushed; e == nil {
		t.Fatal("failure not injected")
	}
	if _, e = store.db.Exec(`DROP TRIGGER fail_checkpoint`); e != nil {
		t.Fatal(e)
	}
	if _, e = store.flushBatch(); e != nil {
		t.Fatal(e)
	}
	if e = <-deleted; e != nil {
		t.Fatal(e)
	}
	loaded, e := store.load()
	if e != nil || len(loaded) != 0 {
		t.Fatal("resurrected", loaded, e)
	}
	if e = store.save(j, nil); e == nil {
		t.Fatal("stale producer accepted")
	}
}
func TestDeletionPropagatesToConnectedEventStream(t *testing.T) {
	s, _, src, target := serviceFixture(t)
	j, e := s.createJob([]string{src}, target, false)
	if e != nil {
		t.Fatal(e)
	}
	serviceWait(t, s, j.ID, "ready")
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/events", nil)
	r.Header.Set("Authorization", "Bearer "+s.token)
	resp, e := http.DefaultClient.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	readJob := func() ServiceJob {
		t.Helper()
		for scanner.Scan() {
			line := scanner.Text()
			if len(line) > 6 && line[:6] == "data: " {
				var v ServiceJob
				if e := json.Unmarshal([]byte(line[6:]), &v); e != nil {
					t.Fatal(e)
				}
				return v
			}
		}
		t.Fatal("missing event", scanner.Err())
		return ServiceJob{}
	}
	if readJob().ID != j.ID {
		t.Fatal("missing initial task")
	}
	done := make(chan int, 1)
	go func() {
		r := httptest.NewRequest("DELETE", "/api/v1/jobs/"+j.ID, nil)
		r.Header.Set("Authorization", "Bearer "+s.token)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		done <- w.Code
	}()
	deleted := readJob()
	if deleted.ID != j.ID || deleted.Status != "deleted" {
		t.Fatal(deleted)
	}
	if code := <-done; code != 200 {
		t.Fatal(code)
	}
}
