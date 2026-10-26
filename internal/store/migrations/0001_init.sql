CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    uid TEXT NOT NULL UNIQUE,
    username TEXT NOT NULL COLLATE NOCASE UNIQUE,
    display_name TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    is_active INTEGER NOT NULL DEFAULT 1,
    telegram_user_id INTEGER,
    must_change_password INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX idx_users_telegram ON users(telegram_user_id) WHERE telegram_user_id IS NOT NULL;
CREATE INDEX idx_users_active ON users(is_active);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_secret TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT,
    ip TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

CREATE TABLE api_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    uid TEXT NOT NULL UNIQUE,
    token_hash TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    last4 TEXT NOT NULL,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    last_used_at TEXT,
    expires_at TEXT,
    revoked_at TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_api_tokens_user ON api_tokens(user_id);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value_json TEXT NOT NULL,
    updated_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE reminder_configs (
    kind TEXT PRIMARY KEY CHECK (kind IN ('transactions', 'snapshot')),
    cron_expr TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 0,
    timezone TEXT NOT NULL DEFAULT 'Europe/Kyiv',
    window_days INTEGER NOT NULL DEFAULT 7,
    target_chat_id INTEGER,
    target_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    last_run_at TEXT,
    last_sent_at TEXT,
    updated_at TEXT NOT NULL
);

CREATE TABLE categories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    uid TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL COLLATE NOCASE,
    kind TEXT NOT NULL CHECK (kind IN ('income', 'expense')),
    color TEXT NOT NULL DEFAULT '#64748b',
    emoji TEXT NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0,
    archived_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(name, kind),
    UNIQUE(id, kind)
);
CREATE INDEX idx_categories_kind_sort ON categories(kind, sort_order);

CREATE TABLE category_aliases (
    alias_norm TEXT PRIMARY KEY,
    alias_text TEXT NOT NULL,
    category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE
);
CREATE INDEX idx_category_aliases_category ON category_aliases(category_id);

CREATE TABLE accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    uid TEXT NOT NULL UNIQUE,
    owner_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    name TEXT NOT NULL COLLATE NOCASE,
    currency TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('bank', 'cash', 'investment', 'other')),
    include_in_net_worth INTEGER NOT NULL DEFAULT 1,
    sort_order INTEGER NOT NULL DEFAULT 0,
    archived_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(owner_user_id, name)
);
CREATE INDEX idx_accounts_owner ON accounts(owner_user_id, sort_order);

CREATE TABLE account_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    uid TEXT NOT NULL UNIQUE,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    snapshot_date TEXT NOT NULL,
    balance_minor INTEGER NOT NULL CHECK (balance_minor >= 0),
    note TEXT NOT NULL DEFAULT '',
    created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    updated_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT,
    deleted_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX idx_snapshots_account_date_active ON account_snapshots(account_id, snapshot_date) WHERE deleted_at IS NULL;
CREATE INDEX idx_snapshots_account_date ON account_snapshots(account_id, snapshot_date);

CREATE TABLE transactions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    uid TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind IN ('income', 'expense')),
    amount_minor INTEGER NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL,
    occurred_on TEXT NOT NULL,
    category_id INTEGER NOT NULL,
    owner_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    description TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    original_text TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL CHECK (source IN ('web', 'telegram', 'mcp')),
    created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    updated_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT,
    deleted_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    delete_reason TEXT NOT NULL DEFAULT '',
    replaces_id INTEGER REFERENCES transactions(id) ON DELETE SET NULL,
    FOREIGN KEY (category_id, kind) REFERENCES categories(id, kind)
);
CREATE INDEX idx_transactions_active_date ON transactions(occurred_on DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_transactions_owner_date ON transactions(owner_user_id, occurred_on DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_transactions_category_date ON transactions(category_id, occurred_on DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_transactions_kind_date ON transactions(kind, occurred_on DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_transactions_replaces ON transactions(replaces_id);

CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TEXT NOT NULL,
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('web', 'telegram', 'mcp', 'system')),
    actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    actor_token_uid TEXT,
    actor_telegram_user_id INTEGER,
    actor_chat_id INTEGER,
    action TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_uid TEXT,
    before_json TEXT,
    after_json TEXT,
    request_id TEXT,
    ip TEXT,
    user_agent TEXT
);
CREATE INDEX idx_audit_created ON audit_log(created_at DESC);
CREATE INDEX idx_audit_entity ON audit_log(entity_type, entity_uid);
CREATE INDEX idx_audit_actor ON audit_log(actor_user_id, created_at DESC);

CREATE TABLE exchange_rates (
    currency TEXT NOT NULL,
    rate_date TEXT NOT NULL,
    rate_scaled INTEGER NOT NULL CHECK (rate_scaled > 0),
    rate_scale INTEGER NOT NULL DEFAULT 8,
    source TEXT NOT NULL CHECK (source IN ('nbu', 'manual')),
    fetched_at TEXT NOT NULL,
    updated_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    PRIMARY KEY (currency, rate_date)
);
CREATE INDEX idx_exchange_rates_date ON exchange_rates(rate_date);

CREATE TABLE telegram_chats (
    chat_id INTEGER PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    chat_type TEXT NOT NULL DEFAULT 'group',
    added_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE telegram_updates (
    bot_id INTEGER NOT NULL,
    update_id INTEGER NOT NULL,
    kind TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'received',
    received_at TEXT NOT NULL,
    processed_at TEXT,
    error TEXT,
    PRIMARY KEY (bot_id, update_id)
);

CREATE TABLE telegram_confirmations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id INTEGER NOT NULL,
    message_id INTEGER,
    actor_telegram_user_id INTEGER NOT NULL,
    items_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX idx_tg_confirmations_msg ON telegram_confirmations(chat_id, message_id) WHERE message_id IS NOT NULL;

CREATE TABLE telegram_actions (
    token TEXT PRIMARY KEY,
    confirmation_id INTEGER NOT NULL REFERENCES telegram_confirmations(id) ON DELETE CASCADE,
    transaction_id INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK (action IN ('undo', 'comment')),
    actor_telegram_user_id INTEGER NOT NULL,
    expires_at TEXT NOT NULL,
    used_at TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_tg_actions_confirmation ON telegram_actions(confirmation_id);

CREATE TABLE telegram_pending_edits (
    chat_id INTEGER NOT NULL,
    prompt_message_id INTEGER NOT NULL,
    confirmation_id INTEGER NOT NULL REFERENCES telegram_confirmations(id) ON DELETE CASCADE,
    transaction_id INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    actor_telegram_user_id INTEGER NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (chat_id, prompt_message_id)
);
CREATE INDEX idx_tg_pending_actor ON telegram_pending_edits(chat_id, actor_telegram_user_id, expires_at);

CREATE TABLE telegram_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    method TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    sent_at TEXT,
    last_error TEXT
);
CREATE INDEX idx_tg_outbox_pending ON telegram_outbox(status, next_attempt_at);

CREATE TABLE idempotency_keys (
    scope TEXT NOT NULL,
    key TEXT NOT NULL,
    actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    request_hash TEXT NOT NULL,
    response_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT,
    PRIMARY KEY (scope, key)
);

CREATE TABLE reminder_runs (
    kind TEXT NOT NULL,
    scheduled_for TEXT NOT NULL,
    status TEXT NOT NULL,
    error TEXT,
    created_at TEXT NOT NULL,
    PRIMARY KEY (kind, scheduled_for)
);
