package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestServiceSourceBrowseSeparatesArchiveAndSource(t *testing.T) {
	s, _, source, target := serviceFixture(t)
	s.config.Roots = []string{source, target}
	if err := os.Symlink(target, filepath.Join(source, "archive-alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, "batch"), 0755); err != nil {
		t.Fatal(err)
	}
	type listing struct {
		Path       string
		Selectable bool
		Entries    []struct {
			Name, Path string
			Selectable bool
		}
	}
	request := func(path string, sourceOnly bool) (int, listing) {
		t.Helper()
		values := url.Values{"path": {path}}
		if sourceOnly {
			values.Set("purpose", "source")
		}
		req := httptest.NewRequest("GET", "/api/v1/browse?"+values.Encode(), nil)
		req.Header.Set("Authorization", "Bearer "+s.token)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		var result listing
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code, result
	}
	code, roots := request("", true)
	if code != 200 || len(roots.Entries) != 1 || roots.Entries[0].Path != source || !roots.Entries[0].Selectable || roots.Selectable {
		t.Fatalf("source roots include destination: %+v %d", roots, code)
	}
	_, generic := request("", false)
	if len(generic.Entries) != 2 {
		t.Fatal("generic browse behavior changed")
	}
	if code, _ = request(target, true); code != 403 {
		t.Fatal("typed archive source accepted", code)
	}
	if code, _ = request(target, false); code != 200 {
		t.Fatal("archive inspection blocked", code)
	}
	_, children := request(source, true)
	if len(children.Entries) != 1 || children.Entries[0].Name != "batch" {
		t.Fatalf("archive alias exposed: %+v", children)
	}
}
func TestServiceSourceBrowseKeepsAncestorNavigableButNotSelectable(t *testing.T) {
	s, config, source, target := serviceFixture(t)
	root := config.Roots[0]
	req := httptest.NewRequest("GET", "/api/v1/browse?"+url.Values{"purpose": {"source"}, "path": {root}, "archiveBase": {target}}.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Selectable bool
		Entries    []struct {
			Path       string
			Selectable bool
		}
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Selectable {
		t.Fatal("ancestor containing archive/state can be selected")
	}
	for _, e := range result.Entries {
		if e.Path == target || e.Path == s.config.StateDir {
			t.Fatal("destination/state exposed", e.Path)
		}
	}
	// A different per-job destination is excluded as well.
	custom := filepath.Join(source, "other-archive")
	os.Mkdir(custom, 0755)
	req = httptest.NewRequest("GET", "/api/v1/browse?"+url.Values{"purpose": {"source"}, "path": {source}, "archiveBase": {custom}}.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Selectable || len(result.Entries) != 0 {
		t.Fatal("custom destination not excluded", w.Body.String())
	}
}
