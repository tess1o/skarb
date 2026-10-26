-- Pending MCP tool actions and paginated tool results for the Telegram bot.
-- Kept separate from telegram_actions because that table is constrained to the
-- legacy undo/comment actions and has no free-form payload columns.
CREATE TABLE telegram_tool_actions (
    token TEXT PRIMARY KEY,
    chat_id INTEGER NOT NULL,
    message_id INTEGER,
    actor_telegram_user_id INTEGER NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('confirm', 'page')),
    tool_name TEXT NOT NULL,
    arguments_json TEXT NOT NULL,
    result_json TEXT NOT NULL DEFAULT '',
    next_cursor TEXT NOT NULL DEFAULT '',
    page_size INTEGER NOT NULL DEFAULT 20,
    page_number INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'used', 'cancelled')),
    expires_at TEXT NOT NULL,
    used_at TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX idx_tg_tool_actions_message
    ON telegram_tool_actions(chat_id, message_id);
CREATE INDEX idx_tg_tool_actions_pending
    ON telegram_tool_actions(status, expires_at);
