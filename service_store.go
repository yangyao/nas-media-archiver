package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	_ "modernc.org/sqlite"
)

type serviceStore struct {
	db   *sql.DB
	lock *os.File
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
	return &serviceStore{db: db, lock: lock}, nil
}
func (s *serviceStore) save(j *ServiceJob, f *ServiceFile) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO service_jobs(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, j.ID, data); err != nil {
		return err
	}
	if f != nil {
		b, e := json.Marshal(f)
		if e != nil {
			return e
		}
		if _, err = tx.Exec(`INSERT INTO service_files(job_id,id,data) VALUES(?,?,?) ON CONFLICT(job_id,id) DO UPDATE SET data=excluded.data`, j.ID, f.ID, b); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	err := s.db.Close()
	unlockErr := syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	closeErr := s.lock.Close()
	if err != nil {
		return err
	}
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
