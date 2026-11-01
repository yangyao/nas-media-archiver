package main

import "time"

const serviceVersion = "0.2.6"

type ServiceJob struct {
	Started                 bool           `json:"started"`
	SkipDuplicates          bool           `json:"skipDuplicates"`
	SkippedFiles            int            `json:"skippedFiles"`
	SkippedBytes            int64          `json:"skippedBytes"`
	ID                      string         `json:"id"`
	Name                    string         `json:"name"`
	SourcePaths             []string       `json:"sourcePaths"`
	ArchiveBase             string         `json:"archiveBase"`
	CleanupEmptyDirectories bool           `json:"cleanupEmptyDirectories"`
	Status                  string         `json:"status"`
	CreatedAt               time.Time      `json:"createdAt"`
	UpdatedAt               time.Time      `json:"updatedAt"`
	ScanComplete            bool           `json:"scanComplete"`
	UnsupportedFiles        int            `json:"unsupportedFiles"`
	UnsupportedBytes        int64          `json:"unsupportedBytes"`
	TotalFiles              int            `json:"totalFiles"`
	CompletedFiles          int            `json:"completedFiles"`
	FailedFiles             int            `json:"failedFiles"`
	AttentionFiles          int            `json:"attentionFiles"`
	TotalBytes              int64          `json:"totalBytes"`
	CompletedBytes          int64          `json:"completedBytes"`
	CurrentFile             string         `json:"currentFile"`
	CurrentFileBytes        int64          `json:"currentFileBytes"`
	DateRangeStart          string         `json:"dateRangeStart"`
	DateRangeEnd            string         `json:"dateRangeEnd"`
	Error                   string         `json:"error"`
	CleanupStatus           string         `json:"cleanupStatus"`
	ResidualPaths           []string       `json:"residualPaths"`
	Files                   []*ServiceFile `json:"-"`
	scanCache               map[string]ServiceFile
	counted                 map[string]ServiceFile
	firstDate               time.Time
	lastDate                time.Time
}

type ServiceFile struct {
	DuplicateOf    string    `json:"duplicateOf,omitempty"`
	ID             string    `json:"id"`
	SourcePath     string    `json:"sourcePath"`
	TargetPath     string    `json:"targetPath"`
	Size           int64     `json:"size"`
	Status         string    `json:"status"`
	MetadataSource string    `json:"metadataSource"`
	DateTime       string    `json:"dateTime"`
	Warning        string    `json:"warning"`
	Error          string    `json:"error"`
	SHA256         string    `json:"sha256"`
	MTime          time.Time `json:"mtime"`
	StagingPath    string    `json:"stagingPath,omitempty"`
	Published      bool      `json:"published,omitempty"`
}

type ServiceConfig struct {
	Listen      string
	StateDir    string
	ArchiveBase string
	Roots       []string
	TokenFile   string
	Name        string
}

// Rebuild only on load/reset. Hot-path updates apply one file's delta.
func (j *ServiceJob) recalculate() {
	j.TotalFiles = len(j.Files)
	j.UnsupportedFiles = 0
	j.UnsupportedBytes = 0
	j.CompletedFiles = 0
	j.SkippedFiles = 0
	j.SkippedBytes = 0
	j.FailedFiles = 0
	j.AttentionFiles = 0
	j.TotalBytes = 0
	j.CompletedBytes = 0
	j.DateRangeStart = ""
	j.DateRangeEnd = ""
	j.firstDate = time.Time{}
	j.lastDate = time.Time{}
	j.counted = make(map[string]ServiceFile, len(j.Files))
	for _, f := range j.Files {
		j.updateFile(f)
	}
}
func (j *ServiceJob) applyFile(f ServiceFile, sign int) {
	j.TotalBytes += int64(sign) * f.Size
	if f.Status == "skipped" {
		j.SkippedFiles += sign
		j.SkippedBytes += int64(sign) * f.Size
	}
	if f.Status == "unsupported" {
		j.UnsupportedFiles += sign
		j.UnsupportedBytes += int64(sign) * f.Size
	}
	if f.Status == "completed" {
		j.CompletedFiles += sign
		j.CompletedBytes += int64(sign) * f.Size
	}
	if f.Status == "failed" {
		j.FailedFiles += sign
	}
	if f.Warning != "" || f.Error != "" || f.Status == "unsupported" {
		j.AttentionFiles += sign
	}
}
func (j *ServiceJob) updateFile(f *ServiceFile) {
	if j.counted == nil {
		j.counted = map[string]ServiceFile{}
	}
	old, exists := j.counted[f.ID]
	if exists {
		j.applyFile(old, -1)
	}
	j.applyFile(*f, 1)
	j.counted[f.ID] = *f
	j.TotalFiles = len(j.Files)
	if exists && old.DateTime == f.DateTime {
		return
	}
	// Capture times normally only change from empty to parsed. On explicit
	// replan, rebuilding the bounds is rare and avoids stale extrema.
	if old.DateTime != "" && (old.DateTime == j.DateRangeStart || old.DateTime == j.DateRangeEnd) {
		j.firstDate = time.Time{}
		j.lastDate = time.Time{}
		j.DateRangeStart = ""
		j.DateRangeEnd = ""
		for _, v := range j.counted {
			j.includeDate(v.DateTime)
		}
	} else {
		j.includeDate(f.DateTime)
	}
}
func (j *ServiceJob) includeDate(value string) {
	dt, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return
	}
	if j.firstDate.IsZero() || dt.Before(j.firstDate) {
		j.firstDate = dt
		j.DateRangeStart = value
	}
	if j.lastDate.IsZero() || dt.After(j.lastDate) {
		j.lastDate = dt
		j.DateRangeEnd = value
	}
}

func serviceTerminal(s string) bool {
	return s == "completed" || s == "completed_with_errors" || s == "failed"
}
