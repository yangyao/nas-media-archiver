package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServiceReadOnlyInspectionShowsResidualsAndRejectsEscape(t *testing.T) {
	s, _, source, _ := serviceFixture(t)
	serviceWrite(t, filepath.Join(source, "left.gif"), []byte("gif"))
	marker := filepath.Join(source, ".stfolder")
	if err := os.Mkdir(marker, 0755); err != nil {
		t.Fatal(err)
	}
	serviceWrite(t, filepath.Join(marker, "syncthing-marker.txt"), []byte("keep"))
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(source, "escape"))
	for _, test := range []struct {
		path string
		code int
	}{{source, 200}, {marker, 200}, {filepath.Join(source, "escape"), 403}, {outside, 403}} {
		req := httptest.NewRequest("GET", "/api/v1/browse?includeFiles=true&path="+url.QueryEscape(test.path), nil)
		req.Header.Set("Authorization", "Bearer "+s.token)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != test.code {
			t.Fatalf("inspection %s: %d %s", test.path, w.Code, w.Body.String())
		}
		if test.path == source {
			var listing struct {
				Entries []struct {
					Name    string
					System  bool
					CanOpen bool
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil {
				t.Fatal(err)
			}
			found := map[string]bool{}
			for _, e := range listing.Entries {
				found[e.Name] = true
				if e.Name == ".stfolder" && !e.System {
					t.Fatal("system folder not identified")
				}
				if e.Name == "escape" && e.CanOpen {
					t.Fatal("symlink navigation exposed")
				}
			}
			if !found["left.gif"] || !found[".stfolder"] {
				t.Fatal("residual content hidden")
			}
		}
	}
	if _, err := s.allowedPath(marker, true); err == nil {
		t.Fatal("inspection accidentally relaxed mutating path policy")
	}
}
func TestServiceReviewDateReplansAndAcknowledgementPersists(t *testing.T) {
	s, c, source, target := serviceFixture(t)
	path := filepath.Join(source, "capture.jpg")
	payload := []byte("keep original")
	serviceWrite(t, path, payload)
	j, err := s.createJob([]string{source}, target, false)
	if err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, j.ID, "ready")
	s.mu.Lock()
	f := *s.jobs[j.ID].Files[0]
	s.mu.Unlock()
	if f.MetadataSource != "mtime" || f.Warning == "" {
		t.Fatal("fixture has no date warning")
	}
	manual := "2020-06-01T12:30:00+08:00"
	updated, err := s.reviewFile(j.ID, f.ID, "date", manual)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AttentionFiles != 0 || updated.DateRangeStart != manual {
		t.Fatalf("date/counters not updated: %+v", updated)
	}
	s.mu.Lock()
	now := *s.jobs[j.ID].Files[0]
	s.mu.Unlock()
	if now.MetadataSource != "manual" || !strings.Contains(now.TargetPath, "/2020/06/20200601_123000_") {
		t.Fatalf("date did not replan: %+v", now)
	}
	if got, _ := os.ReadFile(path); string(got) != string(payload) {
		t.Fatal("date review modified source")
	}
	s.mu.Lock()
	shared := s.jobs[j.ID].Files[0]
	shared.Warning = "A duplicate name will be saved with a suffix."
	s.changed(s.jobs[j.ID], shared)
	s.mu.Unlock()
	updated, err = s.reviewFile(j.ID, f.ID, "acknowledge", "")
	if err != nil || updated.AttentionFiles != 0 {
		t.Fatal(updated, err)
	}
	// Retry must preserve a manually supplied date for unchanged source files.
	s.mu.Lock()
	shared.Status = "failed"
	shared.Error = "temporary IO failure"
	s.jobs[j.ID].Status = "completed_with_errors"
	s.changed(s.jobs[j.ID], shared)
	s.mu.Unlock()
	if _, err = s.jobAction(j.ID, "retry"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	manualAfterRetry := *s.jobs[j.ID].Files[0]
	s.mu.Unlock()
	if manualAfterRetry.MetadataSource != "manual" || manualAfterRetry.DateTime != manual {
		t.Fatal("retry discarded manual date")
	}
	s.Close()
	next, err := newArchiveService(c)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	restored := next.jobs[j.ID].Files[0]
	if restored.MetadataSource != "manual" || restored.Warning != "" {
		t.Fatal("review lost on restart")
	}
}
func TestServiceReviewRejectsUnsafeTransitions(t *testing.T) {
	s, _, source, target := serviceFixture(t)
	path := filepath.Join(source, "IMG_20260701_120000.jpg")
	serviceWrite(t, path, []byte("original"))
	j, _ := s.createJob([]string{source}, target, false)
	serviceWait(t, s, j.ID, "ready")
	s.mu.Lock()
	f := s.jobs[j.ID].Files[0]
	s.jobs[j.ID].Status = "running"
	s.mu.Unlock()
	if _, err := s.reviewFile(j.ID, f.ID, "date", "2020-01-01T00:00:00Z"); err == nil {
		t.Fatal("date changed while running")
	}
	s.mu.Lock()
	s.jobs[j.ID].Status = "ready"
	s.mu.Unlock()
	info, _ := os.Stat(path)
	os.Chtimes(path, time.Now(), info.ModTime().Add(time.Second))
	if _, err := s.reviewFile(j.ID, f.ID, "date", "2020-01-01T00:00:00Z"); err == nil {
		t.Fatal("changed source accepted")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+j.ID+"/files/"+f.ID+"/date", strings.NewReader(`{"dateTime":"invalid"}`))
	req.Header.Set("Authorization", "Bearer "+s.token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	s.mu.Lock()
	f.Status = "unsupported"
	s.mu.Unlock()
	if _, err := s.reviewFile(j.ID, f.ID, "acknowledge", ""); err == nil {
		t.Fatal("unsupported file silently dismissed")
	}
}
