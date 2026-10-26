-- Allow the "change category" button on Telegram confirmations. SQLite cannot
-- alter a CHECK constraint, so the table is rebuilt. Nothing references it.
CREATE TABLE telegram_actions_new (
    token TEXT PRIMARY KEY,
    confirmation_id INTEGER NOT NULL REFERENCES telegram_confirmations(id) ON DELETE CASCADE,
    transaction_id INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK (action IN ('undo', 'comment', 'category')),
    actor_telegram_user_id INTEGER NOT NULL,
    expires_at TEXT NOT NULL,
    used_at TEXT,
    created_at TEXT NOT NULL
);

INSERT INTO telegram_actions_new
    (token, confirmation_id, transaction_id, action, actor_telegram_user_id, expires_at, used_at, created_at)
SELECT token, confirmation_id, transaction_id, action, actor_telegram_user_id, expires_at, used_at, created_at
FROM telegram_actions;

DROP TABLE telegram_actions;
ALTER TABLE telegram_actions_new RENAME TO telegram_actions;
CREATE INDEX idx_tg_actions_confirmation ON telegram_actions(confirmation_id);
