-- 提示词预设：图生图/多图合成的工作流模板。
--
-- body_json 存模板自身的字段（提示词文本、引用的资产 id、尺寸档位等），
-- 刻意与 jobs.params_json 分开：预设是可复用的草稿，job 是不可变的历史快照。
CREATE TABLE IF NOT EXISTS prompt_presets (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    category    TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    body_json   TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_prompt_presets_updated
    ON prompt_presets(updated_at DESC);
