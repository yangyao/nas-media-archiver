package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yangyao/nas-media-archiver/internal/batchsplit"
)

func splitCall(t *testing.T, s *archiveService, method, path string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "/api/v1/tools/splits"+path, bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+s.token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var result map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	return w.Code, result
}
func TestSplitAPIRequiresExplicitExecutionAndPreservesSource(t *testing.T) {
	s, _, src, _ := serviceFixture(t)
	serviceWrite(t, filepath.Join(src, "a.jpg"), []byte("media"))
	code, preview := splitCall(t, s, "POST", "/preview", map[string]any{"sourcePath": src, "prefix": "iPhone 15"})
	if code != 200 {
		t.Fatal(code, preview)
	}
	id := preview["id"].(string)
	if _, e := os.Stat(filepath.Join(src, "a.jpg")); e != nil {
		t.Fatal("preview moved a file")
	}
	if preview["totalFiles"] != float64(1) {
		t.Fatal(preview)
	}
	code, result := splitCall(t, s, "POST", "/"+id+"/start", nil)
	if code != 202 {
		t.Fatal(code, result)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		code, result = splitCall(t, s, "GET", "/"+id, nil)
		if result["status"] == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if result["status"] != "completed" {
		t.Fatal(result)
	}
	dst := filepath.Join(filepath.Dir(src), "iPhone 15 batch 01", "a.jpg")
	b, e := os.ReadFile(dst)
	if e != nil || string(b) != "media" {
		t.Fatal(e)
	}
	if _, e = os.Stat(src); e != nil {
		t.Fatal("source root removed")
	}
	if _, e = s.createJob([]string{filepath.Dir(dst)}, "", false); e != nil {
		t.Fatal("completed split claim was not released", e)
	}
}
func TestSplitRejectsArchiveOutsideRootsAndArchiveClaims(t *testing.T) {
	s, _, src, target := serviceFixture(t)
	serviceWrite(t, filepath.Join(src, "a"), []byte("a"))
	for _, path := range []string{target, t.TempDir(), s.config.StateDir} {
		if code, _ := splitCall(t, s, "POST", "/preview", map[string]any{"sourcePath": path}); code == 200 {
			t.Fatal("invalid source accepted", path)
		}
	}
	s.mu.Lock()
	s.jobs["existing"] = &ServiceJob{ID: "existing", Status: "ready", SourcePaths: []string{src}, ArchiveBase: target}
	s.mu.Unlock()
	if code, _ := splitCall(t, s, "POST", "/preview", map[string]any{"sourcePath": src}); code != 409 {
		t.Fatal("archive claim ignored", code)
	}
}
func TestSplitRecoveryAfterRestartAndMutualClaims(t *testing.T) {
	s, c, src, target := serviceFixture(t)
	serviceWrite(t, filepath.Join(src, "a"), []byte("a"))
	p, e := batchsplit.New(src, "test", 8)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Reserve(); e != nil {
		t.Fatal(e)
	}
	if e = os.Link(filepath.Join(src, "a"), filepath.Join(p.Batches[0].Path, "a")); e != nil {
		t.Fatal(e)
	}
	j := &SplitJob{ID: "split-recovery", Status: "running", CreatedAt: time.Now(), Plan: p}
	s.mu.Lock()
	s.splits[j.ID] = j
	e = s.saveSplitLocked(j)
	s.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.createJob([]string{src}, target, false); e == nil {
		t.Fatal("split claim ignored")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	again, e := newArchiveService(c)
	if e != nil {
		t.Fatal(e)
	}
	defer again.Close()
	if again.splits[j.ID].Status != "interrupted" {
		t.Fatal("restart status")
	}
	code, result := splitCall(t, again, "POST", "/"+j.ID+"/start", nil)
	if code != 202 {
		t.Fatal(code, result)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, result = splitCall(t, again, "GET", "/"+j.ID, nil)
		if result["status"] == "completed" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(result)
}
