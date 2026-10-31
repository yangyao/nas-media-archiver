package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func serviceExcluded(name string) bool {
	switch name {
	case ".@__thumb", "@eaDir", "@Recycle", ".stfolder", ".stversions":
		return true
	}
	return false
}
func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func pathsOverlap(a, b string) bool { return pathWithin(a, b) || pathWithin(b, a) }

// Resolve even a not-yet-created destination through its nearest existing ancestor.
func canonicalServicePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("an absolute path is required")
	}
	path = filepath.Clean(path)
	current := path
	parts := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(parts) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, parts[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		parts = append(parts, filepath.Base(current))
		current = parent
	}
}
func (s *archiveService) allowedPath(path string, mustExist bool) (string, error) {
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
	// System directories cannot be selected by manually typing a path either.
	for _, part := range strings.Split(filepath.ToSlash(resolved), "/") {
		if serviceExcluded(part) {
			return "", errors.New("system directory is excluded")
		}
	}
	if mustExist {
		info, e := os.Stat(resolved)
		if e != nil {
			return "", e
		}
		if !info.IsDir() {
			return "", errors.New("path is not a directory")
		}
	}
	return resolved, nil
}
func (s *archiveService) validateJobPaths(sources []string, base string) ([]string, string, error) {
	if len(sources) == 0 {
		return nil, "", errors.New("select at least one source directory")
	}
	if len(sources) > 100 {
		return nil, "", errors.New("too many source directories")
	}
	target, err := s.allowedPath(base, false)
	if err != nil {
		return nil, "", fmt.Errorf("archive destination: %w", err)
	}
	result := []string{}
	for _, source := range sources {
		path, e := s.allowedPath(source, true)
		if e != nil {
			return nil, "", fmt.Errorf("source directory: %w", e)
		}
		if pathsOverlap(path, target) {
			return nil, "", errors.New("source and archive directories must not overlap")
		}
		if pathsOverlap(path, s.config.StateDir) {
			return nil, "", errors.New("source and service state directories must not overlap")
		}
		for _, other := range result {
			if pathsOverlap(path, other) {
				return nil, "", errors.New("selected source directories overlap")
			}
		}
		result = append(result, path)
	}
	return result, target, nil
}
