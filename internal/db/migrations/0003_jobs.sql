CREATE TABLE IF NOT EXISTS jobs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    url         TEXT NOT NULL,
    kind        TEXT NOT NULL DEFAULT '',
    name        TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL,
    error       TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    started_at  INTEGER,
    finished_at INTEGER
);

CREATE INDEX IF NOT EXISTS jobs_status ON jobs (status);

CREATE TABLE IF NOT EXISTS job_tracks (
    job_id  INTEGER NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    pos     INTEGER NOT NULL,
    title   TEXT NOT NULL,
    artists TEXT NOT NULL DEFAULT '',
    album   TEXT NOT NULL DEFAULT '',
    isrc    TEXT NOT NULL DEFAULT '',
    status  TEXT NOT NULL,
    path    TEXT NOT NULL DEFAULT '',
    bytes   INTEGER NOT NULL DEFAULT 0,
    reason  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (job_id, pos)
);
