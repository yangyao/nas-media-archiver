package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

type ArchiveSettings struct {
	Configured  bool     `json:"configured"`
	SourceRoots []string `json:"sourceRoots"`
	ArchiveBase string   `json:"archiveBase"`
}

func (s *archiveService) settingsSnapshot() ArchiveSettings {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	v := s.settings
	v.SourceRoots = append([]string{}, v.SourceRoots...)
	return v
}
func (s *archiveService) loadSettings() error {
	v := ArchiveSettings{Configured: s.config.ArchiveBase != "", SourceRoots: s.config.Roots, ArchiveBase: s.config.ArchiveBase}
	v.SourceRoots = []string{}
	for _, root := range s.config.Roots {
		if v.ArchiveBase == "" || !pathWithin(root, v.ArchiveBase) {
			v.SourceRoots = append(v.SourceRoots, root)
		}
	}
	data, err := os.ReadFile(filepath.Join(s.config.StateDir, "settings.json"))
	if err == nil {
		if err = json.Unmarshal(data, &v); err != nil {
			return err
		}
		if v.Configured {
			if v.ArchiveBase, err = s.allowedPath(v.ArchiveBase, true); err != nil {
				return err
			}
			for i, p := range v.SourceRoots {
				if v.SourceRoots[i], err = s.allowedPath(p, true); err != nil {
					return err
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	s.settings = v
	return nil
}
func (s *archiveService) settingsEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		writeServiceJSON(w, 200, s.settingsSnapshot())
		return
	}
	if r.Method != "PUT" {
		serviceError(w, 405, "method_not_allowed", "Use GET or PUT.")
		return
	}
	var v ArchiveSettings
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		serviceError(w, 400, "invalid_settings", "配置格式不正确。")
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		serviceError(w, 400, "invalid_settings", "请只提交一份配置。")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		serviceError(w, 409, "busy", "服务正在关闭。")
		return
	}
	for _, j := range s.jobs {
		if j.Status == "scanning" || j.Status == "running" || j.Status == "pausing" || j.Status == "queued" {
			serviceError(w, 409, "busy", "请等待当前任务停止后再修改归档设置。")
			return
		}
	}
	for _, split := range s.splits {
		if split.Status == "running" {
			serviceError(w, 409, "busy", "请等待拆分完成后再修改设置。")
			return
		}
	}
	if len(v.SourceRoots) == 0 || len(v.SourceRoots) > 100 {
		serviceError(w, 400, "invalid_settings", "请选择来源目录。")
		return
	}
	target, err := s.allowedPath(v.ArchiveBase, true)
	if err != nil || pathsOverlap(target, s.config.StateDir) {
		serviceError(w, 400, "invalid_settings", "请选择有效的归档目录，且不能与服务数据目录重叠。")
		return
	}
	roots := []string{}
	for _, p := range v.SourceRoots {
		p, err = s.allowedPath(p, true)
		if err != nil || pathsOverlap(p, target) || pathsOverlap(p, s.config.StateDir) {
			serviceError(w, 400, "invalid_settings", "来源目录不可与归档或服务数据目录重叠，且必须在允许访问的范围内。")
			return
		}
		if _, err = os.ReadDir(p); err != nil {
			serviceError(w, 400, "invalid_settings", "无法读取来源目录。")
			return
		}
		for _, other := range roots {
			if pathsOverlap(p, other) {
				serviceError(w, 400, "invalid_settings", "来源目录不可重复或相互包含。")
				return
			}
		}
		roots = append(roots, p)
	}
	// Verify effective write access without changing existing files.
	f, err := os.CreateTemp(target, ".archive-permission-")
	if err != nil {
		serviceError(w, 400, "invalid_settings", "归档目录不可写。")
		return
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	v = ArchiveSettings{Configured: true, SourceRoots: roots, ArchiveBase: target}
	data, _ := json.MarshalIndent(v, "", "  ")
	f, err = os.CreateTemp(s.config.StateDir, ".settings-")
	if err == nil {
		defer os.Remove(f.Name())
		err = f.Chmod(0600)
		if err == nil {
			_, err = f.Write(data)
		}
		if err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
		if err == nil {
			err = os.Rename(f.Name(), filepath.Join(s.config.StateDir, "settings.json"))
		}
	}
	if err != nil {
		serviceError(w, 500, "save_failed", "无法保存服务端配置。")
		return
	}
	s.settingsMu.Lock()
	s.settings = v
	s.settingsMu.Unlock()
	writeServiceJSON(w, 200, v)
}
