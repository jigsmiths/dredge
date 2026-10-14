CREATE TABLE IF NOT EXISTS library (
    path         TEXT PRIMARY KEY,
    isrc         TEXT NOT NULL DEFAULT '',
    title_norm   TEXT NOT NULL DEFAULT '',
    artists_norm TEXT NOT NULL DEFAULT '',
    mtime        INTEGER NOT NULL,
    size         INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS library_isrc ON library (isrc) WHERE isrc <> '';
CREATE INDEX IF NOT EXISTS library_title ON library (title_norm);
