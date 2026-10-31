package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type archiveService struct {
	mu              sync.Mutex
	config          ServiceConfig
	store           *serviceStore
	token           string
	jobs            map[string]*ServiceJob
	wake            chan struct{}
	stop            chan struct{}
	closing         bool
	wg              sync.WaitGroup
	closeOnce       sync.Once
	closeErr        error
	metadataReaders chan *serviceMetadataReader
}

func newArchiveService(config ServiceConfig) (*archiveService, error) {
	if config.Name == "" {
		config.Name = "NAS Archiver"
	}
	var err error
	config.StateDir, err = filepath.Abs(config.StateDir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(config.StateDir, 0700); err != nil {
		return nil, err
	}
	config.StateDir, err = canonicalServicePath(config.StateDir)
	if err != nil {
		return nil, err
	}
	roots := []string{}
	for _, r := range config.Roots {
		if strings.TrimSpace(r) == "" {
			continue
		}
		v, e := canonicalServicePath(strings.TrimSpace(r))
		if e != nil {
			return nil, e
		}
		if info, e := os.Stat(v); e != nil || !info.IsDir() {
			return nil, fmt.Errorf("configured root is not a directory: %s", r)
		}
		roots = append(roots, v)
	}
	if len(roots) == 0 {
		return nil, errors.New("at least one allowed root is required")
	}
	config.Roots = roots
	if config.TokenFile == "" {
		config.TokenFile = filepath.Join(config.StateDir, "token")
	}
	token, err := loadServiceToken(config.TokenFile)
	if err != nil {
		return nil, err
	}
	store, err := openServiceStore(config.StateDir)
	if err != nil {
		return nil, err
	}
	jobs, err := store.load()
	if err != nil {
		store.Close()
		return nil, err
	}
	readers := make(chan *serviceMetadataReader, serviceMetadataWorkers)
	for i := 0; i < serviceMetadataWorkers; i++ {
		readers <- &serviceMetadataReader{}
	}
	s := &archiveService{metadataReaders: readers, config: config, store: store, token: token, jobs: jobs, wake: make(chan struct{}, 1), stop: make(chan struct{})}
	archive, err := s.allowedPath(config.ArchiveBase, false)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("archive base: %w", err)
	}
	s.config.ArchiveBase = archive
	for _, j := range jobs {
		if j.CompletedFiles > 0 || j.SkippedFiles > 0 || j.Status == "running" || j.Status == "pausing" || j.Status == "paused" || j.Status == "queued" || j.Status == "completed" || j.Status == "completed_with_errors" {
			j.Started = true
		}
		for _, f := range j.Files {
			if f.SHA256 != "" || f.StagingPath != "" || f.Published || f.Status == "copying" {
				j.Started = true
				break
			}
		}
		if len(j.Files) != j.TotalFiles {
			j.ScanComplete = false
			j.Status = "interrupted"
			j.Error = "Checkpoint is incomplete; retry to rebuild the preview."
		}
		if j.Status == "scanning" || j.Status == "running" || j.Status == "pausing" || j.Status == "queued" {
			j.Status = "interrupted"
			j.Error = "Service restarted. Review and resume the task."
		}
		j.CurrentFile = ""
		j.CurrentFileBytes = 0
		j.recalculate()
		j.UpdatedAt = time.Now()
		if err = store.save(j, nil); err != nil {
			store.Close()
			return nil, err
		}
	}
	s.wg.Add(1)
	go s.worker()
	return s, nil
}
func loadServiceToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return "", err
		}
		b := make([]byte, 32)
		if _, err = rand.Read(b); err != nil {
			return "", err
		}
		data = []byte(hex.EncodeToString(b) + "\n")
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return "", e
		}
		if _, e = f.Write(data); e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return "", e
		}
		if ce != nil {
			return "", ce
		}
	} else if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if len(token) < 24 {
		return "", errors.New("token must contain at least 24 characters")
	}
	if err = os.Chmod(path, 0600); err != nil {
		return "", err
	}
	return token, nil
}
func (s *archiveService) changed(j *ServiceJob, f *ServiceFile) error {
	j.UpdatedAt = time.Now()
	if f != nil {
		j.updateFile(f)
	}
	return s.store.save(j, f)
}
func serviceJobSnapshot(j *ServiceJob) ServiceJob {
	v := *j
	v.Files = nil
	v.counted = nil
	v.scanCache = nil
	v.SourcePaths = append([]string{}, j.SourcePaths...)
	v.ResidualPaths = append([]string{}, j.ResidualPaths...)
	return v
}
func (s *archiveService) listJobs() []ServiceJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []ServiceJob{}
	for _, j := range s.jobs {
		out = append(out, serviceJobSnapshot(j))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}
func (s *archiveService) createJob(sources []string, base string, cleanup bool) (ServiceJob, error) {
	return s.createJobWithOptions(sources, base, cleanup, false)
}
func (s *archiveService) createJobWithOptions(sources []string, base string, cleanup, skipDuplicates bool) (ServiceJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return ServiceJob{}, errors.New("service is shutting down")
	}
	if base == "" {
		base = s.config.ArchiveBase
	}
	paths, target, err := s.validateJobPaths(sources, base)
	if err != nil {
		return ServiceJob{}, err
	}
	if err = s.checkClaims(paths, target, ""); err != nil {
		return ServiceJob{}, err
	}
	b := make([]byte, 8)
	if _, err = rand.Read(b); err != nil {
		return ServiceJob{}, err
	}
	now := time.Now()
	j := &ServiceJob{ID: "job-" + hex.EncodeToString(b), Name: filepath.Base(paths[0]), SourcePaths: paths, ArchiveBase: target, CleanupEmptyDirectories: cleanup, SkipDuplicates: skipDuplicates, Status: "scanning", CreatedAt: now, UpdatedAt: now, CleanupStatus: "not_requested", ResidualPaths: []string{}, Files: []*ServiceFile{}}
	if len(paths) > 1 {
		j.Name = fmt.Sprintf("%s + %d", j.Name, len(paths)-1)
	}
	if cleanup {
		j.CleanupStatus = "pending"
	}
	if err = s.store.save(j, nil); err != nil {
		return ServiceJob{}, err
	}
	s.jobs[j.ID] = j
	s.wg.Add(1)
	go s.scanJob(j)
	return serviceJobSnapshot(j), nil
}
func (s *archiveService) stopped() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.closing }
func (s *archiveService) scanJob(j *ServiceJob) {
	defer s.wg.Done()
	reserved := map[string]bool{}
	var scanErr error
	// Enumerate cheaply first; metadata is the expensive, bounded parallel stage.
	var files []*ServiceFile
	for _, source := range j.SourcePaths {
		err := filepath.WalkDir(source, func(path string, d fs.DirEntry, walkErr error) error {
			if s.stopped() {
				return errors.New("scan interrupted by service shutdown")
			}
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if path != source && serviceExcluded(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			f := &ServiceFile{ID: fmt.Sprintf("file-%06d", len(files)+1), SourcePath: path, Size: info.Size(), MTime: info.ModTime(), Status: "discovered"}
			if !info.Mode().IsRegular() || d.Type()&os.ModeSymlink != 0 {
				f.Status = "unsupported"
				f.Warning = "Symbolic links and special files are left at source."
			} else if classifyArchiveType(path) == "misc" {
				f.Status = "unsupported"
				f.Warning = "Unsupported file type; left at source."
			}
			files = append(files, f)
			s.mu.Lock()
			initial := *f
			j.Files = append(j.Files, &initial)
			err = s.changed(j, &initial)
			s.mu.Unlock()
			return err
		})
		if err != nil {
			scanErr = err
			break
		}
	}
	type result struct {
		index int
		file  *ServiceFile
		date  time.Time
	}
	tasks := make(chan int)
	results := make(chan result, serviceMetadataWorkers)
	var workers sync.WaitGroup
	for n := 0; n < serviceMetadataWorkers; n++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			var reader *serviceMetadataReader
			select {
			case reader = <-s.metadataReaders:
			case <-s.stop:
				return
			}
			defer func() { s.metadataReaders <- reader }()
			for i := range tasks {
				f := files[i]
				var dt time.Time
				if f.Status != "unsupported" {
					cached, ok := j.scanCache[f.SourcePath]
					if ok && cached.Status == "planned" && cached.Size == f.Size && cached.MTime.Equal(f.MTime) {
						dt, _ = time.Parse(time.RFC3339, cached.DateTime)
						f.MetadataSource = cached.MetadataSource
					}
					if dt.IsZero() {
						var err error
						dt, f.MetadataSource, err = reader.detect(f.SourcePath, f.MTime)
						if err != nil {
							f.Status = "failed"
							f.Error = err.Error()
						}
					}
					if f.Status != "failed" {
						f.Status = "planned"
						f.DateTime = dt.Format(time.RFC3339)
						if f.MetadataSource == "mtime" {
							f.Warning = "No capture date found; using original modification time."
						}
					}
				}
				results <- result{i, f, dt}
			}
		}()
	}
	go func() {
		defer close(tasks)
		for i := range files {
			select {
			case tasks <- i:
			case <-s.stop:
				return
			}
		}
	}()
	go func() { workers.Wait(); close(results) }()
	// Apply in discovery order so collision suffixes remain deterministic even
	// when metadata requests finish out of order.
	pending := map[int]result{}
	next := 0
	for r := range results {
		pending[r.index] = r
		for {
			current, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			f, dt := current.file, current.date
			if f.Status == "planned" {
				base := filepath.Join(j.ArchiveBase, classifyArchiveType(f.SourcePath), dt.Format("2006"), dt.Format("01"), fmt.Sprintf("%s_%d%s", dt.Format("20060102_150405"), f.Size, strings.ToLower(filepath.Ext(f.SourcePath))))
				target, collision, err := serviceAvailableTarget(base, reserved)
				if err != nil {
					f.Status = "failed"
					f.Error = err.Error()
				} else {
					f.TargetPath = target
					reserved[target] = true
					if collision {
						f.Warning = joinServiceWarning(f.Warning, "A duplicate name will be saved with a suffix.")
					}
				}
			}
			s.mu.Lock()
			j.Files[next] = f
			j.CurrentFile = f.SourcePath
			err := s.changed(j, f)
			s.mu.Unlock()
			if err != nil && scanErr == nil {
				scanErr = err
			}
			next++
		}
	}
	if s.stopped() && scanErr == nil {
		scanErr = errors.New("scan interrupted by service shutdown")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	j.CurrentFile = ""
	j.scanCache = nil
	if scanErr != nil {
		j.Status = "failed"
		if s.closing {
			j.Status = "interrupted"
		}
		j.Error = scanErr.Error()
	} else {
		j.ScanComplete = true
		j.Status = "ready"
	}
	if err := s.changed(j, nil); err != nil {
		j.Status = "failed"
		j.Error = "Cannot persist scan: " + err.Error()
	}
}

// Rebuild the preview from the current directory tree, reusing only metadata
// for unchanged source files. No scan result remains implicitly ready.
func (s *archiveService) restartScanLocked(j *ServiceJob) (ServiceJob, error) {
	j.scanCache = make(map[string]ServiceFile, len(j.Files))
	for _, f := range j.Files {
		j.scanCache[f.SourcePath] = *f
	}
	s.store.resetFiles(j.ID)
	j.Files = []*ServiceFile{}
	j.recalculate()
	j.ScanComplete = false
	j.Status = "scanning"
	j.Error = ""
	j.CurrentFile = ""
	j.CurrentFileBytes = 0
	if err := s.changed(j, nil); err != nil {
		return ServiceJob{}, err
	}
	s.wg.Add(1)
	go s.scanJob(j)
	return serviceJobSnapshot(j), nil
}

func joinServiceWarning(a, b string) string {
	if a == "" {
		return b
	}
	return a + " " + b
}
func serviceAvailableTarget(base string, reserved map[string]bool) (string, bool, error) {
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 0; i < 1000000; i++ {
		p := base
		if i > 0 {
			p = fmt.Sprintf("%s_%d%s", stem, i, ext)
		}
		if reserved[p] {
			continue
		}
		_, err := os.Lstat(p)
		if os.IsNotExist(err) {
			return p, i > 0, nil
		}
		if err != nil {
			return "", false, err
		}
	}
	return "", false, errors.New("too many target name collisions")
}

func (s *archiveService) checkClaims(paths []string, target, ignore string) error {
	for id, other := range s.jobs {
		if id == ignore || serviceTerminal(other.Status) {
			continue
		}
		for _, a := range paths {
			if pathsOverlap(a, other.ArchiveBase) {
				return errors.New("source overlaps another task's archive destination")
			}
			for _, b := range other.SourcePaths {
				if pathsOverlap(a, b) {
					return errors.New("source directory is already claimed by another task")
				}
			}
		}
		for _, b := range other.SourcePaths {
			if pathsOverlap(target, b) {
				return errors.New("archive destination overlaps another task's source")
			}
		}
	}
	return nil
}
func (s *archiveService) validateExistingClaims(j *ServiceJob, requireSources bool) error {
	for _, p := range j.SourcePaths {
		resolved, err := s.allowedPath(p, requireSources)
		if err != nil {
			return err
		}
		if resolved != p {
			return errors.New("source directory changed")
		}
		if pathsOverlap(p, j.ArchiveBase) {
			return errors.New("source and archive overlap")
		}
	}
	resolved, err := s.allowedPath(j.ArchiveBase, false)
	if err != nil {
		return err
	}
	if resolved != j.ArchiveBase {
		return errors.New("archive directory changed")
	}
	return s.checkClaims(j.SourcePaths, j.ArchiveBase, j.ID)
}

func (s *archiveService) jobAction(id, action string) (ServiceJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[id]
	if j == nil {
		return ServiceJob{}, os.ErrNotExist
	}
	if s.closing {
		return ServiceJob{}, errors.New("service is shutting down")
	}
	switch action {
	case "start":
		if j.Status != "ready" && j.Status != "paused" && j.Status != "interrupted" {
			return ServiceJob{}, errors.New("task cannot be started in its current state")
		}
		if !j.ScanComplete {
			return ServiceJob{}, errors.New("scan was interrupted; retry this task to rebuild the preview")
		}
		if err := s.validateExistingClaims(j, false); err != nil {
			return ServiceJob{}, err
		}
		if j.TotalFiles-j.UnsupportedFiles-j.CompletedFiles-j.SkippedFiles-j.FailedFiles <= 0 {
			return ServiceJob{}, errors.New("no files are ready to archive; rescan the directory after adding files")
		}
		j.Started = true
		j.Status = "queued"
		j.Error = ""
	case "pause":
		if j.Status == "queued" {
			j.Status = "paused"
		} else if j.Status == "running" {
			j.Status = "pausing"
		} else if j.Status != "pausing" {
			return ServiceJob{}, errors.New("task is not running or queued")
		}
	case "rescan":
		if j.Status != "ready" || j.Started {
			return ServiceJob{}, errors.New("rescan is available before archiving starts; create a new task for later batches")
		}
		if err := s.validateExistingClaims(j, true); err != nil {
			return ServiceJob{}, err
		}
		return s.restartScanLocked(j)
	case "retry":
		if !serviceTerminal(j.Status) && j.Status != "interrupted" && j.Status != "paused" {
			return ServiceJob{}, errors.New("task is still active")
		}
		if err := s.validateExistingClaims(j, !j.ScanComplete); err != nil {
			return ServiceJob{}, err
		}
		if !j.ScanComplete {
			return s.restartScanLocked(j)
		}
		if err := s.replanFailedLocked(j); err != nil {
			return ServiceJob{}, err
		}
		j.Status = "ready"
		j.Error = ""
	case "cleanup":
		if !serviceTerminal(j.Status) {
			return ServiceJob{}, errors.New("cleanup requires a finished task")
		}
		s.cleanupLocked(j)
	default:
		return ServiceJob{}, errors.New("unknown action")
	}
	if err := s.changed(j, nil); err != nil {
		return ServiceJob{}, err
	}
	if action == "start" {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return serviceJobSnapshot(j), nil
}
func (s *archiveService) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stop:
			return
		case <-s.wake:
		}
		for {
			s.mu.Lock()
			var next *ServiceJob
			if !s.closing {
				for _, j := range s.jobs {
					if j.Status == "queued" && (next == nil || j.UpdatedAt.Before(next.UpdatedAt)) {
						next = j
					}
				}
			}
			if next == nil {
				s.mu.Unlock()
				break
			}
			next.Status = "running"
			if err := s.changed(next, nil); err != nil {
				next.Status = "failed"
				next.Error = err.Error()
				s.mu.Unlock()
				continue
			}
			s.mu.Unlock()
			s.runJob(next)
		}
	}
}
func (s *archiveService) runJob(j *ServiceJob) {
	duplicates := newServiceDuplicateIndex()
	for _, f := range j.Files {
		s.mu.Lock()
		if s.closing || j.Status == "pausing" {
			j.Status = "paused"
			j.CurrentFile = ""
			j.CurrentFileBytes = 0
			_ = s.changed(j, nil)
			s.mu.Unlock()
			return
		}
		skip := f.Status == "completed" || f.Status == "skipped" || f.Status == "unsupported" || f.Status == "failed"
		if !skip {
			j.CurrentFile = f.SourcePath
			j.CurrentFileBytes = 0
			_ = s.changed(j, nil)
		}
		s.mu.Unlock()
		if skip {
			continue
		}
		var err error
		skipped := false
		if j.SkipDuplicates {
			skipped, err = s.skipDuplicate(j, f, duplicates)
		}
		if err == nil && !skipped {
			err = s.archiveFile(j, f)
		}
		if err == nil && f.Status == "completed" {
			duplicates.add(f.TargetPath)
		}
		s.mu.Lock()
		if err != nil {
			f.Status = "failed"
			f.Error = err.Error()
		}
		j.CurrentFile = ""
		j.CurrentFileBytes = 0
		if e := s.changed(j, f); e != nil {
			j.Status = "failed"
			j.Error = "Cannot persist task: " + e.Error()
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j.recalculate()
	j.Status = "completed"
	if j.FailedFiles > 0 {
		j.Status = "completed_with_errors"
	}
	if j.CleanupEmptyDirectories {
		s.cleanupLocked(j)
	}
	if err := s.changed(j, nil); err != nil {
		j.Status = "failed"
		j.Error = err.Error()
	}
}
func (s *archiveService) cleanupLocked(j *ServiceJob) {
	residual := []string{}
	for _, root := range j.SourcePaths {
		canonical, err := s.allowedPath(root, false)
		if err != nil || canonical != root {
			residual = append(residual, root)
			continue
		}
		dirs := []string{}
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
			if os.IsNotExist(e) {
				return nil
			}
			if e != nil {
				return e
			}
			if d.IsDir() {
				dirs = append(dirs, p)
			}
			return nil
		})
		if err != nil {
			residual = append(residual, root)
			continue
		}
		sort.Slice(dirs, func(i, k int) bool { return len(dirs[i]) > len(dirs[k]) })
		for _, d := range dirs {
			_ = syscall.Rmdir(d)
		} // Rmdir cannot remove a file if a directory was replaced.
		if _, err = os.Lstat(root); !os.IsNotExist(err) {
			residual = append(residual, root)
		}
	}
	j.ResidualPaths = residual
	j.CleanupStatus = "done"
	if len(residual) > 0 {
		j.CleanupStatus = "residual"
	}
}
func (s *archiveService) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		for _, j := range s.jobs {
			if j.Status == "queued" {
				j.Status = "paused"
				_ = s.changed(j, nil)
			}
		}
		s.mu.Unlock()
		close(s.stop)
		s.wg.Wait()
		for i := 0; i < serviceMetadataWorkers; i++ {
			reader := <-s.metadataReaders
			reader.Close()
		}
		s.closeErr = s.store.Close()
	})
	return s.closeErr
}

// Explicit retry creates a new preview for files that never reached a durable
// verified journal. Published files retain their original hash and recovery state.
func (s *archiveService) replanFailedLocked(j *ServiceJob) error {
	reserved := map[string]bool{}
	for _, f := range j.Files {
		if f.Status != "failed" && f.TargetPath != "" {
			reserved[f.TargetPath] = true
		}
	}
	for _, f := range j.Files {
		if f.Status != "failed" {
			continue
		}
		if f.SHA256 != "" {
			f.Status = "copying"
			f.Error = ""
		} else {
			err := func() error {
				p, err := s.allowedPath(f.SourcePath, false)
				if err != nil {
					return err
				}
				if p != f.SourcePath {
					return errors.New("source path changed")
				}
				info, err := os.Lstat(p)
				if err != nil {
					return err
				}
				if !info.Mode().IsRegular() {
					return errors.New("source is not a regular file")
				}
				var dt time.Time
				var metadata string
				if f.MetadataSource == "manual" && info.Size() == f.Size && info.ModTime().Equal(f.MTime) {
					dt, err = time.Parse(time.RFC3339, f.DateTime)
					metadata = "manual"
				} else {
					dt, metadata, err = detectDateTime(p, info.ModTime())
				}
				if err != nil {
					return err
				}
				base := filepath.Join(j.ArchiveBase, classifyArchiveType(p), dt.Format("2006"), dt.Format("01"), fmt.Sprintf("%s_%d%s", dt.Format("20060102_150405"), info.Size(), strings.ToLower(filepath.Ext(p))))
				target, collision, err := serviceAvailableTarget(base, reserved)
				if err != nil {
					return err
				}
				f.Size = info.Size()
				f.MTime = info.ModTime()
				f.DateTime = dt.Format(time.RFC3339)
				f.MetadataSource = metadata
				f.TargetPath = target
				f.Status = "planned"
				f.Error = ""
				f.Warning = ""
				f.Published = false
				if metadata == "mtime" {
					f.Warning = "No capture date found; using original modification time."
				}
				if collision {
					f.Warning = joinServiceWarning(f.Warning, "A duplicate name will be saved with a suffix.")
				}
				reserved[target] = true
				return nil
			}()
			if err != nil {
				f.Error = err.Error()
			}
		}
		if err := s.changed(j, f); err != nil {
			return err
		}
	}
	return nil
}
