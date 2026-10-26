package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"skarb/internal/agent"
	"skarb/internal/auth"
	"skarb/internal/domain"
	"skarb/internal/mcp"
	"skarb/internal/money"
	"skarb/internal/nlu"
	"skarb/internal/store"
)

// handleToolMessage runs one LLM-routed MCP tool call. It returns false only
// when the LLM is not configured and the caller should use the legacy path.
func (b *Bot) handleToolMessage(ctx context.Context, msg *models.Message, user *domain.User, actor domain.Actor, text string) (bool, error) {
	if !b.toolMode || b.router == nil || b.mcpServer == nil {
		return false, nil
	}
	chatID := msg.Chat.ID
	llmCfg, cfgErr := b.svc.GetLLMConfig(ctx)
	if cfgErr != nil || !b.svc.LLMConfigured(llmCfg) {
		return false, nil
	}

	sessionCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	session, err := b.mcpServer.NewToolSession(sessionCtx, actor)
	if err != nil {
		b.log.Error("telegram tool session failed", "error", err)
		return true, b.reply(ctx, chatID, "Не вдалося підключити інструменти Skarb. Спробуйте пізніше.", nil)
	}
	defer session.Close()

	tools := session.ListTools()
	decision, err := b.router.Decide(ctx, actor, text, tools)
	if err != nil {
		if errors.Is(err, agent.ErrNotConfigured) {
			return false, nil
		}
		b.log.Warn("telegram tool router failed", "error", err)
		if nlu.LooksFinancial(text) {
			if used, fallbackErr := b.handleRegexFallback(ctx, msg, user, actor, text); used || fallbackErr != nil {
				return used, fallbackErr
			}
		}
		return true, b.reply(ctx, chatID, "Не вдалося обробити запит. Спробуйте ще раз або переформулюйте.", nil)
	}

	if decision.Tool == "noop" {
		if nlu.LooksFinancial(text) {
			if used, fallbackErr := b.handleRegexFallback(ctx, msg, user, actor, text); used || fallbackErr != nil {
				return used, fallbackErr
			}
		}
		return true, b.reply(ctx, chatID, "Не зрозумів. Напишіть, наприклад: «покажи всі транзакції за жовтень».", nil)
	}

	toolInfo, ok := findTool(tools, decision.Tool)
	if !ok {
		return true, b.reply(ctx, chatID, "Ця операція недоступна для вашого акаунта.", nil)
	}
	args := decision.Arguments
	if decision.Tool == "add_transactions" {
		args = injectOriginalText(args, text)
	}
	if isDestructiveTool(decision.Tool, args) {
		if err := b.askToolConfirmation(ctx, chatID, actor, decision.Tool, args); err != nil {
			b.log.Warn("telegram tool confirmation failed", "tool", decision.Tool, "error", err)
			return true, b.reply(ctx, chatID, "Не вдалося створити підтвердження. Спробуйте ще раз.", nil)
		}
		return true, nil
	}

	res, err := session.CallTool(ctx, toolInfo.Name, args)
	if err != nil {
		b.log.Error("telegram tool call failed", "tool", decision.Tool, "error", err)
		return true, b.reply(ctx, chatID, "Не вдалося виконати операцію: "+escape(truncate(err.Error(), 500)), nil)
	}
	if res.IsError {
		return true, b.reply(ctx, chatID, formatToolError(res.Text), nil)
	}

	switch decision.Tool {
	case "add_transactions":
		return true, b.handleAddToolResult(ctx, chatID, actor, res)
	case "create_user":
		return true, b.handleCreateUserToolResult(ctx, msg, actor, res)
	default:
		return true, b.sendToolResult(ctx, chatID, actor, decision.Tool, args, res)
	}
}

func (b *Bot) handleRegexFallback(ctx context.Context, msg *models.Message, user *domain.User, actor domain.Actor, text string) (bool, error) {
	intent, err := b.parser.ParseRegex(ctx, text)
	if err != nil {
		return false, nil
	}
	return true, b.handleIntent(ctx, msg, user, actor, intent, text)
}

func findTool(tools []mcp.ToolInfo, name string) (mcp.ToolInfo, bool) {
	for _, tool := range tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return mcp.ToolInfo{}, false
}

func injectOriginalText(args json.RawMessage, text string) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil || m == nil {
		m = map[string]any{}
	}
	m["original_text"] = truncate(text, 2000)
	out, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return out
}

func isDestructiveTool(tool string, args json.RawMessage) bool {
	switch tool {
	case "delete_transaction", "delete_category", "delete_account", "delete_snapshot":
		return true
	case "update_account":
		var m map[string]any
		_ = json.Unmarshal(args, &m)
		archived, _ := m["archived"].(bool)
		return archived
	case "update_user":
		var m map[string]any
		_ = json.Unmarshal(args, &m)
		active, ok := m["active"].(bool)
		return ok && !active
	}
	return false
}

func (b *Bot) askToolConfirmation(ctx context.Context, chatID int64, actor domain.Actor, tool string, args json.RawMessage) error {
	preview, err := b.destructivePreview(ctx, actor, tool, args)
	if err != nil {
		return b.reply(ctx, chatID, "Об'єкт для цієї дії не знайдено.", nil)
	}
	token, err := auth.NewToken(16)
	if err != nil {
		return err
	}
	action, err := b.svc.Store.CreateToolAction(ctx, nil, store.CreateToolActionInput{
		Token:               token,
		ChatID:              chatID,
		ActorTelegramUserID: actor.TelegramUserID,
		Action:              "confirm",
		ToolName:            tool,
		ArgumentsJSON:       string(args),
		PageSize:            toolPageSize,
		ExpiresAt:           b.svc.Now().Add(15 * time.Minute),
	})
	if err != nil {
		return err
	}
	text := "⚠️ <b>Підтвердження</b>\n\n" + preview + "\n\nВиконати?"
	markup := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "✅ Підтвердити", CallbackData: "tc:" + token},
		{Text: "❌ Скасувати", CallbackData: "tk:" + token},
	}}}
	sent, err := b.api.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: markup,
	})
	if err != nil {
		return err
	}
	return b.svc.Store.BindToolActionMessage(ctx, action.Token, int64(sent.ID))
}

func (b *Bot) destructivePreview(ctx context.Context, actor domain.Actor, tool string, args json.RawMessage) (string, error) {
	var m map[string]any
	_ = json.Unmarshal(args, &m)
	uid := stringArg(m, "uid")
	switch tool {
	case "delete_transaction":
		tx, err := b.svc.GetTransaction(ctx, actor, uid)
		if err != nil {
			return "", err
		}
		line := fmt.Sprintf("%s <b>%s</b> — %s · %s",
			escape(tx.CategoryEmoji), escape(tx.CategoryName),
			money.FormatAmount(tx.AmountMinor, tx.Currency), tx.OccurredOn.Format("02.01.2006"))
		if strings.TrimSpace(tx.Description) != "" && tx.Description != tx.CategoryName {
			line += "\n<i>" + escape(truncate(tx.Description, 160)) + "</i>"
		}
		line += "\nВласник: " + escape(tx.OwnerName)
		return "Видалити транзакцію?\n\n" + line, nil
	case "delete_category":
		cat, err := b.svc.Store.GetCategoryByUID(ctx, uid)
		if err != nil {
			return "", err
		}
		return "Видалити категорію «" + escape(cat.Name) + "» (" + escape(string(cat.Kind)) + ")?", nil
	case "delete_account":
		acc, err := b.svc.ResolveAccount(ctx, uid, "")
		if err != nil {
			return "", err
		}
		return "Видалити рахунок «" + escape(acc.Name) + "» (" + escape(acc.OwnerName) + ", " + escape(acc.Currency) + ")?", nil
	case "delete_snapshot":
		return "Видалити знімок заощаджень <code>" + escape(uid) + "</code>?", nil
	case "update_account":
		acc, err := b.svc.ResolveAccount(ctx, uid, "")
		if err != nil {
			return "", err
		}
		return "Архівувати рахунок «" + escape(acc.Name) + "» (" + escape(acc.OwnerName) + ", " + escape(acc.Currency) + ")?", nil
	case "update_user":
		u, err := b.svc.UserByUID(ctx, uid)
		if err != nil {
			return "", err
		}
		return "Деактивувати користувача «" + escape(u.DisplayName) + "» (@" + escape(u.Username) + ")?", nil
	}
	return "", fmt.Errorf("unknown destructive tool %s", tool)
}

func stringArg(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

func (b *Bot) handleAddToolResult(ctx context.Context, chatID int64, actor domain.Actor, res *mcp.ToolCallResult) error {
	var out mcp.AddTransactionsOutput
	if err := json.Unmarshal(res.Structured, &out); err != nil {
		return b.reply(ctx, chatID, "Транзакції створено.", nil)
	}
	uids := make([]string, 0, len(out.Created))
	for _, tx := range out.Created {
		uids = append(uids, tx.UID)
	}
	if len(uids) == 0 {
		return b.reply(ctx, chatID, "Не створено жодної транзакції.", nil)
	}
	return b.sendConfirmationUIDs(ctx, chatID, actor.TelegramUserID, uids, nil)
}

func (b *Bot) handleCreateUserToolResult(ctx context.Context, msg *models.Message, actor domain.Actor, res *mcp.ToolCallResult) error {
	var out mcp.CreateUserOutput
	if err := json.Unmarshal(res.Structured, &out); err != nil {
		return b.reply(ctx, msg.Chat.ID, "Користувача створено.", nil)
	}
	text := fmt.Sprintf("✅ Користувача <b>%s</b> (@%s) створено.",
		escape(out.User.DisplayName), escape(out.User.Username))
	if out.TemporaryPassword == "" {
		return b.reply(ctx, msg.Chat.ID, text, nil)
	}
	secret := "\nТимчасовий пароль: <code>" + escape(out.TemporaryPassword) + "</code>"
	if msg.Chat.Type == models.ChatTypePrivate {
		return b.reply(ctx, msg.Chat.ID, text+secret, nil)
	}
	if dmErr := b.reply(ctx, actor.TelegramUserID, text+secret, nil); dmErr != nil {
		text += "\n⚠️ Не вдалося надіслати пароль у DM. Скористайтеся веб-UI, щоб змінити пароль."
	} else {
		text += "\n🔐 Пароль надіслано в DM."
	}
	return b.reply(ctx, msg.Chat.ID, text, nil)
}

func (b *Bot) sendToolResult(ctx context.Context, chatID int64, actor domain.Actor, tool string, args json.RawMessage, res *mcp.ToolCallResult) error {
	render, err := buildToolRender(tool, res.Structured)
	if err != nil {
		if strings.TrimSpace(res.Text) != "" {
			return b.sendChunked(ctx, chatID, escape(res.Text), nil)
		}
		return b.reply(ctx, chatID, "✅ Готово.", nil)
	}
	needsPage := false
	if render.ServerPaged {
		needsPage = render.NextCursor != ""
	} else {
		needsPage = len(render.Rows) > toolPageSize
	}
	if needsPage {
		return b.sendToolPage(ctx, chatID, actor, tool, args, res.Structured, render)
	}
	return b.sendChunked(ctx, chatID, render.text(0), render.markup(0, ""))
}

func pageSizeFor(tool string, args json.RawMessage) int {
	if tool != "list_transactions" {
		return toolPageSize
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return toolPageSize
	}
	if n, ok := m["limit"].(float64); ok && n >= 1 && n <= 30 {
		return int(n)
	}
	return toolPageSize
}

func (b *Bot) sendToolPage(ctx context.Context, chatID int64, actor domain.Actor, tool string, args, raw json.RawMessage, render *toolRender) error {
	token, err := auth.NewToken(16)
	if err != nil {
		return err
	}
	pageSize := pageSizeFor(tool, args)
	resultJSON := ""
	nextCursor := ""
	if render.ServerPaged {
		nextCursor = render.NextCursor
	} else {
		resultJSON = string(raw)
	}
	action, err := b.svc.Store.CreateToolAction(ctx, nil, store.CreateToolActionInput{
		Token:               token,
		ChatID:              chatID,
		ActorTelegramUserID: actor.TelegramUserID,
		Action:              "page",
		ToolName:            tool,
		ArgumentsJSON:       string(args),
		ResultJSON:          resultJSON,
		NextCursor:          nextCursor,
		PageSize:            pageSize,
		PageNumber:          0,
		ExpiresAt:           b.svc.Now().Add(24 * time.Hour),
	})
	if err != nil {
		return err
	}
	markup := render.markup(0, token)
	sent, err := b.api.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        render.text(0),
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: markup,
	})
	if err != nil {
		return err
	}
	return b.svc.Store.BindToolActionMessage(ctx, action.Token, int64(sent.ID))
}

func (b *Bot) sendChunked(ctx context.Context, chatID int64, text string, markup models.ReplyMarkup) error {
	chunks := splitMessage(text, 3800)
	for i, chunk := range chunks {
		var m models.ReplyMarkup
		if i == len(chunks)-1 {
			m = markup
		}
		if err := b.reply(ctx, chatID, chunk, m); err != nil {
			return err
		}
	}
	return nil
}

func splitMessage(text string, limit int) []string {
	if limit <= 0 {
		return []string{text}
	}
	var chunks []string
	var current strings.Builder
	currentRunes := 0
	flush := func() {
		if currentRunes > 0 {
			chunks = append(chunks, strings.TrimRight(current.String(), "\n"))
			current.Reset()
			currentRunes = 0
		}
	}
	for _, line := range strings.Split(text, "\n") {
		runes := []rune(line)
		for len(runes) > limit {
			flush()
			chunks = append(chunks, string(runes[:limit]))
			runes = runes[limit:]
		}
		lineRunes := len(runes)
		if currentRunes > 0 && currentRunes+1+lineRunes > limit {
			flush()
		}
		if currentRunes > 0 {
			current.WriteByte('\n')
			currentRunes++
		}
		current.WriteString(string(runes))
		currentRunes += lineRunes
	}
	flush()
	if len(chunks) == 0 {
		chunks = append(chunks, "")
	}
	return chunks
}

func isToolCallback(data string) bool {
	for _, prefix := range []string{"tc:", "tk:", "tp:", "td:"} {
		if strings.HasPrefix(data, prefix) {
			return true
		}
	}
	return false
}

func (b *Bot) handleToolCallback(ctx context.Context, cq *models.CallbackQuery, actor domain.Actor) error {
	data := cq.Data
	switch {
	case strings.HasPrefix(data, "td:"):
		return b.handleRowDeleteCallback(ctx, cq, actor, strings.TrimPrefix(data, "td:"))
	case strings.HasPrefix(data, "tc:"):
		return b.handleToolConfirmCallback(ctx, cq, actor, strings.TrimPrefix(data, "tc:"))
	case strings.HasPrefix(data, "tk:"):
		return b.handleToolCancelCallback(ctx, cq, actor, strings.TrimPrefix(data, "tk:"))
	case strings.HasPrefix(data, "tp:"):
		return b.handleToolPageCallback(ctx, cq, actor, strings.TrimPrefix(data, "tp:"))
	}
	return b.answerCallback(ctx, cq, "Невідома дія.")
}

func (b *Bot) handleRowDeleteCallback(ctx context.Context, cq *models.CallbackQuery, actor domain.Actor, uid string) error {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return b.answerCallback(ctx, cq, "Транзакцію не знайдено.")
	}
	tx, err := b.svc.GetTransaction(ctx, actor, uid)
	if err != nil || tx.Deleted() {
		return b.answerCallback(ctx, cq, "Транзакцію не знайдено.")
	}
	args, _ := json.Marshal(map[string]string{"uid": uid})
	chatID := cq.Message.Message.Chat.ID
	if err := b.askToolConfirmation(ctx, chatID, actor, "delete_transaction", args); err != nil {
		b.log.Warn("row delete confirmation failed", "error", err)
		return b.answerCallback(ctx, cq, "Не вдалося створити підтвердження.")
	}
	return b.answerCallback(ctx, cq, "Підтвердіть видалення.")
}

func (b *Bot) handleToolConfirmCallback(ctx context.Context, cq *models.CallbackQuery, actor domain.Actor, token string) error {
	chatID := cq.Message.Message.Chat.ID
	action, err := b.svc.Store.GetToolAction(ctx, token)
	if err != nil {
		return b.answerCallback(ctx, cq, "Кнопка застаріла.")
	}
	if action.ChatID != chatID || action.ActorTelegramUserID != cq.From.ID || action.Action != "confirm" {
		return b.answerCallback(ctx, cq, "Немає доступу до цієї дії.")
	}
	used, err := b.svc.Store.UseToolAction(ctx, token, b.svc.Now())
	if err != nil || !used {
		return b.answerCallback(ctx, cq, "Кнопка вже неактивна.")
	}
	text, execErr := b.executeConfirmedTool(ctx, actor, action)
	if execErr != nil {
		b.log.Warn("confirmed tool failed", "tool", action.ToolName, "error", execErr)
		text = "⚠️ Не вдалося виконати операцію."
	}
	b.editToolMessage(ctx, chatID, int64(cq.Message.Message.ID), text, nil)
	return b.answerCallback(ctx, cq, "Готово")
}

func (b *Bot) handleToolCancelCallback(ctx context.Context, cq *models.CallbackQuery, actor domain.Actor, token string) error {
	chatID := cq.Message.Message.Chat.ID
	action, err := b.svc.Store.GetToolAction(ctx, token)
	if err != nil {
		return b.answerCallback(ctx, cq, "Кнопка застаріла.")
	}
	if action.ChatID != chatID || action.ActorTelegramUserID != cq.From.ID || action.Action != "confirm" {
		return b.answerCallback(ctx, cq, "Немає доступу до цієї дії.")
	}
	cancelled, err := b.svc.Store.CancelToolAction(ctx, token, b.svc.Now())
	if err != nil || !cancelled {
		return b.answerCallback(ctx, cq, "Кнопка вже неактивна.")
	}
	b.editToolMessage(ctx, chatID, int64(cq.Message.Message.ID), "❌ Скасовано.", nil)
	return b.answerCallback(ctx, cq, "Скасовано")
}

func (b *Bot) handleToolPageCallback(ctx context.Context, cq *models.CallbackQuery, actor domain.Actor, token string) error {
	chatID := cq.Message.Message.Chat.ID
	action, err := b.svc.Store.GetToolAction(ctx, token)
	if err != nil {
		return b.answerCallback(ctx, cq, "Кнопка застаріла.")
	}
	if action.ChatID != chatID || action.ActorTelegramUserID != cq.From.ID || action.Action != "page" {
		return b.answerCallback(ctx, cq, "Немає доступу до цієї дії.")
	}
	nextPage := action.PageNumber + 1
	offset := nextPage * action.PageSize
	var (
		text       string
		markup     models.ReplyMarkup
		nextCursor string
	)
	if action.ToolName == "list_transactions" {
		if action.NextCursor == "" {
			return b.answerCallback(ctx, cq, "Більше сторінок немає.")
		}
		sessionCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		session, err := b.mcpServer.NewToolSession(sessionCtx, actor)
		if err != nil {
			return b.answerCallback(ctx, cq, "Не вдалося завантажити сторінку.")
		}
		defer session.Close()
		var args map[string]any
		if err := json.Unmarshal([]byte(action.ArgumentsJSON), &args); err != nil {
			args = map[string]any{}
		}
		if action.NextCursor != "" {
			args["cursor"] = action.NextCursor
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return b.answerCallback(ctx, cq, "Не вдалося завантажити сторінку.")
		}
		res, err := session.CallTool(ctx, action.ToolName, raw)
		if err != nil || res.IsError {
			return b.answerCallback(ctx, cq, "Не вдалося завантажити сторінку.")
		}
		render, err := buildToolRender(action.ToolName, res.Structured)
		if err != nil {
			return b.answerCallback(ctx, cq, "Не вдалося завантажити сторінку.")
		}
		nextCursor = render.NextCursor
		text = render.text(offset)
		markup = render.markup(offset, token)
	} else {
		render, err := buildToolRender(action.ToolName, json.RawMessage(action.ResultJSON))
		if err != nil || offset >= len(render.Rows) {
			return b.answerCallback(ctx, cq, "Більше сторінок немає.")
		}
		text = render.text(offset)
		markup = render.markup(offset, token)
	}
	updated, err := b.svc.Store.AdvanceToolPage(ctx, token, action.PageNumber, nextCursor, b.svc.Now())
	if err != nil || !updated {
		return b.answerCallback(ctx, cq, "Кнопка вже неактивна.")
	}
	b.editToolMessage(ctx, chatID, int64(cq.Message.Message.ID), text, markup)
	return b.answerCallback(ctx, cq, "Сторінку завантажено")
}

func (b *Bot) executeConfirmedTool(ctx context.Context, actor domain.Actor, action *domain.TelegramToolAction) (string, error) {
	sessionCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session, err := b.mcpServer.NewToolSession(sessionCtx, actor)
	if err != nil {
		return "", err
	}
	defer session.Close()
	res, err := session.CallTool(ctx, action.ToolName, json.RawMessage(action.ArgumentsJSON))
	if err != nil {
		return "", err
	}
	if res.IsError {
		return formatToolError(res.Text), nil
	}
	render, err := buildToolRender(action.ToolName, res.Structured)
	if err != nil {
		return "✅ Готово.", nil
	}
	return render.text(0), nil
}

func (b *Bot) editToolMessage(ctx context.Context, chatID, messageID int64, text string, markup models.ReplyMarkup) {
	if messageID == 0 {
		_ = b.reply(ctx, chatID, text, markup)
		return
	}
	if markup == nil {
		markup = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}}
	}
	_, err := b.api.EditMessageText(ctx, &tgbot.EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   int(messageID),
		Text:        text,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: markup,
	})
	if err == nil {
		return
	}
	// The message may be too old to edit; send a fresh one instead.
	_ = b.reply(ctx, chatID, text, markup)
}

func formatToolError(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return "⚠️ Не вдалося виконати операцію."
	}
	lower := strings.ToLower(t)
	switch {
	case strings.Contains(lower, "forbidden"):
		return "⛔ Недостатньо прав."
	case strings.Contains(lower, "not_found"):
		return "🔍 Не знайдено."
	case strings.Contains(lower, "idempotency_conflict"):
		return "⚠️ Конфлікт повторного запиту."
	}
	return "⚠️ " + escape(truncate(t, 1000))
}
