package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func runServe(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := flags.String("listen", "127.0.0.1:8787", "HTTP listen address")
	state := flags.String("state-dir", ".archive-service", "persistent service data directory")
	archive := flags.String("archive-base", "", "default archive root")
	roots := flags.String("roots", "/share", "comma-separated allowed filesystem roots")
	tokenFile := flags.String("token-file", "", "bearer token file; generated with mode 0600 if absent")
	name := flags.String("name", "NAS Archiver", "display name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	s, err := newArchiveService(ServiceConfig{Listen: *listen, StateDir: *state, ArchiveBase: *archive, Roots: strings.Split(*roots, ","), TokenFile: *tokenFile, Name: *name})
	if err != nil {
		return err
	}
	defer s.Close()
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	failed := make(chan error, 1)
	go func() { failed <- server.Serve(listener) }()
	log.Printf("NAS Archiver %s listening on %s; token file: %s", serviceVersion, listener.Addr(), s.config.TokenFile)
	select {
	case err = <-failed:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		// Stop accepting mutations before waiting for the current file to finish.
		s.mu.Lock()
		s.closing = true
		s.mu.Unlock()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		_ = server.Close()
		return s.Close()
	}
}

func (s *archiveService) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/healthz" {
			if r.Method != "GET" {
				serviceError(w, 405, "method_not_allowed", "Use GET.")
				return
			}
			writeServiceJSON(w, 200, map[string]string{"status": "ok"})
			return
		}
		if r.URL.Path == "/" {
			if r.Method != "GET" {
				serviceError(w, 405, "method_not_allowed", "Use GET.")
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
			io.WriteString(w, serviceLandingPage)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			serviceError(w, 404, "not_found", "Endpoint not found.")
			return
		}
		expected := sha256.Sum256([]byte("Bearer " + s.token))
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			serviceError(w, 401, "unauthorized", "A valid NAS access token is required.")
			return
		}
		s.route(w, r)
	})
}
func writeServiceJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func serviceError(w http.ResponseWriter, status int, code, message string) {
	writeServiceJSON(w, status, map[string]interface{}{"error": map[string]string{"code": code, "message": message}})
}
func (s *archiveService) route(w http.ResponseWriter, r *http.Request) {
	endpoint := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	if endpoint == "tools/splits" || strings.HasPrefix(endpoint, "tools/splits/") {
		s.splitEndpoint(w, r)
		return
	}
	switch endpoint {
	case "settings":
		s.settingsEndpoint(w, r)
		return
	case "info":
		if r.Method != "GET" {
			serviceError(w, 405, "method_not_allowed", "Use GET.")
			return
		}
		s.info(w)
		return
	case "browse":
		if r.Method != "GET" {
			serviceError(w, 405, "method_not_allowed", "Use GET.")
			return
		}
		s.browse(w, r)
		return
	case "events":
		if r.Method != "GET" {
			serviceError(w, 405, "method_not_allowed", "Use GET.")
			return
		}
		s.events(w, r)
		return
	case "jobs":
		switch r.Method {
		case "GET":
			writeServiceJSON(w, 200, s.listJobs())
		case "POST":
			var input struct {
				SourcePaths             []string `json:"sourcePaths"`
				ArchiveBase             string   `json:"archiveBase"`
				CleanupEmptyDirectories bool     `json:"cleanupEmptyDirectories"`
				SkipDuplicates          bool     `json:"skipDuplicates"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				serviceError(w, 400, "invalid_request", "Invalid job request: "+err.Error())
				return
			}
			var extra interface{}
			if err := decoder.Decode(&extra); err != io.EOF {
				serviceError(w, 400, "invalid_request", "Provide one JSON object.")
				return
			}
			j, err := s.createJobWithOptions(input.SourcePaths, input.ArchiveBase, input.CleanupEmptyDirectories, input.SkipDuplicates)
			if err != nil {
				serviceError(w, 400, "invalid_request", err.Error())
				return
			}
			writeServiceJSON(w, 202, j)
		default:
			serviceError(w, 405, "method_not_allowed", "Use GET or POST.")
		}
		return
	}
	if !strings.HasPrefix(endpoint, "jobs/") {
		serviceError(w, 404, "not_found", "Endpoint not found.")
		return
	}
	parts := strings.Split(strings.TrimPrefix(endpoint, "jobs/"), "/")
	if isServiceReviewRoute(parts) {
		s.fileReviewEndpoint(w, r, parts)
		return
	}
	if len(parts) > 2 || parts[0] == "" {
		serviceError(w, 404, "not_found", "Endpoint not found.")
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		if r.Method != "GET" {
			serviceError(w, 405, "method_not_allowed", "Use GET.")
			return
		}
		s.mu.Lock()
		j := s.jobs[id]
		if j == nil {
			s.mu.Unlock()
			serviceError(w, 404, "not_found", "Task not found.")
			return
		}
		snapshot := serviceJobSnapshot(j)
		s.mu.Unlock()
		writeServiceJSON(w, 200, snapshot)
		return
	}
	if parts[1] == "files" {
		if r.Method != "GET" {
			serviceError(w, 405, "method_not_allowed", "Use GET.")
			return
		}
		s.files(w, r, id)
		return
	}
	if r.Method != "POST" {
		serviceError(w, 405, "method_not_allowed", "Use POST.")
		return
	}
	if parts[1] == "skip-duplicates" {
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.Enabled == nil {
			serviceError(w, 400, "invalid_request", "Provide enabled: true or false.")
			return
		}
		var extra interface{}
		if err := decoder.Decode(&extra); err != io.EOF {
			serviceError(w, 400, "invalid_request", "Provide one JSON object.")
			return
		}
		j, err := s.setSkipDuplicates(id, *input.Enabled)
		if err != nil {
			status := 409
			if errors.Is(err, os.ErrNotExist) {
				status = 404
			}
			serviceError(w, status, "invalid_state", err.Error())
			return
		}
		writeServiceJSON(w, 200, j)
		return
	}
	if parts[1] != "start" && parts[1] != "pause" && parts[1] != "retry" && parts[1] != "rescan" && parts[1] != "cleanup" {
		serviceError(w, 404, "not_found", "Endpoint not found.")
		return
	}
	j, err := s.jobAction(id, parts[1])
	if err != nil {
		status := 409
		code := "invalid_state"
		if errors.Is(err, os.ErrNotExist) {
			status = 404
			code = "not_found"
		}
		serviceError(w, status, code, err.Error())
		return
	}
	status := 200
	if parts[1] == "start" {
		status = 202
	}
	writeServiceJSON(w, status, j)
}
func (s *archiveService) info(w http.ResponseWriter) {
	tools := map[string]bool{}
	for _, name := range []string{"exiftool", "ffprobe", "ffmpeg"} {
		_, err := exec.LookPath(name)
		if err != nil {
			_, err = os.Stat("/opt/bin/" + name)
		}
		if err != nil {
			_, err = os.Stat("/usr/bin/" + name)
		}
		tools[name] = err == nil
	}
	settings := s.settingsSnapshot()
	path := settings.ArchiveBase
	if path == "" {
		path = s.config.StateDir
	}
	var stat syscall.Statfs_t
	for {
		if err := syscall.Statfs(path, &stat); err == nil {
			break
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	free := int64(stat.Bavail) * int64(stat.Bsize)
	writeServiceJSON(w, 200, map[string]interface{}{"name": s.config.Name, "version": serviceVersion, "capabilities": map[string]bool{"directorySplit": true}, "configured": settings.Configured, "archiveBase": settings.ArchiveBase, "roots": s.config.Roots, "freeBytes": free, "tools": tools, "persistence": s.store.status()})
}

// Source browsing is a view of the configured access roots, not a broader
// filesystem permission. Generic browsing/inspection retain their own purposes.
func (s *archiveService) browse(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("includeFiles") == "true" {
		s.inspectDirectory(w, r)
		return
	}
	sourceOnly := r.URL.Query().Get("purpose") == "source"
	settings := s.settingsSnapshot()
	excluded := []string{s.config.StateDir}
	if settings.ArchiveBase != "" {
		excluded = append(excluded, settings.ArchiveBase)
	}
	roots := s.config.Roots
	if sourceOnly {
		roots = settings.SourceRoots
	}
	if sourceOnly && r.URL.Query().Get("archiveBase") != "" {
		target, err := s.allowedPath(r.URL.Query().Get("archiveBase"), false)
		if err != nil {
			serviceError(w, 400, "invalid_request", "Archive destination is outside configured roots.")
			return
		}
		excluded = append(excluded, target)
	}
	hidden := func(path string) bool {
		if !sourceOnly {
			return false
		}
		for _, target := range excluded {
			if pathWithin(path, target) {
				return true
			}
		}
		return false
	}
	selectable := func(path string) bool {
		if path == "" {
			return false
		}
		if !sourceOnly {
			return true
		}
		for _, target := range excluded {
			if pathsOverlap(path, target) {
				return false
			}
		}
		return true
	}
	type entry struct {
		Name       string `json:"name"`
		Path       string `json:"path"`
		Selectable bool   `json:"selectable"`
	}
	entries := []entry{}
	path := r.URL.Query().Get("path")
	var parent *string
	if path == "" {
		for _, root := range roots {
			if !hidden(root) {
				entries = append(entries, entry{filepath.Base(root), root, selectable(root)})
			}
		}
	} else {
		resolved, err := s.allowedPath(path, true)
		if err != nil {
			serviceError(w, 403, "forbidden", err.Error())
			return
		}
		if hidden(resolved) {
			serviceError(w, 403, "invalid_source", "Archive and service state directories cannot be selected as sources.")
			return
		}
		if sourceOnly {
			permitted := false
			for _, root := range roots {
				if pathWithin(resolved, root) {
					permitted = true
				}
			}
			if !permitted {
				serviceError(w, 403, "invalid_source", "请选择配置的来源目录。")
				return
			}
		}
		path = resolved
		list, err := os.ReadDir(path)
		if err != nil {
			serviceError(w, 403, "unreadable", err.Error())
			return
		}
		for _, d := range list {
			if serviceExcluded(d.Name()) {
				continue
			}
			if !d.IsDir() && d.Type()&os.ModeSymlink == 0 {
				continue
			}
			v, e := s.allowedPath(filepath.Join(path, d.Name()), true)
			if e == nil && !hidden(v) {
				entries = append(entries, entry{d.Name(), v, selectable(v)})
			}
		}
		candidate := filepath.Dir(path)
		if candidate != path {
			if p, e := s.allowedPath(candidate, true); e == nil && !hidden(p) {
				permitted := !sourceOnly
				for _, root := range roots {
					if pathWithin(p, root) {
						permitted = true
					}
				}
				if permitted {
					parent = &p
				}
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	writeServiceJSON(w, 200, map[string]interface{}{"path": path, "parentPath": parent, "entries": entries, "selectable": selectable(path)})
}

func (s *archiveService) files(w http.ResponseWriter, r *http.Request, id string) {
	filter := r.URL.Query().Get("filter")
	if filter == "" {
		filter = "all"
	}
	if filter != "all" && filter != "attention" && filter != "failed" && filter != "skipped" {
		serviceError(w, 400, "invalid_request", "Unknown file filter.")
		return
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if r.URL.Query().Get("offset") == "" {
		offset = 0
		err = nil
	}
	if err != nil || offset < 0 {
		serviceError(w, 400, "invalid_request", "Invalid offset.")
		return
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if r.URL.Query().Get("limit") == "" {
		limit = 200
		err = nil
	}
	if err != nil || limit < 1 || limit > 1000 {
		serviceError(w, 400, "invalid_request", "Limit must be between 1 and 1000.")
		return
	}
	s.mu.Lock()
	j := s.jobs[id]
	if j == nil {
		s.mu.Unlock()
		serviceError(w, 404, "not_found", "Task not found.")
		return
	}
	matches := []ServiceFile{}
	for _, f := range j.Files {
		if filter == "skipped" && f.Status != "skipped" {
			continue
		}
		if filter == "failed" && f.Status != "failed" {
			continue
		}
		if filter == "attention" && f.Warning == "" && f.Error == "" && f.Status != "unsupported" {
			continue
		}
		matches = append(matches, *f)
	}
	s.mu.Unlock()
	total := len(matches)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeServiceJSON(w, 200, map[string]interface{}{"files": matches[offset:end], "total": total})
}
func (s *archiveService) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		serviceError(w, 500, "stream_unavailable", "Streaming is unavailable.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	io.WriteString(w, ": connected\n\n")
	flusher.Flush()
	seen := map[string]time.Time{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heart := time.NewTicker(15 * time.Second)
	defer heart.Stop()
	send := func() bool {
		for _, j := range s.listJobs() {
			if t, ok := seen[j.ID]; ok && t.Equal(j.UpdatedAt) {
				continue
			}
			b, err := json.Marshal(j)
			if err != nil {
				return false
			}
			if _, err = fmt.Fprintf(w, "event: job\ndata: %s\n\n", b); err != nil {
				return false
			}
			seen[j.ID] = j.UpdatedAt
		}
		flusher.Flush()
		return true
	}
	if !send() {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stop:
			return
		case <-ticker.C:
			if !send() {
				return
			}
		case <-heart.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
