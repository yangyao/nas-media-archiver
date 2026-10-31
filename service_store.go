package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

type serviceStore struct {
	db           *sql.DB
	lock         *os.File
	mu           sync.Mutex
	generation   map[string]uint64
	seen         map[string]bool
	lastFlush    time.Time
	lastError    string
	lastBatch    int
	inFlight     int
	lastDuration time.Duration
	pending      map[string]storeRecord
	order        []string
	stop         chan struct{}
	done         chan struct{}
	closeOnce    sync.Once
	closeErr     error
}

func openServiceStore(dir string) (*serviceStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "service.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("service state is already in use: %w", err)
	}
	success := false
	defer func() {
		if !success {
			_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			_ = lock.Close()
		}
	}()
	db, err := sql.Open("sqlite", filepath.Join(dir, "service.sqlite"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA busy_timeout=5000", `CREATE TABLE IF NOT EXISTS service_jobs (id TEXT PRIMARY KEY, data BLOB NOT NULL)`, `CREATE TABLE IF NOT EXISTS service_files (job_id TEXT NOT NULL, id TEXT NOT NULL, data BLOB NOT NULL, PRIMARY KEY(job_id,id))`} {
		if _, err = db.Exec(statement); err != nil {
			db.Close()
			return nil, err
		}
	}
	success = true
	s := &serviceStore{db: db, lock: lock, pending: map[string]storeRecord{}, generation: map[string]uint64{}, seen: map[string]bool{}, stop: make(chan struct{}), done: make(chan struct{})}
	go s.writer()
	return s, nil
}

const serviceFlushInterval = 2 * time.Second
const serviceFlushBatch = 500

type storeRecord struct {
	jobID, fileID string
	data          []byte
	reset         bool
	summary       bool
}

// save only snapshots memory. The writer goroutine exclusively owns all SQL
// mutations; repeated updates coalesce without growing an event backlog.
func (s *serviceStore) save(j *ServiceJob, f *ServiceFile) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	var fileData []byte
	if f != nil {
		fileData, err = json.Marshal(f)
		if err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := fmt.Sprintf("%s/%d/", j.ID, s.generation[j.ID])
	if !s.seen[j.ID] {
		s.seen[j.ID] = true
		s.enqueue("initial/"+j.ID, storeRecord{jobID: j.ID, data: data})
	}
	s.enqueue("job/"+j.ID, storeRecord{jobID: j.ID, data: data, summary: true})
	if f != nil {
		s.enqueue(prefix+"file/"+f.ID, storeRecord{jobID: j.ID, fileID: f.ID, data: fileData})
	}
	return nil
}
func (s *serviceStore) enqueue(key string, r storeRecord) {
	if _, ok := s.pending[key]; !ok {
		s.order = append(s.order, key)
	}
	s.pending[key] = r
}
func (s *serviceStore) resetFiles(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Generations keep replacement rows after the reset even when a batch
	// fails while producers are updating the next generation.
	s.generation[id]++
	s.enqueue(fmt.Sprintf("%s/%d/reset", id, s.generation[id]), storeRecord{jobID: id, reset: true})
}
func (s *serviceStore) writer() {
	defer close(s.done)
	ticker := time.NewTicker(serviceFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if _, err := s.flushBatch(); err != nil {
				log.Printf("state flush failed (will retry): %v", err)
			}
		case <-s.stop:
			for {
				n, err := s.flushBatch()
				if err != nil {
					s.closeErr = err
					return
				}
				if n == 0 {
					return
				}
			}
		}
	}
}
func (s *serviceStore) flushBatch() (int, error) {
	s.mu.Lock()
	// File/reset records preserve order. Summaries are checkpointed only after
	// their file updates, so a completed preview never precedes its file list.
	keys := make([]string, 0, serviceFlushBatch)
	remaining := make([]string, 0, len(s.order))
	blocked := map[string]bool{}
	for _, key := range s.order {
		r := s.pending[key]
		if !r.summary && len(keys) < serviceFlushBatch {
			keys = append(keys, key)
		} else {
			remaining = append(remaining, key)
			if !r.summary {
				blocked[r.jobID] = true
			}
		}
	}
	deferred := remaining[:0]
	for _, key := range remaining {
		r := s.pending[key]
		if r.summary && !blocked[r.jobID] && len(keys) < serviceFlushBatch {
			keys = append(keys, key)
		} else {
			deferred = append(deferred, key)
		}
	}
	s.order = deferred
	n := len(keys)
	s.inFlight = n
	records := make([]storeRecord, n)
	for i, key := range keys {
		records[i] = s.pending[key]
		delete(s.pending, key)
	}
	s.mu.Unlock()
	if n == 0 {
		return 0, nil
	}
	started := time.Now()
	err := s.writeBatch(records)
	s.mu.Lock()
	s.inFlight = 0
	s.lastDuration = time.Since(started)
	if err != nil {
		s.lastError = err.Error()
	} else {
		s.lastError = ""
		s.lastFlush = time.Now()
		s.lastBatch = n
	}
	if err != nil {
		// Restore older records ahead of subsequent updates. A later version wins,
		// but its queue position must also move ahead of a dependent reset.
		restored := make([]string, 0, len(keys)+len(s.order))
		selected := map[string]bool{}
		for i, key := range keys {
			if _, ok := s.pending[key]; !ok {
				s.pending[key] = records[i]
			}
			restored = append(restored, key)
			selected[key] = true
		}
		for _, key := range s.order {
			if !selected[key] {
				restored = append(restored, key)
			}
		}
		s.order = restored
	}
	s.mu.Unlock()
	return n, err
}
func (s *serviceStore) writeBatch(records []storeRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range records {
		if r.reset {
			_, err = tx.Exec(`DELETE FROM service_files WHERE job_id=?`, r.jobID)
		} else if r.fileID != "" {
			_, err = tx.Exec(`INSERT INTO service_files(job_id,id,data) VALUES(?,?,?) ON CONFLICT(job_id,id) DO UPDATE SET data=excluded.data`, r.jobID, r.fileID, r.data)
		} else {
			_, err = tx.Exec(`INSERT INTO service_jobs(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, r.jobID, r.data)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *serviceStore) status() map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]interface{}{"pendingRecords": len(s.pending) + s.inFlight, "flushIntervalSeconds": serviceFlushInterval.Seconds(), "batchLimit": serviceFlushBatch, "lastFlush": s.lastFlush, "lastBatchRecords": s.lastBatch, "lastFlushMilliseconds": s.lastDuration.Milliseconds(), "error": s.lastError}
}

func (s *serviceStore) load() (map[string]*ServiceJob, error) {
	rows, err := s.db.Query(`SELECT data FROM service_jobs`)
	if err != nil {
		return nil, err
	}
	jobs := map[string]*ServiceJob{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return nil, err
		}
		var j ServiceJob
		if err = json.Unmarshal(b, &j); err != nil {
			rows.Close()
			return nil, fmt.Errorf("invalid stored job: %w", err)
		}
		j.Files = []*ServiceFile{}
		if j.ResidualPaths == nil {
			j.ResidualPaths = []string{}
		}
		jobs[j.ID] = &j
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT job_id,data FROM service_files ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var b []byte
		if err = rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		var f ServiceFile
		if err = json.Unmarshal(b, &f); err != nil {
			return nil, err
		}
		if j := jobs[id]; j != nil {
			j.Files = append(j.Files, &f)
		}
	}
	return jobs, rows.Err()
}

func (s *serviceStore) Close() error {
	s.closeOnce.Do(func() {
		close(s.stop)
		<-s.done
		if err := s.db.Close(); s.closeErr == nil {
			s.closeErr = err
		}
		if err := syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN); s.closeErr == nil {
			s.closeErr = err
		}
		if err := s.lock.Close(); s.closeErr == nil {
			s.closeErr = err
		}
	})
	return s.closeErr
}
