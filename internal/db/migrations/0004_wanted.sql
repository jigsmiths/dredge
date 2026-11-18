CREATE TABLE IF NOT EXISTS wanted (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    key         TEXT NOT NULL UNIQUE, -- the Spotify track id, or title and artists
    title       TEXT NOT NULL,
    artists     TEXT NOT NULL DEFAULT '',
    album       TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    source_id   TEXT NOT NULL DEFAULT '',
    reason      TEXT NOT NULL DEFAULT '',
    added_at    INTEGER NOT NULL,
    checked_at  INTEGER
);
