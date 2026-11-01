package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveSettingsPersistenceAndValidation(t *testing.T) {
	s, c, source, target := serviceFixture(t)
	call := func(v ArchiveSettings) int {
		b, _ := json.Marshal(v)
		r := httptest.NewRequest("PUT", "/api/v1/settings", bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+s.token)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w.Code
	}
	good := ArchiveSettings{SourceRoots: []string{source}, ArchiveBase: target}
	if code := call(good); code != 200 {
		t.Fatal(code)
	}
	if !s.settingsSnapshot().Configured {
		t.Fatal("not configured")
	}
	if code := call(ArchiveSettings{SourceRoots: []string{target}, ArchiveBase: target}); code != 400 {
		t.Fatal("overlap", code)
	}
	if code := call(ArchiveSettings{SourceRoots: []string{t.TempDir()}, ArchiveBase: target}); code != 400 {
		t.Fatal("escaped access roots", code)
	}
	s.mu.Lock()
	s.jobs["test"] = &ServiceJob{Status: "scanning"}
	s.mu.Unlock()
	if code := call(good); code != 409 {
		t.Fatal("busy", code)
	}
	s.mu.Lock()
	delete(s.jobs, "test")
	s.mu.Unlock()
	info, err := os.Stat(filepath.Join(c.StateDir, "settings.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("settings permissions", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := newArchiveService(c)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	v := again.settingsSnapshot()
	if len(v.SourceRoots) != 1 || v.SourceRoots[0] != source || v.ArchiveBase != target {
		t.Fatal(v)
	}
}
func TestUnconfiguredServiceConnectsButCannotCreateJobs(t *testing.T) {
	root := t.TempDir()
	c := ServiceConfig{StateDir: filepath.Join(root, "state"), Roots: []string{root}}
	s, err := newArchiveService(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.settingsSnapshot().Configured {
		t.Fatal("unexpected defaults")
	}
	if _, err = s.createJob([]string{root}, root, false); err == nil {
		t.Fatal("unconfigured job accepted")
	}
}
