package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func waitStore(t *testing.T, s *serviceStore, predicate func(map[string]interface{}) bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if predicate(s.status()) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("persistence did not converge: %+v", s.status())
}
func TestServiceFixedTickBoundedBatchAndCoalescing(t *testing.T) {
	s, err := openServiceStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j := &ServiceJob{ID: "batch", Status: "ready", ScanComplete: true, TotalFiles: 1200}
	for i := 0; i < 1200; i++ {
		f := &ServiceFile{ID: fmt.Sprintf("file-%06d", i), Status: "discovered"}
		if err = s.save(j, f); err != nil {
			t.Fatal(err)
		}
		f.Status = "planned"
		if err = s.save(j, f); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM service_files`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("producer wrote SQL before tick: %d %v", count, err)
	}
	waitStore(t, s, func(v map[string]interface{}) bool { return v["lastBatchRecords"] == serviceFlushBatch })
	if err = s.db.QueryRow(`SELECT count(*) FROM service_files`).Scan(&count); err != nil || count != 499 {
		t.Fatalf("first batch exceeded cap: %d %v", count, err)
	}
	var jobs int
	s.db.QueryRow(`SELECT count(*) FROM service_jobs`).Scan(&jobs)
	if jobs != 1 {
		t.Fatal("partial checkpoint lacks initial job")
	}
	var data []byte
	s.db.QueryRow(`SELECT data FROM service_jobs WHERE id='batch'`).Scan(&data)
	// The initial record permits partial checkpoints to remain inspectable.
	var initial ServiceJob
	json.Unmarshal(data, &initial)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := openServiceStore(filepath.Dir(s.lock.Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	loaded, err := next.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded[j.ID].Files) != 1200 {
		t.Fatalf("shutdown lost pending files: %d", len(loaded[j.ID].Files))
	}
	for _, f := range loaded[j.ID].Files {
		if f.Status != "planned" {
			t.Fatal("coalescing persisted obsolete version")
		}
	}
}
func TestServiceFailedFlushRetriesLatestAndResetOrdering(t *testing.T) {
	s, err := openServiceStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.db.Exec(`CREATE TRIGGER fail_files BEFORE INSERT ON service_files BEGIN SELECT RAISE(ABORT,'test disk failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	j := &ServiceJob{ID: "retry"}
	s.save(j, &ServiceFile{ID: "old", Status: "discovered"})
	waitStore(t, s, func(v map[string]interface{}) bool { return v["error"] != "" })
	s.save(j, &ServiceFile{ID: "old", Status: "planned"})
	s.resetFiles(j.ID)
	s.save(j, &ServiceFile{ID: "new", Status: "planned"})
	if _, err = s.db.Exec(`DROP TRIGGER fail_files`); err != nil {
		t.Fatal(err)
	}
	waitStore(t, s, func(v map[string]interface{}) bool { return v["error"] == "" && v["pendingRecords"] == 0 })
	loaded, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	if files := loaded[j.ID].Files; len(files) != 1 || files[0].ID != "new" {
		t.Fatalf("reset reordered on retry: %+v", files)
	}
}
func TestServiceIncrementalCountersMatchRebuild(t *testing.T) {
	j := &ServiceJob{}
	states := []string{"discovered", "planned", "copying", "failed", "unsupported", "completed"}
	for i := 0; i < 100; i++ {
		f := &ServiceFile{ID: fmt.Sprint(i), Size: int64(i + 1), Status: states[i%len(states)], DateTime: time.Date(2026, 1, 1+i%28, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)}
		j.Files = append(j.Files, f)
		j.updateFile(f)
	}
	check := func() {
		want := *j
		want.recalculate()
		if !reflect.DeepEqual(serviceJobSnapshot(j), serviceJobSnapshot(&want)) {
			t.Fatalf("incremental counters differ:\n%+v\n%+v", serviceJobSnapshot(j), serviceJobSnapshot(&want))
		}
	}
	check()
	for i, f := range j.Files {
		f.Status = states[(i+3)%len(states)]
		f.Size += 9
		f.Warning = "attention"
		j.updateFile(f)
		check()
	}
	j.Files[0].DateTime = "2025-01-01T00:00:00Z"
	j.updateFile(j.Files[0])
	check()
	j.Files[0].DateTime = ""
	j.updateFile(j.Files[0])
	check()
}
func TestServicePersistentExifTool(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "exiftool")
	starts := filepath.Join(dir, "starts")
	script := "#!/bin/sh\nprintf 'start\\n' >> '" + starts + "'\nwhile IFS= read -r arg; do\n if [ \"$arg\" = '-execute' ]; then\n printf '2026:07:01 12:00:00\\n{ready}\\n'\n fi\ndone\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	r := &serviceMetadataReader{executable: bin}
	defer r.Close()
	for i := 0; i < 3; i++ {
		dt, err := r.exif("/tmp/photo with space.heic")
		if err != nil || dt.Format("20060102") != "20260701" {
			t.Fatalf("persistent response: %v %v", dt, err)
		}
	}
	data, err := os.ReadFile(starts)
	if err != nil || string(data) != "start\n" {
		t.Fatalf("ExifTool restarted per file: %q %v", data, err)
	}
}
func TestServiceRescanReusesMetadataAndRechecksChangedSource(t *testing.T) {
	s, _, source, target := serviceFixture(t)
	path := filepath.Join(source, "capture.jpg")
	serviceWrite(t, path, []byte("one"))
	job, err := s.createJob([]string{source}, target, false)
	if err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, job.ID, "ready")
	s.mu.Lock()
	j := s.jobs[job.ID]
	f := j.Files[0]
	f.DateTime = "2020-01-01T00:00:00Z"
	f.MetadataSource = "cached-test"
	j.ScanComplete = false
	j.Status = "interrupted"
	s.changed(j, f)
	s.mu.Unlock()
	if _, err = s.jobAction(job.ID, "retry"); err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, job.ID, "ready")
	s.mu.Lock()
	got := *s.jobs[job.ID].Files[0]
	s.mu.Unlock()
	if got.MetadataSource != "cached-test" {
		t.Fatal("unchanged source metadata was parsed again")
	}
	serviceWrite(t, path, []byte("changed size"))
	s.mu.Lock()
	j.ScanComplete = false
	j.Status = "interrupted"
	s.mu.Unlock()
	if _, err = s.jobAction(job.ID, "retry"); err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, job.ID, "ready")
	s.mu.Lock()
	got = *s.jobs[job.ID].Files[0]
	s.mu.Unlock()
	if got.MetadataSource == "cached-test" || got.Size != 12 {
		t.Fatalf("stale metadata reused: %+v", got)
	}
}
func BenchmarkServiceScanState(b *testing.B) {
	for _, count := range []int{1000, 8000} {
		for _, incremental := range []bool{false, true} {
			mode := "rebuild"
			if incremental {
				mode = "incremental"
			}
			b.Run(fmt.Sprintf("%s/%d", mode, count), func(b *testing.B) {
				for n := 0; n < b.N; n++ {
					j := &ServiceJob{}
					for i := 0; i < count; i++ {
						f := &ServiceFile{ID: fmt.Sprint(i), Size: 1024, Status: "planned", DateTime: "2026-01-01T00:00:00Z"}
						j.Files = append(j.Files, f)
						if incremental {
							j.updateFile(f)
						} else {
							j.referenceRecalculate()
						}
					}
				}
			})
		}
	}
}

// Original implementation retained only as a benchmark reference.
func (j *ServiceJob) referenceRecalculate() {
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

// A crash may leave a planned checkpoint after a fully verified move. The
// stale journal must not fabricate success or consume a pre-existing target.
func TestServiceStaleCheckpointAfterSourceRemoval(t *testing.T) {
	s, c, source, target := serviceFixture(t)
	payload := []byte("verified original")
	path := filepath.Join(source, "IMG_20260701_120000.jpg")
	serviceWrite(t, path, payload)
	job, err := s.createJob([]string{source}, target, false)
	if err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, job.ID, "ready")
	s.mu.Lock()
	staleJob := serviceJobSnapshot(s.jobs[job.ID])
	staleFile := *s.jobs[job.ID].Files[0]
	s.mu.Unlock()
	if _, err = s.jobAction(job.ID, "start"); err != nil {
		t.Fatal(err)
	}
	serviceWait(t, s, job.ID, "completed")
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	staleJob.Status = "running"
	db, err := sql.Open("sqlite", filepath.Join(c.StateDir, "service.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	jb, _ := json.Marshal(staleJob)
	fb, _ := json.Marshal(staleFile)
	if _, err = db.Exec(`UPDATE service_jobs SET data=? WHERE id=?`, jb, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE service_files SET data=? WHERE job_id=? AND id=?`, fb, job.ID, staleFile.ID); err != nil {
		t.Fatal(err)
	}
	db.Close()
	next, err := newArchiveService(c)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, err = next.jobAction(job.ID, "start"); err != nil {
		t.Fatal(err)
	}
	result := serviceWait(t, next, job.ID, "completed_with_errors", "failed")
	if result.FailedFiles != 1 {
		t.Fatalf("stale state fabricated completion: %+v", result)
	}
	if got, err := os.ReadFile(staleFile.TargetPath); err != nil || string(got) != string(payload) {
		t.Fatalf("verified archive lost: %q %v", got, err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("stale recovery recreated source")
	}
}
