CREATE TABLE IF NOT EXISTS jobs (
    id           VARCHAR(64)      NOT NULL PRIMARY KEY,
    magnet       TEXT             NOT NULL,
    info_hash    VARCHAR(64)      NOT NULL UNIQUE,
    status       VARCHAR(32)      NOT NULL DEFAULT 'queued',
    worker_id    VARCHAR(64),
    name         VARCHAR(512),
    size         BIGINT           NOT NULL DEFAULT 0,
    progress     DOUBLE PRECISION NOT NULL DEFAULT 0,
    error_msg    TEXT,
    paused       BOOLEAN          NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ      NOT NULL,
    updated_at   TIMESTAMPTZ      NOT NULL,
    started_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS workers (
    id           VARCHAR(64)   NOT NULL PRIMARY KEY,
    address      VARCHAR(256)  NOT NULL,
    capacity     INT           NOT NULL DEFAULT 1,
    active_jobs  INT           NOT NULL DEFAULT 0,
    status       VARCHAR(32)   NOT NULL DEFAULT 'active',
    version      VARCHAR(64),
    last_seen    TIMESTAMPTZ   NOT NULL,
    joined_at    TIMESTAMPTZ   NOT NULL
);

CREATE TABLE IF NOT EXISTS join_tokens (
    id           VARCHAR(64)   NOT NULL PRIMARY KEY,
    token_hash   VARCHAR(128)  NOT NULL UNIQUE,
    name         VARCHAR(128),
    created_at   TIMESTAMPTZ   NOT NULL,
    expires_at   TIMESTAMPTZ,
    revoked      BOOLEAN       NOT NULL DEFAULT FALSE
);

-- Used by standalone DB state (mirrors daemon.SavedTorrent)
CREATE TABLE IF NOT EXISTS state_torrents (
    id           VARCHAR(64)   NOT NULL PRIMARY KEY,
    magnet       TEXT          NOT NULL,
    name         VARCHAR(512),
    paused       BOOLEAN       NOT NULL DEFAULT FALSE
);
