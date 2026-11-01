package batchsplit

import (
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "iphone15")
	if e := os.Mkdir(source, 0755); e != nil {
		t.Fatal(e)
	}
	return root, source
}
func put(t *testing.T, path, data string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(data), 0644); e != nil {
		t.Fatal(e)
	}
}
func TestSmallDirectoryStillCreatesSiblingAndPreservesMetadata(t *testing.T) {
	_, src := fixture(t)
	put(t, filepath.Join(src, "a.jpg"), "original")
	put(t, filepath.Join(src, ".verysync", "state"), "sync")
	put(t, filepath.Join(src, ".nomedia"), "")
	original, _ := os.Stat(filepath.Join(src, "a.jpg"))
	p, e := New(src, "iPhone 15", DefaultLimit)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Batches) != 1 || filepath.Base(p.Batches[0].Path) != "iPhone 15 batch 01" || p.RetainedEntries != 2 {
		t.Fatal(p)
	}
	if _, e = os.Stat(p.Batches[0].Path); !os.IsNotExist(e) {
		t.Fatal("preview changed filesystem")
	}
	if e = p.Reserve(); e != nil {
		t.Fatal(e)
	}
	if e = p.Execute(func() bool { return false }, func(n int, b int64) {
		if n != 1 || b != 8 {
			t.Fatal(n, b)
		}
	}); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(p.Batches[0].Path, "a.jpg")
	info, e := os.Stat(target)
	if e != nil || !os.SameFile(original, info) || !info.ModTime().Equal(original.ModTime()) {
		t.Fatal("metadata changed", e)
	}
	if _, e = os.Stat(filepath.Join(src, "a.jpg")); !os.IsNotExist(e) {
		t.Fatal("source not moved")
	}
	if _, e = os.Stat(filepath.Join(src, ".verysync", "state")); e != nil {
		t.Fatal(e)
	}
	if e = p.Execute(func() bool { return false }, func(int, int64) {}); e != nil {
		t.Fatal("recovery", e)
	}
}
func TestSizeBoundsCollisionsAndNestedPaths(t *testing.T) {
	root, src := fixture(t)
	os.Mkdir(filepath.Join(root, "iPhone 15 batch 01"), 0755)
	put(t, filepath.Join(src, "nested", "a"), "1234")
	put(t, filepath.Join(src, "nested", "b"), "5678")
	put(t, filepath.Join(src, "z"), "oversized")
	p, e := New(src, "iPhone 15", 4)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Batches) != 3 || filepath.Base(p.Batches[0].Path) != "iPhone 15 batch 02" || !p.Batches[2].Oversized {
		t.Fatal(p)
	}
	if e = p.Reserve(); e != nil {
		t.Fatal(e)
	}
	if e = p.Execute(func() bool { return false }, func(int, int64) {}); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(p.Batches[0].Path, "nested", "a"))
	if e != nil || string(b) != "1234" {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(src, "nested")); !os.IsNotExist(e) {
		t.Fatal("empty descendant not removed")
	}
	if _, e = os.Stat(src); e != nil {
		t.Fatal("source root removed")
	}
}
func TestChangedFileAndCollisionNeverOverwrite(t *testing.T) {
	_, src := fixture(t)
	put(t, filepath.Join(src, "a"), "before")
	p, _ := New(src, "batch", 10)
	put(t, filepath.Join(src, "a"), "changed")
	if e := p.Reserve(); e == nil {
		t.Fatal("changed source accepted")
	}
	p, _ = New(src, "batch", 10)
	if e := p.Reserve(); e != nil {
		t.Fatal(e)
	}
	put(t, filepath.Join(p.Batches[0].Path, "a"), "existing")
	if e := p.Execute(func() bool { return false }, func(int, int64) {}); e == nil {
		t.Fatal("overwrote target")
	}
	b, _ := os.ReadFile(filepath.Join(p.Batches[0].Path, "a"))
	if string(b) != "existing" {
		t.Fatal(string(b))
	}
}
func TestResumeInterruptedLinkAndKeepNewFiles(t *testing.T) {
	_, src := fixture(t)
	put(t, filepath.Join(src, "a"), "a")
	put(t, filepath.Join(src, "b"), "b")
	p, _ := New(src, "batch", 10)
	if e := p.Reserve(); e != nil {
		t.Fatal(e)
	}
	if e := os.Link(filepath.Join(src, "a"), filepath.Join(p.Batches[0].Path, "a")); e != nil {
		t.Fatal(e)
	}
	put(t, filepath.Join(src, "new"), "new")
	if e := p.Execute(func() bool { return false }, func(int, int64) {}); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(src, "new")); e != nil {
		t.Fatal("new file removed")
	}
}
func TestEmptyAndSymlink(t *testing.T) {
	_, src := fixture(t)
	os.Symlink(t.TempDir(), filepath.Join(src, "alias"))
	p, e := New(src, "ok", 8)
	if e != nil || len(p.Batches) != 0 || p.RetainedEntries != 1 {
		t.Fatal(p, e)
	}
	if _, e = New(src, "../escape", 8); e == nil {
		t.Fatal("unsafe prefix")
	}
}

func TestHiddenSourceCannotMoveSyncState(t *testing.T) {
	root, _ := fixture(t)
	src := filepath.Join(root, ".verysync", "Archive")
	put(t, filepath.Join(src, "state"), "sync")
	if _, e := New(src, "visible", 8); e == nil {
		t.Fatal("hidden source accepted")
	}
}
