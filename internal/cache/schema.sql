CREATE TABLE IF NOT EXISTS probes (
    path            TEXT    PRIMARY KEY,
    size            INTEGER NOT NULL,
    mtime_unix      INTEGER NOT NULL,
    container       TEXT    NOT NULL,
    video_codec     TEXT    NOT NULL,
    video_profile   TEXT    NOT NULL,
    video_level     INTEGER,
    audio_codec     TEXT    NOT NULL,
    is_target       INTEGER NOT NULL,
    probed_at_unix  INTEGER NOT NULL
) STRICT;

CREATE INDEX IF NOT EXISTS idx_probes_is_target ON probes(is_target);

CREATE TABLE IF NOT EXISTS jobs (
    path                TEXT    PRIMARY KEY,
    library             TEXT    NOT NULL,
    status              TEXT    NOT NULL,
    attempts            INTEGER NOT NULL DEFAULT 0,
    last_attempt_unix   INTEGER,
    last_error          TEXT,
    last_error_code     TEXT,
    started_at_unix     INTEGER,
    completed_at_unix   INTEGER,
    bytes_in            INTEGER,
    bytes_out           INTEGER
) STRICT;

CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);

CREATE TABLE IF NOT EXISTS job_attempts (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    path                TEXT    NOT NULL,
    attempt             INTEGER NOT NULL,
    started_at_unix     INTEGER NOT NULL,
    completed_at_unix   INTEGER,
    outcome             TEXT    NOT NULL,
    error_code          TEXT,
    error_message       TEXT,
    bytes_in            INTEGER,
    bytes_out           INTEGER,
    duration_ms         INTEGER
) STRICT;

CREATE INDEX IF NOT EXISTS idx_job_attempts_path ON job_attempts(path);
