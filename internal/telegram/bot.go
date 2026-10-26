package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"skarb/internal/agent"
	"skarb/internal/config"
	"skarb/internal/domain"
	"skarb/internal/mcp"
	"skarb/internal/nlu"
	"skarb/internal/service"
)

var ErrDisabled = errors.New("telegram bot is not configured")

type Bot struct {
	svc       *service.Service
	cfg       config.Config
	log       *slog.Logger
	parser    *nlu.Parser
	router    *agent.Router
	mcpServer *mcp.Server
	api       *tgbot.Bot
	loc       *time.Location
	username  string
	toolMode  bool
}

func New(svc *service.Service, cfg config.Config, logger *slog.Logger, toolServer *mcp.Server) (*Bot, error) {
	if strings.TrimSpace(cfg.TelegramBotToken) == "" {
		return nil, ErrDisabled
	}
	if logger == nil {
		logger = slog.Default()
	}
	if toolServer == nil {
		toolServer = mcp.New(svc, cfg, logger)
	}
	b := &Bot{
		svc: svc, cfg: cfg, log: logger,
		parser:    nlu.NewParser(svc, logger),
		router:    agent.NewRouter(svc, logger),
		mcpServer: toolServer,
		loc:       svc.Loc,
		toolMode:  cfg.TelegramToolMode,
	}
	api, err := tgbot.New(cfg.TelegramBotToken,
		tgbot.WithDefaultHandler(b.handleUpdate),
		tgbot.WithAllowedUpdates(tgbot.AllowedUpdates{"message", "callback_query"}),
		tgbot.WithHTTPClient(cfg.TelegramPollTimeout, &http.Client{Timeout: cfg.TelegramPollTimeout + 20*time.Second}),
		tgbot.WithErrorsHandler(func(err error) {
			b.log.Error("telegram api error", "error", redactToken(err, cfg.TelegramBotToken))
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("telegram init: %w", redactToken(err, cfg.TelegramBotToken))
	}
	b.api = api
	return b, nil
}

func (b *Bot) Start(ctx context.Context) {
	b.log.Info("telegram bot starting", "bot_id", b.api.ID())
	b.logStartupDiagnostics(ctx)
	if _, err := b.svc.Store.PurgeToolActions(ctx, b.svc.Now().Add(-24*time.Hour)); err != nil {
		b.log.Warn("telegram tool action purge failed", "error", err)
	}
	if _, err := b.api.DeleteWebhook(ctx, &tgbot.DeleteWebhookParams{}); err != nil {
		b.log.Warn("telegram deleteWebhook failed; polling may conflict with an existing webhook", "error", redactToken(err, b.cfg.TelegramBotToken))
	} else {
		b.log.Info("telegram webhook cleared; using long polling")
	}
	b.api.Start(ctx)
	b.log.Info("telegram bot stopped")
}

// logStartupDiagnostics surfaces the failures that otherwise look like the bot
// is silently ignoring messages: BotFather privacy mode (Telegram never hands
// ordinary group messages to the bot) and an allowlisted chat that the bot can
// no longer read. Both are checked once, at startup.
func (b *Bot) logStartupDiagnostics(ctx context.Context) {
	if b.toolMode {
		cfg, err := b.svc.GetLLMConfig(ctx)
		switch {
		case err != nil:
			b.log.Warn("LLM settings lookup failed", "error", err)
		case !b.svc.LLMConfigured(cfg):
			b.log.Warn("telegram tool mode is enabled but the LLM is not configured; only the deterministic parser will run",
				"fix", "set Base URL and model in Налаштування → ШІ-парсер, or set SKARB_TELEGRAM_TOOL_MODE=false")
		default:
			b.log.Info("LLM configured for telegram tool mode", "model", cfg.Model)
		}
	}

	me, err := b.api.GetMe(ctx)
	if err != nil {
		b.log.Warn("telegram getMe failed", "error", redactToken(err, b.cfg.TelegramBotToken))
	} else {
		b.username = me.Username
		if me.CanReadAllGroupMessages {
			b.log.Info("telegram bot capabilities",
				"bot_id", me.ID, "username", me.Username,
				"privacy_mode", "disabled", "can_read_all_group_messages", true)
		} else {
			b.log.Warn("telegram bot privacy mode is ENABLED: Telegram will not deliver ordinary group messages to the bot",
				"bot_id", me.ID, "username", me.Username,
				"can_read_all_group_messages", false,
				"impact", "only commands, replies to the bot, and service messages arrive in groups",
				"fix", "BotFather → /setprivacy → select the bot → Disable, then remove and re-add the bot to the group")
		}
	}

	chats, err := b.svc.Store.ListTelegramChats(ctx)
	if err != nil {
		b.log.Warn("telegram allowlist lookup failed", "error", err)
		return
	}
	if len(chats) == 0 {
		b.log.Warn("telegram allowlist is empty; group messages will be ignored until a chat is added in Admin → Settings → Telegram")
		return
	}
	for _, c := range chats {
		chat, err := b.api.GetChat(ctx, &tgbot.GetChatParams{ChatID: c.ChatID})
		if err != nil {
			b.log.Warn("telegram allowlisted chat is not accessible",
				"chat_id", c.ChatID, "title", c.Title,
				"error", redactToken(err, b.cfg.TelegramBotToken),
				"hint", "verify the chat id and that the bot is still a member of the chat")
			continue
		}
		attrs := []any{"chat_id", c.ChatID, "title", chat.Title, "type", string(chat.Type)}
		member, err := b.api.GetChatMember(ctx, &tgbot.GetChatMemberParams{ChatID: c.ChatID, UserID: b.api.ID()})
		if err != nil {
			b.log.Warn("telegram bot membership could not be verified", append(attrs,
				"error", redactToken(err, b.cfg.TelegramBotToken))...)
			continue
		}
		attrs = append(attrs, "bot_status", string(member.Type))
		if member.Type == models.ChatMemberTypeLeft || member.Type == models.ChatMemberTypeBanned {
			b.log.Warn("telegram bot is not a member of an allowlisted chat; messages from it will never arrive", attrs...)
			continue
		}
		b.log.Info("telegram allowlisted chat is reachable", attrs...)
	}
}

func (b *Bot) handleUpdate(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
	if update == nil {
		return
	}
	kind := "other"
	switch {
	case update.Message != nil:
		kind = "message"
	case update.CallbackQuery != nil:
		kind = "callback"
	default:
		b.log.Info("telegram update ignored", "update_id", update.ID, "reason", "unsupported update type")
		return
	}
	attrs := updateLogAttrs(update)
	attrs = append(attrs, "kind", kind)
	// Logged before the dedupe check: seeing this line proves Telegram
	// delivered the update; not seeing it points at BotFather privacy mode,
	// a wrong chat, or a stopped poller.
	b.log.Info("telegram update received", attrs...)
	isNew, err := b.svc.Store.MarkUpdateReceived(ctx, b.api.ID(), update.ID, kind)
	if err != nil {
		b.log.Error("telegram dedupe failed", "update_id", update.ID, "error", err)
		return
	}
	if !isNew {
		b.log.Debug("telegram duplicate update skipped", append(attrs, "reason", "already recorded")...)
		return
	}
	start := time.Now()
	err = b.process(ctx, update)
	attrs = append(attrs, "duration", time.Since(start).Round(time.Millisecond))
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
		b.log.Error("telegram update failed", append(attrs, "error", redactToken(err, b.cfg.TelegramBotToken))...)
	} else {
		b.log.Info("telegram update processed", attrs...)
	}
	_ = b.svc.Store.MarkUpdateProcessed(ctx, b.api.ID(), update.ID, errMsg)
}

// updateLogAttrs describes an update without logging user text: amounts and
// notes are sensitive, and the drop reason is what matters for troubleshooting.
// Set SKARB_LOG_LEVEL=debug to additionally log the message text.
func updateLogAttrs(update *models.Update) []any {
	attrs := []any{"update_id", update.ID}
	if m := update.Message; m != nil {
		attrs = append(attrs,
			"chat_id", m.Chat.ID,
			"chat_type", string(m.Chat.Type),
			"message_id", m.ID,
			"text_len", len([]rune(m.Text)),
		)
		if m.From != nil {
			attrs = append(attrs, "from_id", m.From.ID)
			if m.From.Username != "" {
				attrs = append(attrs, "from_username", m.From.Username)
			}
		}
		if m.ReplyToMessage != nil {
			attrs = append(attrs, "reply_to_message_id", m.ReplyToMessage.ID)
		}
	}
	if cq := update.CallbackQuery; cq != nil {
		action := ""
		if i := strings.IndexByte(cq.Data, ':'); i >= 0 {
			action = cq.Data[:i]
		}
		attrs = append(attrs, "callback_id", cq.ID, "callback_action", action, "from_id", cq.From.ID)
		if cq.Message.Message != nil {
			attrs = append(attrs, "chat_id", cq.Message.Message.Chat.ID, "message_id", cq.Message.Message.ID)
		}
	}
	return attrs
}

func (b *Bot) process(ctx context.Context, update *models.Update) error {
	if update.CallbackQuery != nil {
		return b.processCallback(ctx, update.CallbackQuery)
	}
	if update.Message != nil {
		return b.processMessage(ctx, update.Message)
	}
	return nil
}

func (b *Bot) processMessage(ctx context.Context, msg *models.Message) error {
	if msg == nil {
		return nil
	}
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)
	var tgID int64
	isBot := false
	if msg.From != nil {
		tgID = msg.From.ID
		isBot = msg.From.IsBot
	}
	if msg.From == nil || isBot || text == "" {
		b.log.Info("telegram message ignored",
			"chat_id", chatID, "chat_type", string(msg.Chat.Type), "message_id", msg.ID,
			"from_id", tgID, "from_is_bot", isBot, "text_len", len([]rune(msg.Text)),
			"reason", "message has no text or was sent by a bot")
		return nil
	}
	b.log.Debug("telegram message text", "chat_id", chatID, "message_id", msg.ID, "text", text)

	// /id is intentionally available before chat authorization so the admin can
	// discover the group ID while setting Skarb up.
	if commandIs(text, "/id") {
		linked, _ := b.linkedUser(ctx, tgID)
		status := "не прив'язаний"
		if linked != nil {
			status = "прив'язаний: " + linked.DisplayName
		}
		b.log.Info("telegram /id command answered", "chat_id", chatID, "tg_user_id", tgID, "linked", linked != nil)
		return b.reply(ctx, chatID, fmt.Sprintf(
			"🆔 Chat ID: <code>%d</code>\n👤 Telegram user ID: <code>%d</code>\nСтатус: %s",
			chatID, tgID, html.EscapeString(status)), nil)
	}
	user, err := b.linkedUser(ctx, tgID)
	if err != nil {
		if msg.Chat.Type == models.ChatTypePrivate {
			b.log.Info("telegram message rejected",
				"chat_id", chatID, "tg_user_id", tgID,
				"reason", "telegram user is not linked to an active Skarb user", "replied", true)
			return b.reply(ctx, chatID, "Цей акаунт не прив'язаний. Зверніться до адміністратора Skarb.", nil)
		}
		b.log.Warn("telegram message ignored",
			"chat_id", chatID, "tg_user_id", tgID,
			"reason", "telegram user is not linked to an active Skarb user",
			"hint", "set this person's Telegram user ID on Admin → Users")
		return nil
	}
	allowed, err := b.allowedChat(ctx, msg.Chat)
	if err != nil {
		return err
	}
	if !allowed {
		b.log.Warn("telegram message ignored",
			"chat_id", chatID, "chat_type", string(msg.Chat.Type), "tg_user_id", tgID,
			"reason", "chat is not in the Telegram allowlist",
			"hint", "add this chat_id in Admin → Settings → Telegram")
		return nil
	}
	actor := b.actorFor(user, tgID, chatID)
	actor.RequestID = fmt.Sprintf("tg:%d", msg.ID)

	if strings.HasPrefix(text, "/") {
		return b.handleCommand(ctx, chatID, tgID, user, actor, text, msg)
	}

	// A reply to a ForceReply prompt is the comment edit flow.
	if msg.ReplyToMessage != nil {
		if handled, err := b.tryCommentReply(ctx, msg, actor); handled || err != nil {
			return err
		}
	}

	if b.toolMode {
		if b.isAddressedToBot(msg) {
			clean := b.stripBotMention(text)
			if clean == "" {
				return b.reply(ctx, chatID, helpText(), nil)
			}
			if len([]rune(clean)) > 2000 {
				b.log.Info("telegram message rejected", "chat_id", chatID, "message_id", msg.ID,
					"reason", "message exceeds 2000 characters")
				_ = b.reply(ctx, chatID, "Повідомлення занадто довге. Скоротіть його до 2000 символів.", nil)
				return nil
			}
			handled, err := b.handleToolMessage(ctx, msg, user, actor, clean)
			if handled || err != nil {
				return err
			}
			// LLM is not configured: fall through to the legacy parser.
		} else {
			// In a group, the bot only runs full tool mode when explicitly
			// addressed. Ordinary messages still support deterministic quick
			// capture, but never invoke the LLM.
			if !nlu.LooksFinancial(text) {
				b.log.Info("telegram message ignored",
					"chat_id", chatID, "message_id", msg.ID, "text_len", len([]rune(text)),
					"reason", "group message is not addressed to the bot")
				return nil
			}
			if len([]rune(text)) > 2000 {
				b.log.Info("telegram message rejected", "chat_id", chatID, "message_id", msg.ID,
					"reason", "message exceeds 2000 characters")
				_ = b.reply(ctx, chatID, "Повідомлення занадто довге. Скоротіть його до 2000 символів.", nil)
				return nil
			}
			intent, err := b.parser.ParseRegex(ctx, text)
			if err != nil {
				b.log.Info("telegram message ignored",
					"chat_id", chatID, "message_id", msg.ID,
					"reason", "group message is not a recognized quick transaction")
				return nil
			}
			if err := b.handleIntent(ctx, msg, user, actor, intent, text); err != nil {
				return err
			}
			b.log.Info("telegram intent handled (quick capture)",
				"chat_id", chatID, "message_id", msg.ID,
				"operation", intent.Operation, "transactions", len(intent.Transactions))
			return nil
		}
	}

	if !b.shouldParse(ctx, msg, text) {
		b.log.Info("telegram message ignored",
			"chat_id", chatID, "message_id", msg.ID, "text_len", len([]rune(text)),
			"reason", "message does not look like a transaction or stats query",
			"llm_configured", b.llmConfigured(ctx))
		return nil
	}
	if len([]rune(text)) > 2000 {
		b.log.Info("telegram message rejected", "chat_id", chatID, "message_id", msg.ID,
			"reason", "message exceeds 2000 characters")
		_ = b.reply(ctx, chatID, "Повідомлення занадто довге. Скоротіть його до 2000 символів.", nil)
		return nil
	}
	intent, err := b.parser.Parse(ctx, text)
	if err != nil {
		b.log.Warn("telegram message parse failed",
			"chat_id", chatID, "message_id", msg.ID, "text_len", len([]rune(text)), "error", err)
		if msg.Chat.Type == models.ChatTypePrivate || nlu.LooksFinancial(text) {
			_ = b.reply(ctx, chatID, "Не зрозумів повідомлення. Спробуйте, наприклад:\n<code>сьогодні 512 грн за їжу, 300 грн за каву</code>", nil)
		}
		return nil
	}
	if err := b.handleIntent(ctx, msg, user, actor, intent, text); err != nil {
		return err
	}
	b.log.Info("telegram intent handled",
		"chat_id", chatID, "message_id", msg.ID,
		"operation", intent.Operation, "transactions", len(intent.Transactions))
	return nil
}

// shouldParse decides whether a message reaches a parser at all. The
// deterministic prefilter skips chatter, but a configured LLM is also allowed
// to look at addressed messages containing numbers: people often write amounts
// without a currency, for example a multi-line list of expenses.
func (b *Bot) shouldParse(ctx context.Context, msg *models.Message, text string) bool {
	if nlu.LooksFinancial(text) {
		return true
	}
	if !strings.ContainsAny(text, "0123456789") || !b.isAddressedToBot(msg) {
		return false
	}
	return b.llmConfigured(ctx)
}

func (b *Bot) llmConfigured(ctx context.Context) bool {
	cfg, err := b.svc.GetLLMConfig(ctx)
	if err != nil {
		return false
	}
	return b.svc.LLMConfigured(cfg)
}

func (b *Bot) processCallback(ctx context.Context, cq *models.CallbackQuery) error {
	if cq == nil || cq.Message.Message == nil {
		return b.answerCallback(ctx, cq, "Повідомлення недоступне.")
	}
	msg := cq.Message.Message
	chatID := msg.Chat.ID
	user, err := b.linkedUser(ctx, cq.From.ID)
	if err != nil {
		return b.answerCallback(ctx, cq, "Немає доступу.")
	}
	allowed, err := b.allowedChat(ctx, msg.Chat)
	if err != nil || !allowed {
		return b.answerCallback(ctx, cq, "Чат не авторизовано.")
	}
	if isToolCallback(cq.Data) {
		actor := b.actorFor(user, cq.From.ID, chatID)
		actor.RequestID = "tg-cb:" + cq.ID
		return b.handleToolCallback(ctx, cq, actor)
	}
	// Category buttons carry an argument after "|": the chosen category or "back".
	token, arg, _ := strings.Cut(cq.Data, "|")
	action, err := b.svc.Store.GetAction(ctx, token)
	if err != nil {
		return b.answerCallback(ctx, cq, "Кнопка застаріла.")
	}
	if action.UsedAt != nil || b.svc.Now().After(action.ExpiresAt) {
		return b.answerCallback(ctx, cq, "Кнопка вже неактивна.")
	}
	actor := b.actorFor(user, cq.From.ID, chatID)
	actor.RequestID = "tg-cb:" + cq.ID
	if action.Action == "category" {
		// Opening the picker must not consume the token; the handler uses it
		// only when a category is actually chosen.
		return b.handleCategoryCallback(ctx, chatID, actor, action, cq, arg)
	}
	used, err := b.svc.Store.UseAction(ctx, token)
	if err != nil || !used {
		return b.answerCallback(ctx, cq, "Кнопка вже використана.")
	}
	switch action.Action {
	case "undo":
		return b.handleUndoCallback(ctx, chatID, actor, action, cq)
	case "comment":
		return b.handleCommentCallback(ctx, chatID, actor, action, cq)
	default:
		return b.answerCallback(ctx, cq, "Невідома дія.")
	}
}

func (b *Bot) linkedUser(ctx context.Context, tgID int64) (*domain.User, error) {
	u, err := b.svc.Store.GetUserByTelegramID(ctx, tgID)
	if err != nil || !u.IsActive {
		return nil, domain.ErrUnauthorized
	}
	return u, nil
}

func (b *Bot) allowedChat(ctx context.Context, chat models.Chat) (bool, error) {
	if chat.Type == models.ChatTypePrivate {
		return true, nil
	}
	return b.svc.IsTelegramChatAllowed(ctx, chat.ID)
}

func (b *Bot) actorFor(user *domain.User, tgID, chatID int64) domain.Actor {
	actor := b.svc.ActorForUser(user, domain.SourceTelegram)
	actor.TelegramUserID = tgID
	actor.ChatID = chatID
	return actor
}

// SendMessage implements the reminders sender interface.
func (b *Bot) SendMessage(ctx context.Context, chatID int64, text string) error {
	return b.reply(ctx, chatID, text, nil)
}

func (b *Bot) reply(ctx context.Context, chatID int64, text string, markup models.ReplyMarkup) error {
	msg, err := b.api.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: markup,
	})
	if err != nil {
		b.log.Warn("telegram sendMessage failed", "chat_id", chatID, "error", redactToken(err, b.cfg.TelegramBotToken))
		return err
	}
	b.log.Debug("telegram message sent", "chat_id", chatID, "message_id", msg.ID)
	return nil
}

func (b *Bot) answerCallback(ctx context.Context, cq *models.CallbackQuery, text string) error {
	if cq == nil {
		return nil
	}
	_, err := b.api.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
		CallbackQueryID: cq.ID,
		Text:            text,
	})
	return err
}

func (b *Bot) handleIntent(ctx context.Context, msg *models.Message, user *domain.User, actor domain.Actor, intent *nlu.Intent, originalText string) error {
	chatID := msg.Chat.ID
	switch intent.Operation {
	case "noop":
		return nil
	case "add_transactions":
		items := make([]service.TransactionItemInput, 0, len(intent.Transactions))
		for _, tx := range intent.Transactions {
			items = append(items, service.TransactionItemInput{
				Amount: tx.Amount, Currency: tx.Currency, Date: tx.Date,
				Category: tx.Category, Description: tx.Description,
			})
		}
		created, err := b.svc.CreateTransactions(ctx, actor, service.CreateTransactionsInput{
			Items: items, Source: domain.SourceTelegram, OriginalText: originalText,
		})
		if err != nil {
			return b.reply(ctx, chatID, "Не вдалося створити транзакцію: "+html.EscapeString(err.Error()), nil)
		}
		return b.sendConfirmation(ctx, chatID, msg.From.ID, created, intent.Warnings)
	case "stats_query", "list_transactions":
		return b.handleStatsIntent(ctx, chatID, actor, intent, originalText)
	case "snapshot":
		return b.handleSnapshotIntent(ctx, chatID, user, actor, intent)
	default:
		return nil
	}
}

func commandIs(text, command string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	cmd := fields[0]
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	return cmd == command
}

func commandArgs(text string) []string {
	fields := strings.Fields(text)
	if len(fields) <= 1 {
		return nil
	}
	return fields[1:]
}

func escape(s string) string { return html.EscapeString(s) }

func redactToken(err error, token string) error {
	if err == nil || token == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), token, "<redacted>")
	return errors.New(msg)
}

func jsonItems(items []confirmationItem) string {
	b, _ := json.Marshal(items)
	return string(b)
}

func parseItems(raw string) []confirmationItem {
	var items []confirmationItem
	_ = json.Unmarshal([]byte(raw), &items)
	return items
}
