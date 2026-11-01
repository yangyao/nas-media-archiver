package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yangyao/nas-media-archiver/internal/batchsplit"
)

type SplitJob struct {
	ID             string          `json:"id"`
	Status         string          `json:"status"`
	CreatedAt      time.Time       `json:"createdAt"`
	CompletedFiles int             `json:"completedFiles"`
	CompletedBytes int64           `json:"completedBytes"`
	Error          string          `json:"error"`
	Plan           batchsplit.Plan `json:"plan"`
}

func splitSummary(j *SplitJob) any {
	return map[string]any{"id": j.ID, "status": j.Status, "createdAt": j.CreatedAt, "completedFiles": j.CompletedFiles, "completedBytes": j.CompletedBytes, "error": j.Error, "sourcePath": j.Plan.Source, "prefix": j.Plan.Prefix, "maxBytes": j.Plan.Limit, "totalFiles": j.Plan.TotalFiles, "totalBytes": j.Plan.TotalBytes, "retainedEntries": j.Plan.RetainedEntries, "batches": j.Plan.Batches}
}
func (s *archiveService) loadSplitJobs() error {
	s.splits = map[string]*SplitJob{}
	dir := filepath.Join(s.config.StateDir, "splits")
	entries, e := os.ReadDir(dir)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		b, e := os.ReadFile(filepath.Join(dir, entry.Name()))
		if e != nil {
			return e
		}
		var j SplitJob
		if e = json.Unmarshal(b, &j); e != nil {
			return e
		}
		if entry.Name() != j.ID+".json" {
			return errors.New("invalid split state")
		}
		if j.Status == "running" {
			j.Status = "interrupted"
			j.Error = "服务已重启，可继续拆分。"
		}
		s.splits[j.ID] = &j
	}
	return nil
}

// Only explicit split state transitions write JSON; per-file progress stays in memory.
// The durable plan lets recovery infer completed moves from original inode identity.
func (s *archiveService) saveSplitLocked(j *SplitJob) error {
	dir := filepath.Join(s.config.StateDir, "splits")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	b, e := json.Marshal(j)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".split-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e == nil {
		e = os.Rename(f.Name(), filepath.Join(dir, j.ID+".json"))
	}
	if e != nil {
		return e
	}
	d, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (s *archiveService) validateSplitPaths(p batchsplit.Plan) error {
	settings := s.settingsSnapshot()
	paths := []string{p.Source}
	for _, b := range p.Batches {
		paths = append(paths, b.Path)
	}
	for _, path := range paths {
		resolved, e := s.allowedPath(path, path == p.Source)
		if e != nil || resolved != path {
			return errors.New("目录不在允许访问的范围内，或路径已改变")
		}
		sourceAllowed := false
		for _, root := range settings.SourceRoots {
			if pathWithin(path, root) {
				sourceAllowed = true
			}
		}
		if !sourceAllowed || pathsOverlap(path, s.config.StateDir) || (settings.ArchiveBase != "" && pathsOverlap(path, settings.ArchiveBase)) {
			return errors.New("请选择来源范围内的目录，不能拆分归档或服务数据目录")
		}
	}
	for _, b := range p.Batches {
		if e := s.checkClaims([]string{p.Source}, b.Path, ""); e != nil {
			return e
		}
	}
	return nil
}
func (s *archiveService) splitEndpoint(w http.ResponseWriter, r *http.Request) {
	endpoint := strings.TrimPrefix(r.URL.Path, "/api/v1/tools/splits")
	if endpoint == "/preview" && r.Method == "POST" {
		var v struct {
			SourcePath string `json:"sourcePath"`
			Prefix     string `json:"prefix"`
			MaxBytes   int64  `json:"maxBytes"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		d.DisallowUnknownFields()
		if e := d.Decode(&v); e != nil {
			serviceError(w, 400, "invalid_request", "请提供来源目录和批次名称。")
			return
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			serviceError(w, 400, "invalid_request", "请只提交一份请求。")
			return
		}
		if v.MaxBytes == 0 {
			v.MaxBytes = batchsplit.DefaultLimit
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closing {
			serviceError(w, 409, "busy", "服务正在关闭。")
			return
		}
		source, e := s.allowedPath(v.SourcePath, true)
		if e != nil {
			serviceError(w, 403, "forbidden", e.Error())
			return
		}
		// Validate before walking so arbitrary archive/state trees cannot be scanned.
		if e = s.validateSplitPaths(batchsplit.Plan{Source: source}); e != nil {
			serviceError(w, 400, "invalid_split", e.Error())
			return
		}
		p, e := batchsplit.New(source, v.Prefix, v.MaxBytes)
		if e != nil {
			serviceError(w, 400, "invalid_split", e.Error())
			return
		}
		if e = s.validateSplitPaths(p); e != nil {
			serviceError(w, 409, "busy", e.Error())
			return
		}
		// Bound abandoned read-only previews without discarding persisted operations.
		for id, j := range s.splits {
			if j.Status == "preview" && time.Since(j.CreatedAt) > time.Hour {
				delete(s.splits, id)
			}
		}
		previews := 0
		for _, j := range s.splits {
			if j.Status == "preview" {
				previews++
			}
		}
		if previews >= 32 {
			serviceError(w, 429, "busy", "预览较多，请稍后重试。")
			return
		}
		var b [8]byte
		if _, e = rand.Read(b[:]); e != nil {
			serviceError(w, 500, "error", e.Error())
			return
		}
		j := &SplitJob{ID: "split-" + hex.EncodeToString(b[:]), Status: "preview", CreatedAt: time.Now(), Plan: p}
		s.splits[j.ID] = j
		writeServiceJSON(w, 200, splitSummary(j))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if endpoint == "" && r.Method == "GET" {
		jobs := []*SplitJob{}
		for _, j := range s.splits {
			if j.Status != "preview" {
				jobs = append(jobs, j)
			}
		}
		sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
		result := []any{}
		for _, j := range jobs {
			result = append(result, splitSummary(j))
		}
		writeServiceJSON(w, 200, result)
		return
	}
	parts := strings.Split(strings.TrimPrefix(endpoint, "/"), "/")
	if len(parts) < 1 || len(parts) > 2 {
		serviceError(w, 404, "not_found", "找不到拆分任务。")
		return
	}
	j := s.splits[parts[0]]
	if j == nil {
		serviceError(w, 404, "not_found", "拆分预览已失效，请重新预览。")
		return
	}
	if len(parts) == 1 && r.Method == "GET" {
		writeServiceJSON(w, 200, splitSummary(j))
		return
	}
	if len(parts) != 2 || parts[1] != "start" || r.Method != "POST" {
		serviceError(w, 405, "method_not_allowed", "Use GET or POST start.")
		return
	}
	if s.closing || j.Status == "running" || j.Status == "completed" || j.Plan.TotalFiles == 0 {
		serviceError(w, 409, "invalid_state", "当前任务不能执行。")
		return
	}
	// Ignore this task's own reservation while validating all other claims.
	ownStatus := j.Status
	j.Status = "preview"
	e := s.validateSplitPaths(j.Plan)
	j.Status = ownStatus
	if e != nil {
		serviceError(w, 409, "busy", e.Error())
		return
	}
	if j.Status == "preview" {
		if e = j.Plan.Reserve(); e != nil {
			serviceError(w, 409, "changed", e.Error())
			return
		}
	}
	j.Status = "running"
	j.Error = ""
	if e = s.saveSplitLocked(j); e != nil {
		j.Status = "failed"
		j.Error = "无法保存拆分计划，文件尚未移动。"
		serviceError(w, 500, "save_failed", j.Error)
		return
	}
	s.wg.Add(1)
	go s.runSplit(j)
	writeServiceJSON(w, 202, splitSummary(j))
}
func (s *archiveService) runSplit(j *SplitJob) {
	defer s.wg.Done()
	e := j.Plan.Execute(s.stopped, func(n int, b int64) { s.mu.Lock(); j.CompletedFiles = n; j.CompletedBytes = b; s.mu.Unlock() })
	s.mu.Lock()
	defer s.mu.Unlock()
	j.Status = "completed"
	j.Error = ""
	if e != nil {
		j.Status = "failed"
		if s.closing {
			j.Status = "interrupted"
		}
		j.Error = e.Error()
	}
	if e = s.saveSplitLocked(j); e != nil {
		j.Status = "interrupted"
		j.Error = "无法保存完成状态，可继续执行以核对结果。"
	}
}
