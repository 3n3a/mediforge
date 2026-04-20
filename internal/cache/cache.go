package cache

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

const (
	StatusActive    = "active"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusPermanent = "failed_permanent"
)

type Cache struct {
	db *sql.DB
}

type ProbeEntry struct {
	Path         string
	Size         int64
	MtimeUnix    int64
	Container    string
	VideoCodec   string
	VideoProfile string
	VideoLevel   int
	AudioCodec   string
	IsTarget     bool
	ProbedAtUnix int64
}

type Job struct {
	Path            string
	Library         string
	Status          string
	Attempts        int
	LastAttemptUnix int64
	LastError       string
	LastErrorCode   string
	StartedAtUnix   int64
	CompletedAtUnix int64
	BytesIn         int64
	BytesOut        int64
}

type Stats struct {
	ProbeRows     int
	ProbeTargets  int
	JobsActive    int
	JobsDone      int
	JobsFailed    int
	JobsPermanent int
}

func Open(path string) (*Cache, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single connection-friendly: SQLite serializes writes; leave pool defaults.
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Cache{db: db}, nil
}

func (c *Cache) Close() error { return c.db.Close() }

// ---------- Probe cache ----------

func (c *Cache) GetProbe(path string) (ProbeEntry, bool, error) {
	row := c.db.QueryRow(`SELECT path,size,mtime_unix,container,video_codec,video_profile,video_level,audio_codec,is_target,probed_at_unix FROM probes WHERE path = ?`, path)
	var e ProbeEntry
	var level sql.NullInt64
	var isTarget int
	err := row.Scan(&e.Path, &e.Size, &e.MtimeUnix, &e.Container, &e.VideoCodec, &e.VideoProfile, &level, &e.AudioCodec, &isTarget, &e.ProbedAtUnix)
	if errors.Is(err, sql.ErrNoRows) {
		return ProbeEntry{}, false, nil
	}
	if err != nil {
		return ProbeEntry{}, false, err
	}
	if level.Valid {
		e.VideoLevel = int(level.Int64)
	}
	e.IsTarget = isTarget != 0
	return e, true, nil
}

func (c *Cache) PutProbe(e ProbeEntry) error {
	var level any
	if e.VideoLevel != 0 {
		level = e.VideoLevel
	}
	_, err := c.db.Exec(`
		INSERT INTO probes (path,size,mtime_unix,container,video_codec,video_profile,video_level,audio_codec,is_target,probed_at_unix)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET
		  size=excluded.size,
		  mtime_unix=excluded.mtime_unix,
		  container=excluded.container,
		  video_codec=excluded.video_codec,
		  video_profile=excluded.video_profile,
		  video_level=excluded.video_level,
		  audio_codec=excluded.audio_codec,
		  is_target=excluded.is_target,
		  probed_at_unix=excluded.probed_at_unix
	`, e.Path, e.Size, e.MtimeUnix, e.Container, e.VideoCodec, e.VideoProfile, level, e.AudioCodec, boolInt(e.IsTarget), e.ProbedAtUnix)
	return err
}

func (c *Cache) DeleteProbe(path string) error {
	_, err := c.db.Exec(`DELETE FROM probes WHERE path = ?`, path)
	return err
}

// ---------- Jobs ----------

func (c *Cache) GetJob(path string) (Job, bool, error) {
	row := c.db.QueryRow(`SELECT path,library,status,attempts,
		COALESCE(last_attempt_unix,0),
		COALESCE(last_error,''),
		COALESCE(last_error_code,''),
		COALESCE(started_at_unix,0),
		COALESCE(completed_at_unix,0),
		COALESCE(bytes_in,0),
		COALESCE(bytes_out,0)
		FROM jobs WHERE path = ?`, path)
	var j Job
	err := row.Scan(&j.Path, &j.Library, &j.Status, &j.Attempts, &j.LastAttemptUnix, &j.LastError, &j.LastErrorCode, &j.StartedAtUnix, &j.CompletedAtUnix, &j.BytesIn, &j.BytesOut)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	return j, true, nil
}

// StartAttempt sets status='active', increments attempts, and returns the updated Job.
// Creates the row if missing.
func (c *Cache) StartAttempt(path, library string) (Job, error) {
	now := time.Now().Unix()
	tx, err := c.db.Begin()
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO jobs (path,library,status,attempts,last_attempt_unix,started_at_unix)
		VALUES (?,?,?,1,?,?)
		ON CONFLICT(path) DO UPDATE SET
		  library=excluded.library,
		  status='active',
		  attempts=jobs.attempts + 1,
		  last_attempt_unix=excluded.last_attempt_unix,
		  started_at_unix=excluded.started_at_unix,
		  completed_at_unix=NULL,
		  last_error=NULL,
		  last_error_code=NULL
	`, path, library, StatusActive, now, now)
	if err != nil {
		return Job{}, err
	}

	var j Job
	row := tx.QueryRow(`SELECT path,library,status,attempts,
		COALESCE(last_attempt_unix,0),
		COALESCE(last_error,''),
		COALESCE(last_error_code,''),
		COALESCE(started_at_unix,0),
		COALESCE(completed_at_unix,0),
		COALESCE(bytes_in,0),
		COALESCE(bytes_out,0)
		FROM jobs WHERE path = ?`, path)
	if err := row.Scan(&j.Path, &j.Library, &j.Status, &j.Attempts, &j.LastAttemptUnix, &j.LastError, &j.LastErrorCode, &j.StartedAtUnix, &j.CompletedAtUnix, &j.BytesIn, &j.BytesOut); err != nil {
		return Job{}, err
	}

	_, err = tx.Exec(`INSERT INTO job_attempts (path,attempt,started_at_unix,outcome) VALUES (?,?,?,'active')`, path, j.Attempts, now)
	if err != nil {
		return Job{}, err
	}

	return j, tx.Commit()
}

// CompleteAttempt marks the job done and records a successful attempt record.
func (c *Cache) CompleteAttempt(path string, bytesIn, bytesOut int64) error {
	now := time.Now().Unix()
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var attempts int
	var startedAt int64
	if err := tx.QueryRow(`SELECT attempts, COALESCE(started_at_unix,0) FROM jobs WHERE path = ?`, path).Scan(&attempts, &startedAt); err != nil {
		return err
	}

	_, err = tx.Exec(`UPDATE jobs SET status=?, completed_at_unix=?, bytes_in=?, bytes_out=?, last_error=NULL, last_error_code=NULL WHERE path=?`,
		StatusDone, now, bytesIn, bytesOut, path)
	if err != nil {
		return err
	}

	durMs := int64(0)
	if startedAt > 0 {
		durMs = (now - startedAt) * 1000
	}
	_, err = tx.Exec(`UPDATE job_attempts SET completed_at_unix=?, outcome='success', bytes_in=?, bytes_out=?, duration_ms=? WHERE path=? AND attempt=?`,
		now, bytesIn, bytesOut, durMs, path, attempts)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// FailAttempt records an error and flips status to 'failed' or 'failed_permanent'.
func (c *Cache) FailAttempt(path, errCode, errMsg string, maxRetries int) error {
	now := time.Now().Unix()
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var attempts int
	var startedAt int64
	if err := tx.QueryRow(`SELECT attempts, COALESCE(started_at_unix,0) FROM jobs WHERE path = ?`, path).Scan(&attempts, &startedAt); err != nil {
		return err
	}

	status := StatusFailed
	if attempts >= maxRetries {
		status = StatusPermanent
	}

	_, err = tx.Exec(`UPDATE jobs SET status=?, last_error=?, last_error_code=?, completed_at_unix=? WHERE path=?`,
		status, errMsg, errCode, now, path)
	if err != nil {
		return err
	}

	durMs := int64(0)
	if startedAt > 0 {
		durMs = (now - startedAt) * 1000
	}
	_, err = tx.Exec(`UPDATE job_attempts SET completed_at_unix=?, outcome='error', error_code=?, error_message=?, duration_ms=? WHERE path=? AND attempt=?`,
		now, errCode, errMsg, durMs, path, attempts)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// ResetJob clears the permanent-failure flag for a path and resets attempts to 0.
func (c *Cache) ResetJob(path string) (bool, error) {
	res, err := c.db.Exec(`UPDATE jobs SET status='failed', attempts=0, last_error=NULL, last_error_code=NULL, completed_at_unix=NULL WHERE path=?`, path)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (c *Cache) DeleteJob(path string) error {
	_, err := c.db.Exec(`DELETE FROM jobs WHERE path=?`, path)
	return err
}

// SweepStaleActive reclaims jobs left in 'active' status by a crashed prior run.
func (c *Cache) SweepStaleActive() (int64, error) {
	res, err := c.db.Exec(`UPDATE jobs SET status='failed', last_error='stale active job reclaimed', last_error_code='stale_active', completed_at_unix=? WHERE status='active'`, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (c *Cache) ListJobs(status string) ([]Job, error) {
	var rows *sql.Rows
	var err error
	q := `SELECT path,library,status,attempts,
		COALESCE(last_attempt_unix,0),
		COALESCE(last_error,''),
		COALESCE(last_error_code,''),
		COALESCE(started_at_unix,0),
		COALESCE(completed_at_unix,0),
		COALESCE(bytes_in,0),
		COALESCE(bytes_out,0)
		FROM jobs`
	if status != "" {
		q += ` WHERE status = ?`
		rows, err = c.db.Query(q+` ORDER BY last_attempt_unix DESC`, status)
	} else {
		rows, err = c.db.Query(q + ` ORDER BY last_attempt_unix DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.Path, &j.Library, &j.Status, &j.Attempts, &j.LastAttemptUnix, &j.LastError, &j.LastErrorCode, &j.StartedAtUnix, &j.CompletedAtUnix, &j.BytesIn, &j.BytesOut); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (c *Cache) Stats() (Stats, error) {
	var s Stats
	if err := c.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(is_target),0) FROM probes`).Scan(&s.ProbeRows, &s.ProbeTargets); err != nil {
		return s, err
	}
	rows, err := c.db.Query(`SELECT status, COUNT(*) FROM jobs GROUP BY status`)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return s, err
		}
		switch status {
		case StatusActive:
			s.JobsActive = n
		case StatusDone:
			s.JobsDone = n
		case StatusFailed:
			s.JobsFailed = n
		case StatusPermanent:
			s.JobsPermanent = n
		}
	}
	return s, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
