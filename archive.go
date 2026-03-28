package main

import (
	"context"
	"crypto/md5"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

type JobStatus string
type TaskStatus string
type EventStatus string

const (
	JobCreated   JobStatus = "created"
	JobRunning   JobStatus = "running"
	JobCompleted JobStatus = "completed"
	JobFailed    JobStatus = "failed"

	TaskDiscovered      TaskStatus = "discovered"
	TaskQueued          TaskStatus = "queued"
	TaskMetadataRunning TaskStatus = "metadata_running"
	TaskMetadataDone    TaskStatus = "metadata_done"
	TaskPlanningDone    TaskStatus = "planning_done"
	TaskMD5Pending      TaskStatus = "md5_pending"
	TaskMD5Running      TaskStatus = "md5_running"
	TaskMoveQueued      TaskStatus = "move_queued"
	TaskMoving          TaskStatus = "moving"
	TaskCompleted       TaskStatus = "completed"
	TaskFailed          TaskStatus = "failed"
	TaskSkipped         TaskStatus = "skipped"
)

type Job struct {
	ID           string           `json:"id"`
	SourcePath   string           `json:"sourcePath"`
	ArchiveBase  string           `json:"archiveBase,omitempty"`
	Status       JobStatus        `json:"status"`
	CreatedAt    time.Time        `json:"createdAt"`
	UpdatedAt    time.Time        `json:"updatedAt"`
	StartedAt    *time.Time       `json:"startedAt,omitempty"`
	FinishedAt   *time.Time       `json:"finishedAt,omitempty"`
	NextEventSeq int64            `json:"nextEventSeq"`
	Tasks        map[string]*Task `json:"tasks"`
}

type Task struct {
	JobID            string     `json:"jobId"`
	FileID           string     `json:"fileId"`
	SourcePath       string     `json:"sourcePath"`
	Size             int64      `json:"size"`
	MTime            time.Time  `json:"mtime"`
	Status           TaskStatus `json:"status"`
	ErrorMessage     string     `json:"errorMessage,omitempty"`
	DateTimeOriginal *time.Time `json:"datetimeOriginal,omitempty"`
	MetadataSource   string     `json:"metadataSource,omitempty"`
	MD5              string     `json:"md5,omitempty"`
	MD5ComputedAt    *time.Time `json:"md5ComputedAt,omitempty"`
	TargetPath       string     `json:"targetPath,omitempty"`
	ArchiveType      string     `json:"archiveType,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type Event struct {
	JobID         string      `json:"jobId"`
	FileID        string      `json:"fileId,omitempty"`
	Path          string      `json:"path,omitempty"`
	Stage         string      `json:"stage"`
	Status        EventStatus `json:"status"`
	DateTime      string      `json:"datetime,omitempty"`
	Source        string      `json:"source,omitempty"`
	TargetPath    string      `json:"targetPath,omitempty"`
	ErrorMessage  string      `json:"errorMessage,omitempty"`
	QueuePosition int         `json:"queuePosition,omitempty"`
	EventSeq      int64       `json:"eventSeq"`
	EmittedAt     time.Time   `json:"emittedAt"`
}

type Store struct {
	root string
	mu   sync.Mutex
}

type EventSink interface {
	Append(Event) error
}

type EventWriter struct {
	store *Store
	job   *Job
	f     *os.File
	enc   *json.Encoder
	mu    sync.Mutex
}

func NewStore(root string) *Store {
	return &Store{root: root}
}

func (s *Store) Ensure() error {
	return os.MkdirAll(s.root, 0o755)
}

func (s *Store) jobDir(jobID string) string {
	return filepath.Join(s.root, jobID)
}

func (s *Store) snapshotPath(jobID string) string {
	return filepath.Join(s.jobDir(jobID), "job.json")
}

func (s *Store) eventsPath(jobID string) string {
	return filepath.Join(s.jobDir(jobID), "events.jsonl")
}

func (s *Store) SaveJob(job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.jobDir(job.ID), 0o755); err != nil {
		return err
	}
	job.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.snapshotPath(job.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.snapshotPath(job.ID))
}

func (s *Store) LoadJob(jobID string) (*Job, error) {
	data, err := os.ReadFile(s.snapshotPath(jobID))
	if err != nil {
		return nil, err
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, err
	}
	if job.Tasks == nil {
		job.Tasks = map[string]*Task{}
	}
	return &job, nil
}

func (s *Store) AppendEvent(job *Job, ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.jobDir(job.ID), 0o755); err != nil {
		return err
	}
	ev.EventSeq = job.NextEventSeq
	ev.EmittedAt = time.Now()
	job.NextEventSeq++
	f, err := os.OpenFile(s.eventsPath(job.ID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(ev)
}

func (s *Store) NewEventWriter(job *Job) (*EventWriter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.jobDir(job.ID), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.eventsPath(job.ID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &EventWriter{
		store: s,
		job:   job,
		f:     f,
		enc:   json.NewEncoder(f),
	}, nil
}

func (w *EventWriter) Append(ev Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.job.NextEventSeq++
	ev.EventSeq = w.job.NextEventSeq - 1
	ev.EmittedAt = time.Now()
	return w.enc.Encode(ev)
}

func (w *EventWriter) Close() error {
	if w == nil || w.f == nil {
		return nil
	}
	return w.f.Close()
}

func (s *Store) ReadEvents(jobID string) ([]Event, error) {
	f, err := os.Open(s.eventsPath(jobID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	var out []Event
	for {
		var ev Event
		if err := dec.Decode(&ev); err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return nil, err
		}
		out = append(out, ev)
	}
}

func (s *Store) ListJobs() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "scan":
		must(runScan(os.Args[2:]))
	case "run":
		must(runRun(os.Args[2:]))
	case "status":
		must(runStatus(os.Args[2:]))
	case "watch":
		must(runWatch(os.Args[2:]))
	case "files":
		must(runFiles(os.Args[2:]))
	case "retry":
		must(runRetry(os.Args[2:]))
	case "export":
		must(runExport(os.Args[2:]))
	case "jobs":
		must(runJobs(os.Args[2:]))
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `archive commands:
  scan    --path <dir> [--ext .jpg,.mp4] [--state-dir .archive-jobs]
  run     --job <job-id> --archive-base <dir> [--dry-run]
  status  --job <job-id>
  watch   --job <job-id> [--follow]
  files   --job <job-id> [--status failed]
  retry   --job <job-id>
  export  --job <job-id> [--format csv|json] [--output file] [--status failed]
  jobs    [--state-dir .archive-jobs]
`)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runScan(args []string) error {
	flags := flag.NewFlagSet("scan", flag.ContinueOnError)
	path := flags.String("path", "", "source path")
	ext := flags.String("ext", ".jpg,.jpeg,.heic,.png,.mp4,.mov,.3gp", "comma-separated extensions")
	stateDir := flags.String("state-dir", ".archive-jobs", "state dir")
	recursive := flags.Bool("recursive", true, "recursive scan")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("--path is required")
	}
	store := NewStore(*stateDir)
	if err := store.Ensure(); err != nil {
		return err
	}
	now := time.Now()
	jobID := fmt.Sprintf("job-%s-%09d", now.Format("20060102-150405"), now.Nanosecond())
	job := &Job{
		ID:         jobID,
		SourcePath: *path,
		Status:     JobCreated,
		CreatedAt:  now,
		UpdatedAt:  now,
		Tasks:      map[string]*Task{},
	}
	allowed := parseExts(*ext)
	var idx int
	walkFn := func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != *path && !*recursive {
				return filepath.SkipDir
			}
			return nil
		}
		extension := strings.ToLower(filepath.Ext(p))
		if _, ok := allowed[extension]; !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		idx++
		fileID := fmt.Sprintf("file-%06d", idx)
		now := time.Now()
		task := &Task{
			JobID:      jobID,
			FileID:     fileID,
			SourcePath: p,
			Size:       info.Size(),
			MTime:      info.ModTime(),
			Status:     TaskDiscovered,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		job.Tasks[fileID] = task
		return nil
	}
	if err := filepath.WalkDir(*path, walkFn); err != nil {
		return err
	}
	if err := store.SaveJob(job); err != nil {
		return err
	}
	if err := store.AppendEvent(job, Event{JobID: job.ID, Stage: "job_created", Status: "ok"}); err != nil {
		return err
	}
	if err := store.SaveJob(job); err != nil {
		return err
	}
	fmt.Printf("Job: %s\nFiles: %d\nStatus: %s\nScannedAt: %s\n", job.ID, len(job.Tasks), job.Status, job.CreatedAt.Format(time.RFC3339))
	return nil
}

func runRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	jobID := fs.String("job", "", "job id")
	archiveBase := fs.String("archive-base", "", "archive base path")
	stateDir := fs.String("state-dir", ".archive-jobs", "state dir")
	dryRun := fs.Bool("dry-run", false, "plan only, do not move files")
	snapshotEvery := fs.Int("snapshot-every", 100, "persist job snapshot every N processed files")
	workers := fs.Int("workers", max(1, min(4, runtime.NumCPU())), "number of concurrent metadata workers")
	workersFast := fs.Int("workers-fast", 0, "number of concurrent workers for non-video files")
	workersVideo := fs.Int("workers-video", 0, "number of concurrent workers for video files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *jobID == "" || *archiveBase == "" {
		return errors.New("--job and --archive-base are required")
	}
	store := NewStore(*stateDir)
	job, err := store.LoadJob(*jobID)
	if err != nil {
		return err
	}
	now := time.Now()
	job.Status = JobRunning
	job.ArchiveBase = *archiveBase
	job.StartedAt = &now
	if err := store.SaveJob(job); err != nil {
		return err
	}
	eventWriter, err := store.NewEventWriter(job)
	if err != nil {
		return err
	}
	defer eventWriter.Close()
	if err := eventWriter.Append(Event{JobID: job.ID, Stage: "job_started", Status: "ok"}); err != nil {
		return err
	}
	var firstErr error
	var errMu sync.Mutex
	var processed atomic.Int64
	writeMu := &sync.Mutex{}
	fastCh := make(chan *Task)
	videoCh := make(chan *Task)
	baseWorkers := max(1, *workers)
	fastWorkerCount := *workersFast
	videoWorkerCount := *workersVideo
	if fastWorkerCount <= 0 || videoWorkerCount <= 0 {
		if baseWorkers <= 1 {
			fastWorkerCount = 1
			videoWorkerCount = 1
		} else {
			fastWorkerCount = max(1, baseWorkers-1)
			videoWorkerCount = 1
		}
	}
	var wg sync.WaitGroup
	workerFn := func(ch <-chan *Task) {
		defer wg.Done()
		for task := range ch {
			if err := processTask(context.Background(), eventWriter, job, task, *dryRun, writeMu); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
			n := processed.Add(1)
			if fastWorkerCount+videoWorkerCount == 1 && *snapshotEvery > 0 && n%int64(*snapshotEvery) == 0 {
				errMu.Lock()
				if err := store.SaveJob(job); err != nil && firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}
	}
	for i := 0; i < fastWorkerCount; i++ {
		wg.Add(1)
		go workerFn(fastCh)
	}
	for i := 0; i < videoWorkerCount; i++ {
		wg.Add(1)
		go workerFn(videoCh)
	}
	for _, task := range sortedTasks(job.Tasks) {
		if task.Status == TaskCompleted || task.Status == TaskSkipped {
			continue
		}
		if isVideoTask(task.SourcePath) {
			videoCh <- task
		} else {
			fastCh <- task
		}
	}
	close(fastCh)
	close(videoCh)
	wg.Wait()
	finished := time.Now()
	job.FinishedAt = &finished
	if hasFailed(job.Tasks) {
		job.Status = JobFailed
	} else {
		job.Status = JobCompleted
	}
	if err := eventWriter.Append(Event{JobID: job.ID, Stage: "job_finished", Status: EventStatus(job.Status)}); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := store.SaveJob(job); err != nil && firstErr == nil {
		firstErr = err
	}
	fmt.Printf("Job: %s\nStatus: %s\n", job.ID, job.Status)
	return firstErr
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	jobID := fs.String("job", "", "job id")
	stateDir := fs.String("state-dir", ".archive-jobs", "state dir")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *jobID == "" {
		return errors.New("--job is required")
	}
	store := NewStore(*stateDir)
	job, err := store.LoadJob(*jobID)
	if err != nil {
		return err
	}
	var completed, failed, queued, metadataRunning, md5Running int
	for _, task := range job.Tasks {
		switch task.Status {
		case TaskCompleted:
			completed++
		case TaskFailed:
			failed++
		case TaskQueued, TaskMoveQueued:
			queued++
		case TaskMetadataRunning:
			metadataRunning++
		case TaskMD5Running:
			md5Running++
		}
	}
	fmt.Printf("Job: %s\nStatus: %s\nTotal: %d\nCompleted: %d\nFailed: %d\nQueued: %d\nMetadataRunning: %d\nMD5Running: %d\nStartedAt: %s\nUpdatedAt: %s\n",
		job.ID, job.Status, len(job.Tasks), completed, failed, queued, metadataRunning, md5Running, formatPtr(job.StartedAt), job.UpdatedAt.Format(time.RFC3339))
	return nil
}

func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	jobID := fs.String("job", "", "job id")
	stateDir := fs.String("state-dir", ".archive-jobs", "state dir")
	follow := fs.Bool("follow", true, "follow event log")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *jobID == "" {
		return errors.New("--job is required")
	}
	store := NewStore(*stateDir)
	printed := int64(0)
	for {
		events, err := store.ReadEvents(*jobID)
		if err != nil {
			return err
		}
		for _, ev := range events {
			if ev.EventSeq < printed {
				continue
			}
			fmt.Printf("[%s] %s %s", ev.EmittedAt.Format("15:04:05"), coalesce(ev.FileID, ev.JobID), ev.Stage)
			if ev.Source != "" {
				fmt.Printf(" source=%s", ev.Source)
			}
			if ev.ErrorMessage != "" {
				fmt.Printf(" error=%q", ev.ErrorMessage)
			}
			if ev.QueuePosition > 0 {
				fmt.Printf(" queue=%d", ev.QueuePosition)
			}
			fmt.Println()
			printed = ev.EventSeq + 1
		}
		if !*follow {
			return nil
		}
		job, err := store.LoadJob(*jobID)
		if err == nil && (job.Status == JobCompleted || job.Status == JobFailed) {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
}

func runFiles(args []string) error {
	fs := flag.NewFlagSet("files", flag.ContinueOnError)
	jobID := fs.String("job", "", "job id")
	stateDir := fs.String("state-dir", ".archive-jobs", "state dir")
	status := fs.String("status", "", "filter by status")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *jobID == "" {
		return errors.New("--job is required")
	}
	store := NewStore(*stateDir)
	job, err := store.LoadJob(*jobID)
	if err != nil {
		return err
	}
	for _, task := range sortedTasks(job.Tasks) {
		if *status != "" && string(task.Status) != *status {
			continue
		}
		fmt.Printf("%s\t%s\t%s\t%s\n", task.FileID, task.Status, task.MetadataSource, task.SourcePath)
	}
	return nil
}

func runRetry(args []string) error {
	fs := flag.NewFlagSet("retry", flag.ContinueOnError)
	jobID := fs.String("job", "", "job id")
	stateDir := fs.String("state-dir", ".archive-jobs", "state dir")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *jobID == "" {
		return errors.New("--job is required")
	}
	store := NewStore(*stateDir)
	job, err := store.LoadJob(*jobID)
	if err != nil {
		return err
	}
	for _, task := range job.Tasks {
		if task.Status == TaskFailed {
			task.Status = TaskDiscovered
			task.ErrorMessage = ""
			task.TargetPath = ""
			task.MD5 = ""
			task.MD5ComputedAt = nil
			task.UpdatedAt = time.Now()
		}
	}
	job.Status = JobCreated
	if err := store.AppendEvent(job, Event{JobID: job.ID, Stage: "job_retried", Status: "ok"}); err != nil {
		return err
	}
	if err := store.SaveJob(job); err != nil {
		return err
	}
	fmt.Printf("Job: %s\nStatus: %s\n", job.ID, job.Status)
	return nil
}

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	jobID := fs.String("job", "", "job id")
	stateDir := fs.String("state-dir", ".archive-jobs", "state dir")
	format := fs.String("format", "csv", "csv or json")
	output := fs.String("output", "", "output file")
	status := fs.String("status", "failed", "filter by status")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *jobID == "" {
		return errors.New("--job is required")
	}
	store := NewStore(*stateDir)
	job, err := store.LoadJob(*jobID)
	if err != nil {
		return err
	}
	var tasks []*Task
	for _, task := range sortedTasks(job.Tasks) {
		if *status != "" && string(task.Status) != *status {
			continue
		}
		tasks = append(tasks, task)
	}
	var w io.Writer = os.Stdout
	if *output != "" {
		f, err := os.Create(*output)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	switch *format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(tasks)
	case "csv":
		cw := csv.NewWriter(w)
		if err := cw.Write([]string{"fileId", "status", "sourcePath", "targetPath", "metadataSource", "errorMessage"}); err != nil {
			return err
		}
		for _, task := range tasks {
			if err := cw.Write([]string{task.FileID, string(task.Status), task.SourcePath, task.TargetPath, task.MetadataSource, task.ErrorMessage}); err != nil {
				return err
			}
		}
		cw.Flush()
		return cw.Error()
	default:
		return fmt.Errorf("unsupported format: %s", *format)
	}
}

func runJobs(args []string) error {
	fs := flag.NewFlagSet("jobs", flag.ContinueOnError)
	stateDir := fs.String("state-dir", ".archive-jobs", "state dir")
	if err := fs.Parse(args); err != nil {
		return err
	}
	store := NewStore(*stateDir)
	ids, err := store.ListJobs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		job, err := store.LoadJob(id)
		if err != nil {
			return err
		}
		fmt.Printf("%s\t%s\t%d\t%s\n", job.ID, job.Status, len(job.Tasks), job.SourcePath)
	}
	return nil
}

func processTask(ctx context.Context, events EventSink, job *Job, task *Task, dryRun bool, writeMu *sync.Mutex) error {
	_ = ctx
	now := time.Now()
	task.Status = TaskQueued
	task.UpdatedAt = now
	if err := events.Append(Event{JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskQueued), Status: "ok"}); err != nil {
		return err
	}
	task.Status = TaskMetadataRunning
	task.UpdatedAt = time.Now()
	if err := events.Append(Event{JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskMetadataRunning), Status: "ok"}); err != nil {
		return err
	}
	dt, src, err := detectDateTime(task.SourcePath, task.MTime)
	if err != nil {
		if isMissingSourceErr(err) {
			return skipMissingTask(events, job, task, err)
		}
		task.Status = TaskFailed
		task.ErrorMessage = err.Error()
		task.UpdatedAt = time.Now()
		_ = events.Append(Event{JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskFailed), Status: "error", ErrorMessage: err.Error()})
		return err
	}
	task.DateTimeOriginal = &dt
	task.MetadataSource = src
	task.Status = TaskMetadataDone
	task.UpdatedAt = time.Now()
	_ = events.Append(Event{
		JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskMetadataDone),
		Status: "ok", DateTime: dt.Format(time.RFC3339), Source: src,
	})
	target, archiveType, needsMD5, err := planTarget(job.ArchiveBase, task)
	if err != nil {
		task.Status = TaskFailed
		task.ErrorMessage = err.Error()
		task.UpdatedAt = time.Now()
		_ = events.Append(Event{JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskFailed), Status: "error", ErrorMessage: err.Error()})
		return err
	}
	task.TargetPath = target
	task.ArchiveType = archiveType
	task.Status = TaskPlanningDone
	task.UpdatedAt = time.Now()
	_ = events.Append(Event{JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskPlanningDone), Status: "ok", TargetPath: target})
	if needsMD5 {
		task.Status = TaskMD5Pending
		task.UpdatedAt = time.Now()
		_ = events.Append(Event{JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskMD5Pending), Status: "ok", TargetPath: target})
		finalTarget, md5Value, err := resolveDuplicateTarget(task.SourcePath, target)
		if err != nil {
			if isMissingSourceErr(err) {
				return skipMissingTask(events, job, task, err)
			}
			task.Status = TaskFailed
			task.ErrorMessage = err.Error()
			task.UpdatedAt = time.Now()
			_ = events.Append(Event{JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskFailed), Status: "error", ErrorMessage: err.Error()})
			return err
		}
		task.Status = TaskMD5Running
		now = time.Now()
		task.UpdatedAt = now
		task.MD5 = md5Value
		task.MD5ComputedAt = &now
		task.TargetPath = finalTarget
		_ = events.Append(Event{JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskMD5Running), Status: "ok", TargetPath: finalTarget})
	}
	if dryRun {
		task.Status = TaskSkipped
		task.UpdatedAt = time.Now()
		_ = events.Append(Event{
			JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: "dry_run_done", Status: "ok", TargetPath: task.TargetPath,
		})
		return nil
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	if task.TargetPath != "" {
		if _, err := os.Stat(task.TargetPath); err == nil {
			finalTarget, md5Value, err := resolveDuplicateTarget(task.SourcePath, task.TargetPath)
			if err != nil {
				if isMissingSourceErr(err) {
					return skipMissingTask(events, job, task, err)
				}
				task.Status = TaskFailed
				task.ErrorMessage = err.Error()
				task.UpdatedAt = time.Now()
				_ = events.Append(Event{JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskFailed), Status: "error", ErrorMessage: err.Error()})
				return err
			}
			now := time.Now()
			task.TargetPath = finalTarget
			task.MD5 = md5Value
			task.MD5ComputedAt = &now
		} else if !errors.Is(err, os.ErrNotExist) {
			task.Status = TaskFailed
			task.ErrorMessage = err.Error()
			task.UpdatedAt = time.Now()
			_ = events.Append(Event{JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskFailed), Status: "error", ErrorMessage: err.Error()})
			return err
		}
	}
	task.Status = TaskMoveQueued
	task.UpdatedAt = time.Now()
	_ = events.Append(Event{JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskMoveQueued), Status: "ok", TargetPath: task.TargetPath, QueuePosition: 1})
	task.Status = TaskMoving
	task.UpdatedAt = time.Now()
	_ = events.Append(Event{JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskMoving), Status: "ok", TargetPath: task.TargetPath})
	if err := os.MkdirAll(filepath.Dir(task.TargetPath), 0o755); err != nil {
		task.Status = TaskFailed
		task.ErrorMessage = err.Error()
		task.UpdatedAt = time.Now()
		_ = events.Append(Event{JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskFailed), Status: "error", ErrorMessage: err.Error()})
		return err
	}
	if err := moveFile(task.SourcePath, task.TargetPath); err != nil {
		if isMissingSourceErr(err) {
			return skipMissingTask(events, job, task, err)
		}
		task.Status = TaskFailed
		task.ErrorMessage = err.Error()
		task.UpdatedAt = time.Now()
		_ = events.Append(Event{JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskFailed), Status: "error", ErrorMessage: err.Error()})
		return err
	}
	task.Status = TaskCompleted
	task.UpdatedAt = time.Now()
	_ = events.Append(Event{JobID: job.ID, FileID: task.FileID, Path: task.SourcePath, Stage: string(TaskCompleted), Status: "ok", TargetPath: task.TargetPath})
	return nil
}

func skipMissingTask(events EventSink, job *Job, task *Task, err error) error {
	task.Status = TaskSkipped
	task.ErrorMessage = err.Error()
	task.UpdatedAt = time.Now()
	return events.Append(Event{
		JobID:        job.ID,
		FileID:       task.FileID,
		Path:         task.SourcePath,
		Stage:        string(TaskSkipped),
		Status:       "ok",
		ErrorMessage: err.Error(),
		TargetPath:   task.TargetPath,
	})
}

func detectDateTime(path string, mtime time.Time) (time.Time, string, error) {
	if dt, ok := parseFilenameTime(filepath.Base(path)); ok {
		return dt, "filename", nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg":
		if dt, err := extractJPEGExif(path); err == nil {
			return dt, "native-exif", nil
		}
	case ".heic":
		if dt, err := extractWithExiftool(path); err == nil {
			return dt, "exiftool", nil
		}
	case ".mp4", ".mov", ".3gp":
		if dt, err := extractWithFFProbe(path); err == nil {
			return dt, "ffprobe", nil
		}
	}
	return mtime, "mtime", nil
}

func extractJPEGExif(path string) (time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()
	x, err := exif.Decode(f)
	if err != nil {
		return time.Time{}, err
	}
	if tag, err := x.Get(exif.DateTimeOriginal); err == nil {
		raw, err := tag.StringVal()
		if err == nil {
			return time.Parse("2006:01:02 15:04:05", raw)
		}
	}
	if dt, err := x.DateTime(); err == nil {
		return dt, nil
	}
	return time.Time{}, errors.New("no exif datetime found")
}

func extractWithExiftool(path string) (time.Time, error) {
	cmd := exec.Command("/opt/bin/exiftool", "-s3", "-DateTimeOriginal", "-CreateDate", path)
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if dt, err := time.Parse("2006:01:02 15:04:05", line); err == nil {
			return dt, nil
		}
	}
	return time.Time{}, errors.New("no exiftool datetime found")
}

func extractWithFFProbe(path string) (time.Time, error) {
	for _, bin := range []string{"ffprobe", "/usr/bin/ffprobe", "ffmpeg", "/usr/bin/ffmpeg"} {
		if _, err := exec.LookPath(bin); err != nil && !strings.HasPrefix(bin, "/") {
			continue
		}
		var cmd *exec.Cmd
		if strings.Contains(filepath.Base(bin), "ffprobe") {
			cmd = exec.Command(bin, "-v", "quiet", "-show_entries", "format_tags=creation_time", "-of", "default=nw=1:nk=1", path)
		} else {
			cmd = exec.Command(bin, "-i", path)
		}
		out, err := cmd.CombinedOutput()
		if err != nil && len(out) == 0 {
			continue
		}
		text := string(out)
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "creation_time=")
			if idx := strings.Index(line, "creation_time"); idx >= 0 {
				parts := strings.Split(line, ": ")
				if len(parts) == 2 {
					line = strings.TrimSpace(parts[1])
				}
			}
			if dt, err := time.Parse(time.RFC3339Nano, line); err == nil {
				return dt, nil
			}
		}
	}
	return time.Time{}, errors.New("no video datetime found")
}

var (
	reYMDHMS1 = regexp.MustCompile(`(?i)(?:IMG|VID)[-_](\d{8})[_-](\d{6})`)
	reYMDHMS2 = regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})[ _](\d{2})\.(\d{2})\.(\d{2})`)
	reMillis  = regexp.MustCompile(`mmexport(\d{13})`)
)

func parseFilenameTime(name string) (time.Time, bool) {
	if m := reYMDHMS1.FindStringSubmatch(name); len(m) == 3 {
		dt, err := time.Parse("20060102150405", m[1]+m[2])
		return dt, err == nil
	}
	if m := reYMDHMS2.FindStringSubmatch(name); len(m) == 7 {
		dt, err := time.Parse("2006-01-02 15:04:05", fmt.Sprintf("%s-%s-%s %s:%s:%s", m[1], m[2], m[3], m[4], m[5], m[6]))
		return dt, err == nil
	}
	if m := reMillis.FindStringSubmatch(name); len(m) == 2 {
		ms, err := time.ParseDuration(m[1] + "ms")
		if err == nil {
			return time.Unix(0, ms.Nanoseconds()), true
		}
	}
	return time.Time{}, false
}

func planTarget(base string, task *Task) (string, string, bool, error) {
	if task.DateTimeOriginal == nil {
		return "", "", false, errors.New("missing datetime")
	}
	archiveType := classifyArchiveType(task.SourcePath)
	dt := task.DateTimeOriginal
	ext := strings.ToLower(filepath.Ext(task.SourcePath))
	dir := filepath.Join(base, archiveType, dt.Format("2006"), dt.Format("01"))
	name := fmt.Sprintf("%s_%d%s", dt.Format("20060102_150405"), task.Size, ext)
	target := filepath.Join(dir, name)
	_, err := os.Stat(target)
	if err == nil {
		return target, archiveType, true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", "", false, err
	}
	return target, archiveType, false, nil
}

func classifyArchiveType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".heic":
		return "photos"
	case ".png":
		return "pngs"
	case ".mp4", ".mov", ".3gp":
		return "videos"
	default:
		return "misc"
	}
}

func isVideoTask(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".mov", ".3gp":
		return true
	default:
		return false
	}
}

func resolveDuplicateTarget(source, target string) (string, string, error) {
	srcMD5, err := fileMD5(source)
	if err != nil {
		return "", "", err
	}
	dstMD5, err := fileMD5(target)
	if err != nil {
		return "", "", err
	}
	ext := filepath.Ext(target)
	base := strings.TrimSuffix(target, ext)
	if srcMD5 == dstMD5 {
		for i := 1; ; i++ {
			candidate := fmt.Sprintf("%s_%d%s", base, i, ext)
			if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
				return candidate, srcMD5, nil
			}
		}
	}
	hash8 := srcMD5[:8]
	candidate := fmt.Sprintf("%s_%s%s", base, hash8, ext)
	if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
		return candidate, srcMD5, nil
	}
	for i := 1; ; i++ {
		candidate = fmt.Sprintf("%s_%s_%d%s", base, hash8, i, ext)
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, srcMD5, nil
		}
	}
}

func fileMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func moveFile(source, target string) error {
	if err := os.Rename(source, target); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}

	tmp := target + ".tmp-" + fmt.Sprintf("%d", time.Now().UnixNano())
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	cleanupTmp := true
	defer func() {
		out.Close()
		if cleanupTmp {
			_ = os.Remove(tmp)
		}
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	cleanupTmp = false
	return os.Remove(source)
}

func sortedTasks(tasks map[string]*Task) []*Task {
	out := make([]*Task, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FileID < out[j].FileID })
	return out
}

func parseExts(raw string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, ext := range strings.Split(raw, ",") {
		ext = strings.TrimSpace(strings.ToLower(ext))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		out[ext] = struct{}{}
	}
	return out
}

func formatPtr(v *time.Time) string {
	if v == nil {
		return ""
	}
	return v.Format(time.RFC3339)
}

func hasFailed(tasks map[string]*Task) bool {
	for _, task := range tasks {
		if task.Status == TaskFailed {
			return true
		}
	}
	return false
}

func isMissingSourceErr(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

func coalesce(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
