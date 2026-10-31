package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const serviceMetadataWorkers = 4
const serviceMetadataTimeout = 30 * time.Second

// Each metadata worker owns one lazy ExifTool process. Its requests are serial,
// while independent workers run in parallel. No external daemon is required.
type serviceMetadataReader struct {
	executable  string
	cmd         *exec.Cmd
	input       io.WriteCloser
	output      *bufio.Scanner
	unavailable bool
}

func (r *serviceMetadataReader) Close() {
	if r.cmd != nil {
		_ = r.input.Close()
		_ = r.cmd.Process.Kill()
		_ = r.cmd.Wait()
		r.cmd = nil
	}
}
func (r *serviceMetadataReader) exif(path string) (time.Time, error) {
	if r.unavailable {
		return time.Time{}, errors.New("ExifTool unavailable")
	}
	if strings.ContainsAny(path, "\r\n") {
		return time.Time{}, errors.New("ExifTool argument contains newline")
	}
	if r.cmd == nil {
		bin := r.executable
		if bin == "" {
			bin = "/opt/bin/exiftool"
		}
		if _, err := os.Stat(bin); err != nil {
			bin, err = exec.LookPath("exiftool")
			if err != nil {
				r.unavailable = true
				return time.Time{}, err
			}
		}
		cmd := exec.Command(bin, "-stay_open", "True", "-@", "-")
		in, err := cmd.StdinPipe()
		if err != nil {
			return time.Time{}, err
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			in.Close()
			return time.Time{}, err
		}
		if err = cmd.Start(); err != nil {
			in.Close()
			out.Close()
			return time.Time{}, err
		}
		r.cmd = cmd
		r.input = in
		r.output = bufio.NewScanner(out)
	}
	type response struct {
		date time.Time
		err  error
	}
	done := make(chan response, 1)
	input, output := r.input, r.output
	go func() {
		if _, err := fmt.Fprintf(input, "-s3\n-DateTimeOriginal\n-CreateDate\n%s\n-execute\n", path); err != nil {
			done <- response{err: err}
			return
		}
		var date time.Time
		for output.Scan() {
			line := strings.TrimSpace(output.Text())
			if line == "{ready}" {
				done <- response{date: date}
				return
			}
			if date.IsZero() {
				date, _ = time.Parse("2006:01:02 15:04:05", line)
			}
		}
		err := output.Err()
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		done <- response{err: err}
	}()
	timer := time.NewTimer(serviceMetadataTimeout)
	defer timer.Stop()
	select {
	case v := <-done:
		if v.err != nil {
			r.Close()
			return time.Time{}, v.err
		}
		if v.date.IsZero() {
			return time.Time{}, errors.New("no ExifTool capture date")
		}
		return v.date, nil
	case <-timer.C:
		r.Close()
		<-done
		return time.Time{}, errors.New("ExifTool timed out")
	}
}
func (r *serviceMetadataReader) detect(path string, mtime time.Time) (time.Time, string, error) {
	if dt, ok := parseFilenameTime(filepath.Base(path)); ok {
		return dt, "filename", nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		if dt, err := extractJPEGExif(path); err == nil {
			return dt, "native-exif", nil
		}
	case ".heic", ".png", ".webp", ".gif":
		if dt, err := r.exif(path); err == nil {
			return dt, "exiftool", nil
		}
	case ".mp4", ".mov", ".3gp":
		if dt, err := extractWithFFProbe(path); err == nil {
			return dt, "ffprobe", nil
		}
	}
	return mtime, "mtime", nil
}

// Used by the legacy external-tool helpers as well as service video parsing.
func metadataCommand(name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), serviceMetadataTimeout)
	return exec.CommandContext(ctx, name, args...), cancel
}
