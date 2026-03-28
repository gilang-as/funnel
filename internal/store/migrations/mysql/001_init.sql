CREATE TABLE IF NOT EXISTS jobs (
    id           VARCHAR(64)   NOT NULL PRIMARY KEY,
    magnet       TEXT          NOT NULL,
    info_hash    VARCHAR(64)   NOT NULL UNIQUE,
    status       VARCHAR(32)   NOT NULL DEFAULT 'queued',
    worker_id    VARCHAR(64),
    name         VARCHAR(512),
    size         BIGINT        NOT NULL DEFAULT 0,
    progress     DOUBLE        NOT NULL DEFAULT 0,
    error_msg    TEXT,
    paused       TINYINT(1)    NOT NULL DEFAULT 0,
    created_at   DATETIME      NOT NULL,
    updated_at   DATETIME      NOT NULL,
    started_at   DATETIME,
    completed_at DATETIME
);

CREATE TABLE IF NOT EXISTS workers (
    id           VARCHAR(64)   NOT NULL PRIMARY KEY,
    address      VARCHAR(256)  NOT NULL,
    capacity     INT           NOT NULL DEFAULT 1,
    active_jobs  INT           NOT NULL DEFAULT 0,
    status       VARCHAR(32)   NOT NULL DEFAULT 'active',
    version      VARCHAR(64),
    last_seen    DATETIME      NOT NULL,
    joined_at    DATETIME      NOT NULL
);

CREATE TABLE IF NOT EXISTS join_tokens (
    id           VARCHAR(64)   NOT NULL PRIMARY KEY,
    token_hash   VARCHAR(128)  NOT NULL UNIQUE,
    name         VARCHAR(128),
    created_at   DATETIME      NOT NULL,
    expires_at   DATETIME,
    revoked      TINYINT(1)    NOT NULL DEFAULT 0
);

-- Used by standalone DB state (mirrors daemon.SavedTorrent)
CREATE TABLE IF NOT EXISTS state_torrents (
    id           VARCHAR(64)   NOT NULL PRIMARY KEY,
    magnet       TEXT          NOT NULL,
    name         VARCHAR(512),
    paused       TINYINT(1)    NOT NULL DEFAULT 0
);
