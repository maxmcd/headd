PRAGMA strict_tables = ON;

CREATE TABLE hosts (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('online', 'offline')) DEFAULT 'offline',
    created_at TEXT NOT NULL DEFAULT (datetime('now')) CHECK (datetime(created_at) IS NOT NULL)
) STRICT;

CREATE TABLE builds (
    id TEXT PRIMARY KEY NOT NULL,
    host_id TEXT NOT NULL,
    command TEXT NOT NULL,
    args TEXT NOT NULL CHECK (json_valid(args)), -- Stored as JSON array
    env TEXT NOT NULL CHECK (json_valid(env)),   -- Stored as JSON object
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    output TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now')) CHECK (datetime(created_at) IS NOT NULL),
    started_at TEXT CHECK (started_at IS NULL OR datetime(started_at) IS NOT NULL),
    completed_at TEXT CHECK (completed_at IS NULL OR datetime(completed_at) IS NOT NULL),
    FOREIGN KEY (host_id) REFERENCES hosts(id)
) STRICT;

-- Indexes for common queries
CREATE INDEX idx_builds_host_id ON builds(host_id);
CREATE INDEX idx_builds_status ON builds(status);