package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var serviceArchiveName = regexp.MustCompile(`^(\d{8}_\d{6}_\d+)(?:_\d+)?(\.[^.]+)$`)

// Index names lazily, once per destination directory. This is a candidate
// index, not a content hash index: unrelated archive files are never read.
type serviceDuplicateIndex struct {
	loaded map[string]bool
	paths  map[string][]string
}

func newServiceDuplicateIndex() *serviceDuplicateIndex {
	return &serviceDuplicateIndex{loaded: map[string]bool{}, paths: map[string][]string{}}
}
func (d *serviceDuplicateIndex) add(path string) {
	match := serviceArchiveName.FindStringSubmatch(filepath.Base(path))
	if match == nil {
		return
	}
	key := filepath.Join(filepath.Dir(path), match[1]+strings.ToLower(match[2]))
	for _, existing := range d.paths[key] {
		if existing == path {
			return
		}
	}
	d.paths[key] = append(d.paths[key], path)
}
func (d *serviceDuplicateIndex) candidates(base string) ([]string, error) {
	dir := filepath.Dir(base)
	if !d.loaded[dir] {
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, entry := range entries {
			if entry.Type().IsRegular() {
				d.add(filepath.Join(dir, entry.Name()))
			}
		}
		d.loaded[dir] = true
	}
	return d.paths[base], nil
}
func serviceDuplicateBase(j *ServiceJob, f *ServiceFile) (string, error) {
	dt, err := time.Parse(time.RFC3339, f.DateTime)
	if err != nil {
		return "", err
	}
	return filepath.Join(j.ArchiveBase, classifyArchiveType(f.SourcePath), dt.Format("2006"), dt.Format("01"), fmt.Sprintf("%s_%d%s", dt.Format("20060102_150405"), f.Size, strings.ToLower(filepath.Ext(f.SourcePath)))), nil
}

// Compare bytes rather than hashes: equality requires reading all bytes, but
// different candidates stop at the first differing chunk. Both files remain
// untouched. Stat/identity checks reject files changed during comparison.
func serviceSameContents(source, target string, size int64, mtime time.Time, progress func(int64)) (bool, error) {
	aInfo, err := os.Lstat(source)
	if err != nil {
		return false, err
	}
	if !aInfo.Mode().IsRegular() || aInfo.Size() != size || !aInfo.ModTime().Equal(mtime) {
		return false, errors.New("source changed since scan; retained")
	}
	bInfo, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !bInfo.Mode().IsRegular() || bInfo.Size() != size {
		return false, nil
	}
	a, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer a.Close()
	b, err := os.Open(target)
	if err != nil {
		return false, err
	}
	defer b.Close()
	openedA, err := a.Stat()
	if err != nil {
		return false, err
	}
	openedB, err := b.Stat()
	if err != nil {
		return false, err
	}
	if !os.SameFile(aInfo, openedA) || !os.SameFile(bInfo, openedB) {
		return false, errors.New("file replaced before duplicate comparison")
	}
	left, right := make([]byte, 128*1024), make([]byte, 128*1024)
	var compared int64
	lastUpdate := time.Now()
	for {
		n, ae := io.ReadFull(a, left)
		m, be := io.ReadFull(b, right)
		if ae != nil && ae != io.EOF && ae != io.ErrUnexpectedEOF {
			return false, ae
		}
		if be != nil && be != io.EOF && be != io.ErrUnexpectedEOF {
			return false, be
		}
		if n != m || !bytes.Equal(left[:n], right[:m]) {
			return false, nil
		}
		compared += int64(n)
		if progress != nil && time.Since(lastUpdate) >= 150*time.Millisecond {
			progress(compared)
			lastUpdate = time.Now()
		}
		if ae != nil || be != nil {
			break
		}
	}
	if compared != size {
		return false, errors.New("file size changed during duplicate comparison")
	}
	for _, pair := range []struct {
		path   string
		before os.FileInfo
	}{{source, aInfo}, {target, bInfo}} {
		now, e := os.Lstat(pair.path)
		if e != nil {
			return false, e
		}
		if !os.SameFile(pair.before, now) || pair.before.Size() != now.Size() || !pair.before.ModTime().Equal(now.ModTime()) {
			return false, errors.New("file changed during duplicate comparison")
		}
	}
	return true, nil
}
func (s *archiveService) skipDuplicate(j *ServiceJob, shared *ServiceFile, index *serviceDuplicateIndex) (bool, error) {
	s.mu.Lock()
	local := *shared
	s.mu.Unlock()
	// A partially published transfer must finish its existing recovery path.
	if local.SHA256 != "" || local.StagingPath != "" {
		return false, nil
	}
	source, e := s.allowedPath(local.SourcePath, false)
	if e != nil {
		return false, e
	}
	if source != local.SourcePath {
		return false, errors.New("source path changed")
	}
	base, err := serviceDuplicateBase(j, &local)
	if err != nil {
		return false, err
	}
	parent, e := s.allowedPath(filepath.Dir(base), false)
	if e != nil {
		return false, e
	}
	if parent != filepath.Dir(base) {
		return false, errors.New("archive directory changed")
	}
	candidates, err := index.candidates(base)
	if err != nil {
		return false, err
	}
	if len(candidates) == 0 {
		return false, nil
	}
	local.Status = "checking"
	if err = s.saveTransfer(j, shared, &local); err != nil {
		return false, err
	}
	for _, path := range candidates {
		canonical, e := s.allowedPath(path, false)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return false, e
		}
		if canonical != path {
			continue
		}
		s.transferProgress(j, 0)
		same, e := serviceSameContents(local.SourcePath, path, local.Size, local.MTime, func(n int64) { s.transferProgress(j, n) })
		if e != nil {
			return false, e
		}
		if same {
			local.Status = "skipped"
			local.DuplicateOf = path
			local.Warning = ""
			local.Error = ""
			local.TargetPath = path
			return true, s.saveTransfer(j, shared, &local)
		}
	}
	return false, nil
}
func (s *archiveService) setSkipDuplicates(id string, enabled bool) (ServiceJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[id]
	if j == nil {
		return ServiceJob{}, os.ErrNotExist
	}
	if s.closing {
		return ServiceJob{}, errors.New("service is shutting down")
	}
	if j.Status != "ready" && j.Status != "paused" && j.Status != "interrupted" {
		return ServiceJob{}, errors.New("change duplicate handling before starting or while paused")
	}
	j.SkipDuplicates = enabled
	if err := s.changed(j, nil); err != nil {
		return ServiceJob{}, err
	}
	return serviceJobSnapshot(j), nil
}
