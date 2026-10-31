package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Read-only inspection also exposes Syncthing/QNAP housekeeping directories.
// Mutating endpoints continue to reject these directories.
func (s *archiveService) inspectionDirectory(path string) (string, error) {
	resolved, err := canonicalServicePath(path)
	if err != nil {
		return "", err
	}
	allowed := false
	for _, root := range s.config.Roots {
		if pathWithin(resolved, root) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", errors.New("path is outside configured roots")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	return resolved, nil
}
func (s *archiveService) inspectDirectory(w http.ResponseWriter, r *http.Request) {
	path, err := s.inspectionDirectory(r.URL.Query().Get("path"))
	if err != nil {
		serviceError(w, 403, "forbidden", err.Error())
		return
	}
	list, err := os.ReadDir(path)
	if err != nil {
		serviceError(w, 403, "unreadable", err.Error())
		return
	}
	type entry struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		IsDirectory bool   `json:"isDirectory"`
		Size        int64  `json:"size"`
		System      bool   `json:"system"`
		CanOpen     bool   `json:"canOpen"`
		Symlink     bool   `json:"symlink"`
	}
	entries := []entry{}
	for _, d := range list {
		p := filepath.Join(path, d.Name())
		info, e := d.Info()
		if e != nil {
			continue
		}
		symlink := d.Type()&os.ModeSymlink != 0
		directory := d.IsDir()
		canOpen := directory && !symlink
		entries = append(entries, entry{d.Name(), p, directory, info.Size(), serviceExcluded(d.Name()), canOpen, symlink})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDirectory != entries[j].IsDirectory {
			return entries[i].IsDirectory
		}
		return entries[i].Name < entries[j].Name
	})
	var parent *string
	candidate := filepath.Dir(path)
	if candidate != path {
		if p, e := s.inspectionDirectory(candidate); e == nil {
			parent = &p
		}
	}
	writeServiceJSON(w, 200, map[string]interface{}{"path": path, "parentPath": parent, "entries": entries})
}
func (s *archiveService) reviewFile(jobID, fileID, action, date string) (ServiceJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[jobID]
	if j == nil {
		return ServiceJob{}, os.ErrNotExist
	}
	if s.closing {
		return ServiceJob{}, errors.New("service is shutting down")
	}
	if j.Status != "ready" && j.Status != "paused" && j.Status != "interrupted" && !serviceTerminal(j.Status) {
		return ServiceJob{}, errors.New("review files after pausing or finishing the task")
	}
	var f *ServiceFile
	for _, v := range j.Files {
		if v.ID == fileID {
			f = v
			break
		}
	}
	if f == nil {
		return ServiceJob{}, os.ErrNotExist
	}
	switch action {
	case "acknowledge":
		if f.Status == "unsupported" || f.Status == "failed" || f.Error != "" {
			return ServiceJob{}, errors.New("this file needs handling rather than acknowledgement")
		}
		f.Warning = ""
	case "date":
		if !j.ScanComplete || serviceTerminal(j.Status) || (f.Status != "planned" && f.Status != "failed") || f.SHA256 != "" {
			return ServiceJob{}, errors.New("capture date can only be changed before this file is archived")
		}
		dt, err := time.Parse(time.RFC3339, date)
		if err != nil {
			return ServiceJob{}, errors.New("provide an RFC3339 capture date")
		}
		source, err := s.allowedPath(f.SourcePath, false)
		if err != nil || source != f.SourcePath {
			return ServiceJob{}, errors.New("source path changed")
		}
		info, err := os.Lstat(source)
		if err != nil {
			return ServiceJob{}, err
		}
		if !info.Mode().IsRegular() || info.Size() != f.Size || !info.ModTime().Equal(f.MTime) {
			return ServiceJob{}, errors.New("source changed since scan; rescan before editing its date")
		}
		if classifyArchiveType(source) == "misc" {
			return ServiceJob{}, errors.New("unsupported file type")
		}
		local := *f
		local.DateTime = dt.Format(time.RFC3339)
		local.MetadataSource = "manual"
		base, err := serviceDuplicateBase(j, &local)
		if err != nil {
			return ServiceJob{}, err
		}
		reserved := map[string]bool{}
		for _, other := range j.Files {
			if other.ID != f.ID && other.TargetPath != "" {
				reserved[other.TargetPath] = true
			}
		}
		target, collision, err := serviceAvailableTarget(base, reserved)
		if err != nil {
			return ServiceJob{}, err
		}
		local.TargetPath = target
		local.Status = "planned"
		local.Error = ""
		local.Warning = ""
		if collision {
			local.Warning = "A duplicate name will be saved with a suffix."
		}
		*f = local
	default:
		return ServiceJob{}, errors.New("unknown file review action")
	}
	if err := s.changed(j, f); err != nil {
		return ServiceJob{}, err
	}
	return serviceJobSnapshot(j), nil
}

func (s *archiveService) fileReviewEndpoint(w http.ResponseWriter, r *http.Request, parts []string) {
	if r.Method != "POST" {
		serviceError(w, 405, "method_not_allowed", "Use POST.")
		return
	}
	var date string
	if parts[3] == "date" {
		var input struct {
			DateTime string `json:"dateTime"`
		}
		if err := decodeServiceObject(w, r, &input); err != nil {
			serviceError(w, 400, "invalid_request", err.Error())
			return
		}
		date = input.DateTime
	}
	j, err := s.reviewFile(parts[0], parts[2], parts[3], date)
	if err != nil {
		status := 409
		if errors.Is(err, os.ErrNotExist) {
			status = 404
		}
		serviceError(w, status, "invalid_state", err.Error())
		return
	}
	writeServiceJSON(w, 200, j)
}

func isServiceReviewRoute(parts []string) bool {
	return len(parts) == 4 && parts[0] != "" && parts[1] == "files" && parts[2] != "" && (parts[3] == "date" || parts[3] == "acknowledge") && !strings.Contains(parts[2], "/")
}

func decodeServiceObject(w http.ResponseWriter, r *http.Request, value interface{}) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("provide one JSON object")
	}
	return nil
}
