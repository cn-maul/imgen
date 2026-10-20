-- Imgen 初始 schema
-- 所有时间戳统一存 RFC3339 文本（UTC）。

CREATE TABLE IF NOT EXISTS providers (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    base_url    TEXT NOT NULL,
    api_key_enc BLOB,
    enabled     INTEGER NOT NULL DEFAULT 1,
    timeout_ms  INTEGER NOT NULL DEFAULT 180000,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS models (
    provider_id  TEXT NOT NULL,
    model_id     TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (provider_id, model_id),
    FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS jobs (
    id          TEXT PRIMARY KEY,
    provider    TEXT NOT NULL,
    model       TEXT NOT NULL,
    mode        TEXT NOT NULL,
    prompt      TEXT NOT NULL,
    params_json TEXT NOT NULL,
    status      TEXT NOT NULL,
    progress    INTEGER NOT NULL DEFAULT 0,
    count       INTEGER NOT NULL DEFAULT 1,
    latency_ms  INTEGER NOT NULL DEFAULT 0,
    error_json  TEXT,
    created_at  TEXT NOT NULL,
    started_at  TEXT,
    finished_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_jobs_status   ON jobs(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_jobs_provider ON jobs(provider, model, created_at DESC);

CREATE TABLE IF NOT EXISTS job_images (
    job_id   TEXT NOT NULL,
    seq      INTEGER NOT NULL,
    asset_id TEXT,
    status   TEXT NOT NULL,
    error_json TEXT,
    PRIMARY KEY (job_id, seq),
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS assets (
    id         TEXT PRIMARY KEY,
    sha256     TEXT NOT NULL UNIQUE,
    mime       TEXT NOT NULL,
    ext        TEXT NOT NULL,
    width      INTEGER NOT NULL DEFAULT 0,
    height     INTEGER NOT NULL DEFAULT 0,
    bytes      INTEGER NOT NULL DEFAULT 0,
    path       TEXT NOT NULL,
    source     TEXT NOT NULL,
    source_url TEXT,
    expires_at TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_assets_created ON assets(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_assets_sha     ON assets(sha256);

CREATE TABLE IF NOT EXISTS usage_daily (
    day      TEXT NOT NULL,
    provider TEXT NOT NULL,
    model    TEXT NOT NULL,
    calls    INTEGER NOT NULL DEFAULT 0,
    ok       INTEGER NOT NULL DEFAULT 0,
    failed   INTEGER NOT NULL DEFAULT 0,
    images   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, provider, model)
);

CREATE TABLE IF NOT EXISTS kv (
    key        TEXT PRIMARY KEY,
    value_json TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- 内置通道定义。API Key 留空，由用户在 UI 配置后写入。
INSERT OR IGNORE INTO providers (id, name, base_url, enabled, created_at, updated_at)
VALUES ('sensenova', '商汤日日新 SenseNova', 'https://token.sensenova.cn/v1', 1, '', ''),
       ('agnes',     'Agnes AI',             'https://apihub.agnes-ai.cn/v1',  1, '', '');

INSERT OR IGNORE INTO models (provider_id, model_id, display_name) VALUES
    ('sensenova', 'sensenova-u1.5-lite', 'SenseNova U1.5 Lite（质量优先）'),
    ('sensenova', 'sensenova-u1.5-fast', 'SenseNova U1.5 Fast（速度优先）'),
    ('agnes',     'agnes-image-2.5-flash', 'Agnes Image 2.5 Flash'),
    ('agnes',     'agnes-image-2.1-flash', 'Agnes Image 2.1 Flash'),
    ('agnes',     'agnes-image-2.0-flash', 'Agnes Image 2.0 Flash');
