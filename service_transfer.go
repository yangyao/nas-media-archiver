package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func serviceSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func syncServiceDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (s *archiveService) saveTransfer(j *ServiceJob, shared, local *ServiceFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	*shared = *local
	return s.changed(j, shared)
}
func (s *archiveService) transferProgress(j *ServiceJob, n int64) {
	s.mu.Lock()
	j.CurrentFileBytes = n
	j.UpdatedAt = time.Now()
	s.mu.Unlock()
}

// A verified hash and exact destination are committed to SQLite before publish or
// source removal. A hard link publishes the destination without overwriting it.
func (s *archiveService) archiveFile(j *ServiceJob, shared *ServiceFile) error {
	s.mu.Lock()
	local := *shared
	s.mu.Unlock()
	f := &local
	source, err := s.allowedPath(f.SourcePath, false)
	if err != nil || source != f.SourcePath {
		return errors.New("source path changed or is outside configured roots")
	}
	parent, err := s.allowedPath(filepath.Dir(f.TargetPath), false)
	if f.TargetPath != "" && (err != nil || parent != filepath.Dir(f.TargetPath)) {
		return errors.New("destination path changed or is outside configured roots")
	}
	if f.SHA256 != "" {
		if _, e := os.Lstat(f.TargetPath); e == nil {
			// An interrupted publish is ours only when its staging inode is the same.
			if !f.Published {
				a, ae := os.Stat(f.StagingPath)
				b, be := os.Stat(f.TargetPath)
				if ae != nil || be != nil || !os.SameFile(a, b) {
					return errors.New("destination appeared while task was interrupted; source retained")
				}
				f.Published = true
				if err = s.saveTransfer(j, shared, f); err != nil {
					return err
				}
			}
			return s.finishPublished(j, shared, f)
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	info, err := os.Lstat(f.SourcePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("source is no longer a regular file")
	}
	if info.Size() != f.Size || !info.ModTime().Equal(f.MTime) {
		return errors.New("source changed since scan; create a new preview before moving")
	}
	if f.TargetPath == "" {
		dt, src, e := detectDateTime(f.SourcePath, f.MTime)
		if e != nil {
			return e
		}
		f.DateTime = dt.Format(time.RFC3339)
		f.MetadataSource = src
		f.TargetPath = filepath.Join(j.ArchiveBase, classifyArchiveType(f.SourcePath), dt.Format("2006"), dt.Format("01"), fmt.Sprintf("%s_%d%s", dt.Format("20060102_150405"), f.Size, strings.ToLower(filepath.Ext(f.SourcePath))))
	}
	// Never consume a pre-existing destination, including identical duplicates.
	target, collision, err := serviceAvailableTarget(f.TargetPath, nil)
	if err != nil {
		return err
	}
	if collision {
		f.Warning = joinServiceWarning(f.Warning, "Destination name was occupied; saved with a suffix.")
	}
	f.TargetPath = target
	if parent, err = s.allowedPath(filepath.Dir(target), false); err != nil || parent != filepath.Dir(target) {
		return errors.New("destination directory was replaced by a symbolic link")
	}
	if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	// Revalidate after mkdir: all destination components must remain canonical.
	if parent, err = s.allowedPath(filepath.Dir(target), true); err != nil || parent != filepath.Dir(target) {
		return errors.New("destination directory changed")
	}
	if f.StagingPath != "" {
		if filepath.Dir(f.StagingPath) == filepath.Dir(target) && strings.HasPrefix(filepath.Base(f.StagingPath), ".archive-"+j.ID+"-") {
			_ = os.Remove(f.StagingPath)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".archive-"+j.ID+"-*.partial")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer tmp.Close()
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.Remove(tmpPath)
		}
	}()
	if err = tmp.Chmod(0644); err != nil {
		return err
	}
	f.Status = "copying"
	f.SHA256 = ""
	f.StagingPath = tmpPath
	f.Published = false
	if err = s.saveTransfer(j, shared, f); err != nil {
		return err
	}
	in, err := os.Open(f.SourcePath)
	if err != nil {
		return err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) {
		return errors.New("source was replaced before copy")
	}
	h := sha256.New()
	buf := make([]byte, 1024*1024)
	var written int64
	lastUpdate := time.Time{}
	for {
		n, readErr := in.Read(buf)
		if n > 0 {
			if _, err = tmp.Write(buf[:n]); err != nil {
				return err
			}
			h.Write(buf[:n])
			written += int64(n)
			if time.Since(lastUpdate) >= 150*time.Millisecond {
				s.transferProgress(j, written)
				lastUpdate = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if written != f.Size {
		return errors.New("source size changed during copy")
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	sourceHash := hex.EncodeToString(h.Sum(nil))
	destHash, err := serviceSHA256(tmpPath)
	if err != nil {
		return err
	}
	if destHash != sourceHash {
		return errors.New("SHA-256 verification failed; source retained")
	}
	f.SHA256 = sourceHash
	if err = s.saveTransfer(j, shared, f); err != nil {
		return err
	}
	// Link is atomic and fails if target exists; unlike rename it never overwrites.
	if err = os.Link(tmpPath, target); err != nil {
		return fmt.Errorf("cannot publish destination without overwrite: %w", err)
	}
	keepStage = true
	if err = syncServiceDirectory(filepath.Dir(target)); err != nil {
		return err
	}
	f.Published = true
	if err = s.saveTransfer(j, shared, f); err != nil {
		return err
	}
	return s.finishPublished(j, shared, f)
}
func (s *archiveService) finishPublished(j *ServiceJob, shared, f *ServiceFile) error {
	expected := f.SHA256
	if expected == "" {
		return errors.New("missing verification journal; source retained")
	}
	target, err := s.allowedPath(f.TargetPath, false)
	if err != nil || target != f.TargetPath {
		return errors.New("published destination path changed")
	}
	hash, err := serviceSHA256(f.TargetPath)
	if err != nil {
		return err
	}
	if hash != expected {
		return errors.New("destination checksum changed; source retained")
	}
	info, err := os.Lstat(f.SourcePath)
	if err == nil {
		if !info.Mode().IsRegular() || info.Size() != f.Size {
			return errors.New("source changed before deletion; retained")
		}
		hash, err = serviceSHA256(f.SourcePath)
		if err != nil {
			return err
		}
		if hash != expected {
			return errors.New("source changed during archive; retained")
		}
		current, e := os.Lstat(f.SourcePath)
		if e != nil {
			return e
		}
		if !os.SameFile(info, current) || !info.ModTime().Equal(current.ModTime()) {
			return errors.New("source was replaced before deletion; retained")
		}
		if err = os.Remove(f.SourcePath); err != nil {
			return err
		}
		if err = syncServiceDirectory(filepath.Dir(f.SourcePath)); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if f.StagingPath != "" {
		if err = os.Remove(f.StagingPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	f.StagingPath = ""
	f.Status = "completed"
	f.Error = ""
	return s.saveTransfer(j, shared, f)
}
