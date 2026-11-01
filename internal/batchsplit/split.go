// Package batchsplit moves whole files into size-limited sibling directories.
// Planning is read-only. Persist the plan before calling Execute for recovery.
package batchsplit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const DefaultLimit int64 = 8_000_000_000

type File struct {
	Relative string `json:"relative"`
	Size     int64  `json:"size"`
	MTime    int64  `json:"mtime"`
	Device   uint64 `json:"device"`
	Inode    uint64 `json:"inode"`
	Batch    int    `json:"batch"`
}
type Batch struct {
	Path      string `json:"path"`
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"`
	Oversized bool   `json:"oversized"`
}
type Plan struct {
	Source          string  `json:"sourcePath"`
	Prefix          string  `json:"prefix"`
	Limit           int64   `json:"maxBytes"`
	TotalFiles      int     `json:"totalFiles"`
	TotalBytes      int64   `json:"totalBytes"`
	RetainedEntries int     `json:"retainedEntries"`
	Batches         []Batch `json:"batches"`
	Files           []File  `json:"files"`
}

func identity(info os.FileInfo) (uint64, uint64) {
	st := info.Sys().(*syscall.Stat_t)
	return uint64(st.Dev), uint64(st.Ino)
}
func matches(info os.FileInfo, f File) bool {
	d, i := identity(info)
	return info.Mode().IsRegular() && info.Size() == f.Size && info.ModTime().UnixNano() == f.MTime && d == f.Device && i == f.Inode
}
func canonical(path string) (string, error) {
	p, e := filepath.EvalSymlinks(path)
	if e != nil {
		return "", e
	}
	return filepath.Abs(p)
}
func New(source, prefix string, limit int64) (Plan, error) {
	p := Plan{Prefix: strings.TrimSpace(prefix), Limit: limit, Batches: []Batch{}, Files: []File{}}
	if limit <= 0 {
		return p, errors.New("每批大小必须大于零")
	}
	var err error
	p.Source, err = canonical(source)
	if err != nil {
		return p, err
	}
	for _, part := range strings.Split(p.Source, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return p, errors.New("隐藏或同步目录不能作为拆分来源")
		}
	}
	if p.Source == filepath.Dir(p.Source) {
		return p, errors.New("不能拆分文件系统根目录")
	}
	info, err := os.Stat(p.Source)
	if err != nil || !info.IsDir() {
		return p, errors.New("请选择有效的来源目录")
	}
	if p.Prefix == "" {
		p.Prefix = filepath.Base(p.Source)
	}
	if len(p.Prefix) > 120 || strings.ContainsAny(p.Prefix, "/\\\x00\r\n") || strings.HasPrefix(p.Prefix, ".") {
		return p, errors.New("批次名称不可包含路径或以点开头")
	}
	next := 1
	newBatch := func() error {
		for {
			path := filepath.Join(filepath.Dir(p.Source), fmt.Sprintf("%s batch %02d", p.Prefix, next))
			next++
			if next > 100001 {
				return errors.New("批次数量过多")
			}
			_, e := os.Lstat(path)
			if os.IsNotExist(e) {
				p.Batches = append(p.Batches, Batch{Path: path})
				return nil
			}
			if e != nil {
				return e
			}
		}
	}
	err = filepath.WalkDir(p.Source, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if path == p.Source {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			p.RetainedEntries++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			p.RetainedEntries++
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			p.RetainedEntries++
			return nil
		}
		if len(p.Batches) == 0 || (p.Batches[len(p.Batches)-1].Files > 0 && info.Size() > limit-p.Batches[len(p.Batches)-1].Bytes) {
			if e = newBatch(); e != nil {
				return e
			}
		}
		b := len(p.Batches) - 1
		rel, _ := filepath.Rel(p.Source, path)
		dev, ino := identity(info)
		p.Files = append(p.Files, File{Relative: rel, Size: info.Size(), MTime: info.ModTime().UnixNano(), Device: dev, Inode: ino, Batch: b})
		p.Batches[b].Files++
		p.Batches[b].Bytes += info.Size()
		p.Batches[b].Oversized = p.Batches[b].Bytes > limit
		p.TotalFiles++
		p.TotalBytes += info.Size()
		return nil
	})
	return p, err
}
func (p Plan) validateSource() error {
	seen := map[string]bool{}
	for _, b := range p.Batches {
		if filepath.Dir(b.Path) != filepath.Dir(p.Source) || b.Path == p.Source || seen[b.Path] {
			return errors.New("批次必须是互不重复的平级目录")
		}
		seen[b.Path] = true
	}
	v, e := canonical(p.Source)
	if e != nil || v != p.Source {
		return errors.New("来源目录已改变")
	}
	return nil
}
func (p Plan) filePaths(f File) (string, string, error) {
	if f.Batch < 0 || f.Batch >= len(p.Batches) || (f.Relative == "." || f.Relative == "..") || filepath.IsAbs(f.Relative) || filepath.Clean(f.Relative) != f.Relative || strings.HasPrefix(f.Relative, ".."+string(filepath.Separator)) {
		return "", "", errors.New("无效的拆分计划")
	}
	src := filepath.Join(p.Source, f.Relative)
	dst := filepath.Join(p.Batches[f.Batch].Path, f.Relative)
	for _, v := range []string{filepath.Dir(src), filepath.Dir(dst)} {
		resolved, e := canonical(v)
		if e == nil && resolved != v {
			return "", "", errors.New("目录已被符号链接替换")
		}
		if e != nil && !os.IsNotExist(e) {
			return "", "", e
		}
	}
	return src, dst, nil
}

// Reserve creates empty batch directories exclusively; existing paths are never reused.
func (p Plan) Reserve() error {
	if e := p.validateSource(); e != nil {
		return e
	}
	for _, f := range p.Files {
		src, _, e := p.filePaths(f)
		if e != nil {
			return e
		}
		i, e := os.Lstat(src)
		if e != nil || !matches(i, f) {
			return fmt.Errorf("文件已变化，请重新预览：%s", f.Relative)
		}
	}
	sourceInfo, err := os.Stat(p.Source)
	if err != nil {
		return err
	}
	created := []string{}
	for _, b := range p.Batches {
		if filepath.Dir(b.Path) != filepath.Dir(p.Source) || b.Path == p.Source {
			return errors.New("批次必须是平级目录")
		}
		if e := os.Mkdir(b.Path, 0755); e != nil {
			for _, v := range created {
				os.Remove(v)
			}
			return fmt.Errorf("批次目录已存在或无法创建，请重新预览：%w", e)
		}
		created = append(created, b.Path)
		if e := os.Chmod(b.Path, sourceInfo.Mode().Perm()); e != nil {
			return e
		}
		if os.Geteuid() == 0 {
			st := sourceInfo.Sys().(*syscall.Stat_t)
			if e := os.Chown(b.Path, int(st.Uid), int(st.Gid)); e != nil {
				return e
			}
		}
	}
	return syncDir(filepath.Dir(p.Source))
}
func syncDir(path string) error {
	d, e := os.Open(path)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}

// Execute can safely resume a persisted plan. It never overwrites a target;
// hard-link publication and directory fsync precede removal of the original name.
func (p Plan) Execute(stop func() bool, progress func(int, int64)) error {
	if e := p.validateSource(); e != nil {
		return e
	}
	n := 0
	var bytes int64
	for _, f := range p.Files {
		if stop() {
			return errors.New("拆分已中断，可继续执行")
		}
		src, dst, e := p.filePaths(f)
		if e != nil {
			return e
		}
		b := p.Batches[f.Batch].Path
		v, e := canonical(b)
		if e != nil || v != b {
			return errors.New("批次目录已改变")
		}
		if e = os.MkdirAll(filepath.Dir(dst), 0755); e != nil {
			return e
		}
		// Recheck ancestor aliases after creating nested directories.
		if _, _, e = p.filePaths(f); e != nil {
			return e
		}
		si, se := os.Lstat(src)
		di, de := os.Lstat(dst)
		if de == nil {
			if !matches(di, f) {
				return fmt.Errorf("目标已存在且不是原文件：%s", dst)
			}
		} else if !os.IsNotExist(de) {
			return de
		}
		if se != nil && !os.IsNotExist(se) {
			return se
		}
		if se == nil {
			if !matches(si, f) {
				return fmt.Errorf("来源文件已改变：%s", src)
			}
			if os.IsNotExist(de) {
				if e = os.Link(src, dst); e != nil {
					return e
				}
			}
			// Persist nested directory links as well as the new file name
			// before removing its source name.
			for dir := filepath.Dir(dst); ; dir = filepath.Dir(dir) {
				if e = syncDir(dir); e != nil {
					return e
				}
				if dir == filepath.Dir(p.Source) {
					break
				}
			}
			si, e = os.Lstat(src)
			if e != nil || !matches(si, f) {
				return fmt.Errorf("来源文件已改变，已保留两处路径：%s", src)
			}
			di, e = os.Lstat(dst)
			if e != nil || !matches(di, f) || !os.SameFile(si, di) {
				return fmt.Errorf("目标文件已改变，来源已保留：%s", dst)
			}
			if _, _, e = p.filePaths(f); e != nil {
				return e
			}
			if e = os.Remove(src); e != nil {
				return e
			}
			if e = syncDir(filepath.Dir(src)); e != nil {
				return e
			}
		} else if de != nil {
			return fmt.Errorf("来源和目标均不存在：%s", f.Relative)
		}
		n++
		bytes += f.Size
		progress(n, bytes)
	}
	// Leave the source directory and all hidden/sync entries intact. Only remove
	// now-empty visible descendants, using rmdir so concurrent new files survive.
	var dirs []string
	_ = filepath.WalkDir(p.Source, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		if d.IsDir() && path != p.Source {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			dirs = append(dirs, path)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = syscall.Rmdir(dirs[i])
	}
	return nil
}
