package main

import "time"

const serviceVersion = "0.2.0"

type ServiceJob struct {
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
}

type ServiceFile struct {
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

func (j *ServiceJob) recalculate() {
	j.TotalFiles = len(j.Files)
	j.UnsupportedFiles = 0
	j.UnsupportedBytes = 0
	j.CompletedFiles = 0
	j.FailedFiles = 0
	j.AttentionFiles = 0
	j.TotalBytes = 0
	j.CompletedBytes = 0
	var first, last time.Time
	for _, f := range j.Files {
		j.TotalBytes += f.Size
		if f.Status == "unsupported" {
			j.UnsupportedFiles++
			j.UnsupportedBytes += f.Size
		}
		if f.Status == "completed" {
			j.CompletedFiles++
			j.CompletedBytes += f.Size
		}
		if f.Status == "failed" {
			j.FailedFiles++
		}
		if f.Warning != "" || f.Status == "unsupported" {
			j.AttentionFiles++
		}
		if dt, err := time.Parse(time.RFC3339, f.DateTime); err == nil {
			if first.IsZero() || dt.Before(first) {
				first = dt
			}
			if last.IsZero() || dt.After(last) {
				last = dt
			}
		}
	}
	if !first.IsZero() {
		j.DateRangeStart = first.Format(time.RFC3339)
		j.DateRangeEnd = last.Format(time.RFC3339)
	}
}

func serviceTerminal(s string) bool {
	return s == "completed" || s == "completed_with_errors" || s == "failed"
}
