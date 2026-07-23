-- User-created plugins (0.3.60 Labs sandbox).
-- Stores user-defined plugin definitions that extend the built-in
-- experimental catalog. Each row maps to a dynamic Flag merged into
-- the Registry at boot. The flag_key is always "user_<slug>".
CREATE TABLE IF NOT EXISTS user_plugin (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug TEXT NOT NULL UNIQUE,
    flag_key TEXT NOT NULL UNIQUE,
    title_en TEXT NOT NULL DEFAULT '',
    title_zh TEXT NOT NULL DEFAULT '',
    description_en TEXT NOT NULL DEFAULT '',
    description_zh TEXT NOT NULL DEFAULT '',
    manifest_json JSONB NOT NULL DEFAULT '{}',
    trigger_mode TEXT NOT NULL DEFAULT 'issue_select'
        CHECK (trigger_mode IN ('auto', 'issue_select')),
    runtime_kind TEXT NOT NULL DEFAULT 'inline'
        CHECK (runtime_kind IN ('none', 'inline', 'subprocess')),
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'disabled', 'deleted')),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_user_plugin_status ON user_plugin (status);
