# Skarb

**Skarb** (Скарб — “treasure”) is a self-hosted money tracker for a two-person household. It combines:

- a Telegram bot for low-friction capture in Ukrainian or English,
- an MCP server for Claude Code / Claude Desktop and other MCP clients,
- a Ukrainian web UI with transactions, statistics, charts, savings snapshots, and admin tools.

Backend: Go + SQLite. Deployment: one Docker container. No Node build step, no external database, no registration.

## Core ideas

- Send `сьогодні 512 грн за їжу, 300 грн за каву` in the family Telegram group. Skarb creates two dated, categorized transactions.
- Each transaction gets its own **Undo**, **Comment** and **Category** action. A comment or category change creates a linked replacement version; undo restores the previous version.
- Stats are computed in Go, never by the LLM: monthly totals, category breakdowns, comparisons, trends, per-user totals, and savings history.
- Savings are tracked as **dated account balances** (bank/cash/investment/other), not as bank/card integrations. Net worth is converted to UAH using NBU rates.
- Everything uses one SQLite database and one audit trail. Web, Telegram, and MCP share the same permission rules.

## Features

- Two active users, one admin from config, no registration.
- Both users can view everything and edit/delete any transaction in web/MCP.
- Only the admin can manage categories, savings accounts/snapshots, users, settings, and FX overrides.
- Income + expenses, multiple currencies, UAH base currency, Europe/Kyiv dates.
- Telegram allowlisted group + linked-user DMs; per-transaction Undo/Comment/Category; natural-language stats; full MCP tool mode in DMs and on @mention/reply or, optionally, every group message.
- OpenAI-compatible LLM parser with strict JSON validation, one repair attempt, and a deterministic regex fallback.
- MCP over stateless streamable HTTP + a stdio bridge for Claude Desktop.
- Admin-configurable Telegram chat allowlist, LLM settings, two reminder crons, FX refresh/override, MCP tokens.
- Calendar-year category statistics with monthly charts, a category-by-month table, filtered transaction links, and matching CSV export. Missing exchange rates are marked as incomplete totals.
- A personal profile for name/password/token management; only the admin can change Telegram bindings. User cards hold password resets and account activation controls.
- CSV export, audit log, consistent SQLite backup download.

## Quick start (Docker)

```bash
cp .env.example .env
# Generate a session secret:
openssl rand -hex 32
# Put it into SKARB_SESSION_SECRET, set SKARB_ADMIN_USERNAME and SKARB_ADMIN_PASSWORD.
docker compose up -d --build
```

Open `http://127.0.0.1:8080`, log in as the admin, then:

1. Create the second user in **Користувачі**. Save the temporary password shown in the flash message.
2. Change the admin password in **Мій профіль** if desired (the config password is only the bootstrap source).
3. Create categories/aliases if the seeded Ukrainian categories need adjustment.
4. Add savings accounts and the first balances in **Заощадження**.
5. Set up Telegram (below) and the LLM (below).
6. Create an MCP token in **Мій профіль** or **Адміністрування → Налаштування системи**.

The compose file binds the web port to `127.0.0.1` only. Put it behind Caddy/nginx/Traefik for TLS if it will be reachable from the internet. When you switch to HTTPS, set `SKARB_PUBLIC_BASE_URL=https://...` and `SKARB_COOKIE_SECURE=true`; for local plain-HTTP setup keep it `false`, otherwise the browser will not send the session cookie.

## Telegram setup

1. Create a bot with [@BotFather](https://t.me/BotFather) and put its token into `SKARB_TELEGRAM_BOT_TOKEN`.
2. In BotFather, open `/setprivacy` for the bot and choose **Disable**. This is required: with privacy mode enabled, the bot does not receive ordinary group messages such as `512 грн за їжу`.
3. Add the bot to the family group. If you changed privacy mode after adding it, remove the bot from the group and add it again.
4. In the group, send `/id`. The bot replies with the chat ID and your Telegram user ID. `/id` works before the chat is allowlisted.
5. In **Адміністрування → Налаштування системи → Telegram**, add the group chat ID (title is optional). Only allowlisted groups are accepted.
6. In **Користувачі**, edit each user and set their numeric Telegram user ID. Only linked, active users can add transactions or view stats.
7. Send a test message in the group.

Commands:

```text
/stats [YYYY-MM]              monthly summary
/compare [YYYY-MM] [YYYY-MM]  compare two months
/categories [YYYY-MM]         category breakdown
/trend [N]                    monthly trend for N months
/last [N]                     recent transactions
/undo                         undo your latest transaction
/id                           chat and user IDs
/snapshot                     savings help (admin)
/cancel                       cancel a pending comment reply
```

In a private chat, or in a group when the bot is @mentioned or you reply to one of its
messages, the bot can call the same tools as the MCP server. An admin can change
**Адміністрування → Налаштування системи → ШІ-парсер → ШІ в групах** to process every group message instead:

```text
покажи всі транзакції за жовтень
скільки витратили за жовтень
додай 500 грн кава
видали останню каву
створи категорію Подарунки
```

The LLM chooses one tool and returns JSON arguments; Go validates them against the tool
schema, executes the tool in-process with `source=telegram`, and formats the result.
Destructive actions show a confirmation card first. Long lists are paginated with «Ще».
In a group without a mention, simple captures such as `512 грн за їжу` still go through
the deterministic parser and do not call the LLM in the default mode. In the all-messages
mode, unrelated chatter is classified by the LLM but receives no bot reply. Set
`SKARB_TELEGRAM_TOOL_MODE=false` to disable full tool mode.

The confirmation list has one row of buttons per transaction: `↩ Скасувати N`, `✏️ Коментар N` and `🏷 Категорія N`. Clicking **Коментар** sends a ForceReply prompt; reply with the new comment. Clicking **Категорія** turns the confirmation into a picker of active categories of the same kind (`← Назад` returns without changes). In both cases Skarb soft-deletes the old row and creates a linked replacement; **Undo** on the replacement restores the original.

## LLM parser

The LLM is optional. Without it, the deterministic parser handles simple messages such as the Ukrainian example above. With it, arbitrary phrasing, dates, and category aliases work better.

Set the API key in the environment:

```dotenv
SKARB_LLM_API_KEY=...
```

Then in **Адміністрування → Налаштування системи → ШІ-парсер** set:

- **Base URL** — e.g. `https://api.openai.com/v1`, `https://openrouter.ai/api/v1`, `http://host.docker.internal:11434/v1` (Ollama), or `http://host.docker.internal:1234/v1` (LM Studio).
- **Model** — the exact model name at that endpoint.
- **JSON mode** — `auto` tries `json_schema`, then `json_object`, then prompt-only JSON. You can force a mode if your provider behaves differently.
- Timeout, output cap, max transactions/message, max input characters, temperature.

The parser returns validated JSON intents only. The Go service validates currencies, dates, category IDs, amounts, permissions, and limits before writing. Prompt injection cannot execute SQL or tools. Invalid LLM output gets one repair attempt and then falls back to the regex parser.

When Telegram tool mode is enabled, the same LLM is also used as a tool router: it selects
one MCP tool and returns JSON arguments, which Go validates against the advertised input
schema, executes in-process with `source=telegram`, and formats deterministically. Tool
results are not sent back to the LLM in this build, so the model cannot rewrite amounts or
totals. The prompt includes the message, today's date, the per-role tool catalog, and active
categories/accounts/users — not the ledger.

Use **Перевірити ШІ** in the admin settings to run a sample parse through the saved configuration; save the settings first.

For a local model, the container needs network access to the host. On Docker Desktop use `host.docker.internal`; on Linux add an `extra_hosts: ["host.docker.internal:host-gateway"]` entry to compose.

## MCP

Create a token in **Мій профіль → Інтеграції → Мої MCP-токени** (or as admin in **Адміністрування → Налаштування системи → MCP-токени**). The plaintext token is shown once.

### HTTP (Claude Code / remote clients)

```bash
claude mcp add --transport http skarb https://skarb.example.com/mcp \
  --header "Authorization: Bearer skarb_pat_REPLACE_ME"
```

Project/JSON config:

```json
{
  "mcpServers": {
    "skarb": {
      "type": "http",
      "url": "https://skarb.example.com/mcp",
      "headers": {
        "Authorization": "Bearer skarb_pat_REPLACE_ME"
      }
    }
  }
}
```

### stdio bridge (Claude Desktop)

Use the same binary locally:

```json
{
  "mcpServers": {
    "skarb": {
      "command": "/usr/local/bin/skarb",
      "args": ["mcp-stdio", "--url", "https://skarb.example.com/mcp"],
      "env": {
        "SKARB_MCP_TOKEN": "skarb_pat_REPLACE_ME"
      }
    }
  }
}
```

With Docker on the same host:

```json
{
  "mcpServers": {
    "skarb": {
      "command": "docker",
      "args": [
        "exec", "-i", "-e", "SKARB_MCP_TOKEN",
        "skarb", "/skarb", "mcp-stdio",
        "--url", "http://127.0.0.1:8080/mcp"
      ],
      "env": {
        "SKARB_MCP_TOKEN": "skarb_pat_REPLACE_ME"
      }
    }
  }
}
```

The bridge logs only to stderr. Its stdout contains only MCP JSON.

### MCP tools

The Telegram bot's full tool mode uses this same in-process tool layer: the same schemas,
permission checks, source-aware audit, and output shapes. External MCP clients and the bot
therefore cannot drift apart.

Transactions: `list_transactions`, `add_transactions`, `update_transaction`, `delete_transaction`.

Stats: `stats_summary`, `stats_category_breakdown`, `stats_user_breakdown`, `stats_compare_periods`, `stats_trend`, `net_worth`, `savings_history`.

Reference data: `list_categories`, `list_accounts`, `list_exchange_rates`, `list_users`.

Admin only: `create_category`, `update_category`, `delete_category`, `create_account`, `update_account`, `delete_account`, `upsert_snapshot`, `delete_snapshot`, `create_user`, `update_user`.

Amounts are decimal strings, dates are `YYYY-MM-DD`, currencies are ISO 4217. Mutations accept an optional `idempotency_key`; a replay returns the original response, a conflicting reuse is rejected. Non-admin tokens do not see admin-only tools and receive `forbidden` if they try.

## Savings snapshots and FX

Create accounts in **Заощадження** (e.g. `bank1` for one spouse, `bank2` for the other, `cash`, `investment`). Each account has an owner and a currency. Update every balance in the **Новий знімок** form and save them together; the latest snapshot is the current savings position, and the trend compares complete snapshots rather than calendar months.

- Transfers between accounts are not tracked. Snapshots answer “how much do we have now?”; transactions answer “where did money go?”.
- The savings page shows native totals per currency and can convert the combined total to any supported currency.
- Rates come from the NBU daily API (`bank.gov.ua`). The app refreshes at ~16:10 Kyiv, on startup, and on demand. Historical rates are fetched when needed; a 730-day backfill runs at startup.
- Stats convert foreign transactions at the transaction-date rate (or the nearest prior business day). Net worth converts snapshots at the requested date's rate.
- Admins can manually override a rate in **Адміністрування → Налаштування системи → Курси NBU**. Manual overrides are not overwritten by the daily NBU job.
- If a rate is missing, converted totals exclude the row and show a warning; the native-currency total remains.

## Reminders

Two configurable cron entries live in **Адміністрування → Налаштування системи → Нагадування**:

- **transactions** — default `0 10 * * 1` (Monday 10:00 Kyiv), window 7 days. Sends only when there are zero active transactions in the window.
- **snapshot** — default `0 10 1 * *` (1st of the month, 10:00 Kyiv). Lists active accounts without a balance in the current month.

Each has an enabled flag, timezone, target group, and optional target user. If no target is configured, the admin's Telegram DM is used. Changes reload the scheduler immediately. The **Тест** button sends a sample reminder to the acting admin's Telegram DM (link your Telegram user ID first). Missed runs while the container is down are not backfilled. The FX refresh, daily backup and daily cleanup (03:30 Kyiv: expired sessions and pending Telegram edits, plus Telegram update IDs and MCP idempotency keys older than 30 days) are internal jobs and are separate from these two user-configurable reminder crons.

## Backup and restore

- The app runs `VACUUM INTO` daily at 03:00 Kyiv into `/data/backups` (retain 14 days).
- **Адміністрування → Налаштування системи → Резервні копії** downloads a consistent snapshot.
- Restore: stop the container, keep the old database/WAL/SHM, replace the main `skarb.db` with the backup, remove stale `-wal`/`-shm`, then start the container and verify `/readyz`, login, recent transactions, snapshots, and MCP.
- The database is not encrypted at rest. Use host/volume encryption if that matters to you.

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/skarb
```

Run locally without Docker:

```bash
SKARB_DATA_DIR=./data \
SKARB_HTTP_ADDR=:8080 \
SKARB_ADMIN_USERNAME=admin \
SKARB_ADMIN_PASSWORD=adminpassword123 \
SKARB_SESSION_SECRET=dev-secret-at-least-32-characters \
go run ./cmd/skarb serve
```

CLI:

```bash
skarb serve
skarb mcp-stdio --url https://skarb.example.com/mcp   # token in SKARB_MCP_TOKEN
skarb healthcheck --url http://127.0.0.1:8080/healthz
skarb admin reset-password                            # re-applies config admin password
skarb backup                                          # writes a VACUUM INTO backup
```

### Project layout

```text
cmd/skarb/                 CLI: serve, mcp-stdio, healthcheck, admin, backup
internal/auth/             Argon2id, tokens, CSRF primitives
internal/agent/            LLM tool router for the Telegram bot
internal/backup/           SQLite VACUUM INTO backups
internal/config/           environment configuration
internal/domain/           entities, dates, errors
internal/fx/               NBU client and rate lookup
internal/llm/              OpenAI-compatible client + JSON compatibility ladder
internal/mcp/              MCP HTTP server, in-process tool sessions, tools, idempotency, stdio bridge client
internal/money/            minor units, parsing, FX conversion, Ukrainian formatting
internal/nlu/              prompt/schema, validation, deterministic regex parser
internal/reminders/        two reminder crons + FX/backup jobs
internal/service/          permissions, transactions, stats, snapshots, users, audit
internal/store/            SQLite schema, migrations, repositories
internal/telegram/         polling, commands, tool mode, confirmations, undo/comment, comments
internal/web/              routes, handlers, templates, static CSS
```

## Security notes

- Keep `SKARB_SESSION_SECRET` secret and stable; rotating it invalidates web sessions.
- Admin credentials from config seed the first admin only. Use `skarb admin reset-password` for lockout recovery.
- MCP tokens and sessions are stored as SHA-256 hashes; token plaintext is shown once.
- Telegram access requires both an allowlisted group (or a DM) and a linked, active user. Unknown groups are ignored.
- The web app uses Argon2id, HttpOnly/SameSite cookies, CSRF tokens, login throttling, and security headers.
- Put the app behind TLS. Never expose `/mcp` or the web UI over plain HTTP on the internet.
- The LLM sees only the current message, the allowed categories/accounts/users, and the current date — not your ledger. Tool results are formatted in Go and are not sent back to the LLM.

## Current simplifications

- The web UI is server-rendered with CSS bars and tables; there is no JavaScript build step. HTMX/Chart.js can be added later without changing the backend.
- Telegram tool mode performs one tool call per message. Transaction lists expose row delete buttons, but there is no autonomous multi-step LLM loop and no row-level edit form; use the web UI or provide a transaction UID for edits.
- Telegram confirmations are sent immediately with a short retry loop; the `telegram_outbox` table is present for future durable delivery but the bot currently sends directly.
- MCP uses static bearer tokens, not OAuth discovery. Use it with Claude Code/Desktop or a client that accepts an `Authorization: Bearer` header. Per-token MCP rate limiting is not enabled in this build.
- MCP idempotency is best-effort: the stored response is replayed for a repeated key, but two truly concurrent first calls can both execute before one response is stored.
- One replica only. Do not run multiple containers against the same SQLite database or two bot pollers with the same token.
- Telegram reminder catch-up is intentionally disabled; a cron missed while the container was down is skipped.
