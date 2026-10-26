package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"skarb/internal/auth"
	"skarb/internal/domain"
	"skarb/internal/money"
)

type confirmationItem struct {
	UID string `json:"uid"`
}

func (b *Bot) sendConfirmation(ctx context.Context, chatID, actorTG int64, txs []domain.Transaction, warnings []string) error {
	uids := make([]string, 0, len(txs))
	for _, tx := range txs {
		uids = append(uids, tx.UID)
	}
	return b.sendConfirmationUIDs(ctx, chatID, actorTG, uids, warnings)
}

func (b *Bot) sendConfirmationUIDs(ctx context.Context, chatID, actorTG int64, uids []string, warnings []string) error {
	items := make([]confirmationItem, 0, len(uids))
	for _, uid := range uids {
		items = append(items, confirmationItem{UID: uid})
	}
	conf, err := b.svc.Store.CreateConfirmation(ctx, nil, chatID, 0, actorTG, jsonItems(items))
	if err != nil {
		return err
	}
	text, markup, err := b.renderConfirmation(ctx, conf, items)
	if err != nil {
		return err
	}
	if len(warnings) > 0 {
		text = "⚠️ " + escape(strings.Join(warnings, "\n⚠️ ")) + "\n\n" + text
	}
	var msg *models.Message
	for attempt := 0; attempt < 3; attempt++ {
		msg, err = b.api.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID:      chatID,
			Text:        text,
			ParseMode:   models.ParseModeHTML,
			ReplyMarkup: markup,
		})
		if err == nil {
			break
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * time.Second):
			}
		}
	}
	if err != nil {
		return err
	}
	return b.svc.Store.UpdateConfirmation(ctx, nil, conf.ID, int64(msg.ID), jsonItems(items))
}

func (b *Bot) renderConfirmation(ctx context.Context, conf *domain.TelegramConfirmation, items []confirmationItem) (string, models.ReplyMarkup, error) {
	if err := b.svc.Store.ClearOpenActions(ctx, conf.ID); err != nil {
		return "", nil, err
	}
	var lines []string
	var keyboard [][]models.InlineKeyboardButton
	expires := b.svc.Now().Add(24 * time.Hour)
	for i, item := range items {
		tx, err := b.svc.Store.GetTransactionByUID(ctx, item.UID)
		if err != nil {
			continue
		}
		n := i + 1
		lines = append(lines, confirmationLine(n, tx))
		if tx.Deleted() {
			continue
		}
		undoTok, err := auth.NewToken(16)
		if err != nil {
			return "", nil, err
		}
		commentTok, err := auth.NewToken(16)
		if err != nil {
			return "", nil, err
		}
		categoryTok, err := auth.NewToken(16)
		if err != nil {
			return "", nil, err
		}
		undo := "u:" + undoTok
		comment := "c:" + commentTok
		category := "k:" + categoryTok
		if err := b.svc.Store.CreateAction(ctx, nil, undo, conf.ID, tx.ID, "undo", conf.ActorTelegramUserID, expires); err != nil {
			return "", nil, err
		}
		if err := b.svc.Store.CreateAction(ctx, nil, comment, conf.ID, tx.ID, "comment", conf.ActorTelegramUserID, expires); err != nil {
			return "", nil, err
		}
		if err := b.svc.Store.CreateAction(ctx, nil, category, conf.ID, tx.ID, "category", conf.ActorTelegramUserID, expires); err != nil {
			return "", nil, err
		}
		keyboard = append(keyboard, []models.InlineKeyboardButton{
			{Text: fmt.Sprintf("↩ Скасувати %d", n), CallbackData: undo},
			{Text: fmt.Sprintf("✏️ Коментар %d", n), CallbackData: comment},
			{Text: fmt.Sprintf("🏷 Категорія %d", n), CallbackData: category},
		})
	}
	text := confirmationText(lines)
	if keyboard == nil {
		keyboard = [][]models.InlineKeyboardButton{}
	}
	markup := &models.InlineKeyboardMarkup{InlineKeyboard: keyboard}
	return text, markup, nil
}

func confirmationLine(n int, tx *domain.Transaction) string {
	if tx.Deleted() {
		return fmt.Sprintf("%d. ❌ <s>%s %s — %s</s> · скасовано",
			n, escape(tx.CategoryEmoji), escape(tx.CategoryName), money.FormatAmount(tx.AmountMinor, tx.Currency))
	}
	line := fmt.Sprintf("%d. %s <b>%s</b> — %s · %s",
		n, escape(tx.CategoryEmoji), escape(tx.CategoryName),
		money.FormatAmount(tx.AmountMinor, tx.Currency), tx.OccurredOn.Format("02.01"))
	if strings.TrimSpace(tx.Note) != "" {
		line += "\n   <i>" + escape(tx.Note) + "</i>"
	} else if tx.Description != "" && tx.Description != tx.CategoryName {
		line += "\n   <i>" + escape(tx.Description) + "</i>"
	}
	return line
}

func confirmationText(lines []string) string {
	if len(lines) == 0 {
		lines = []string{"Записів немає."}
	}
	return "✅ <b>Записано</b>\n\n" + strings.Join(lines, "\n")
}

func (b *Bot) editConfirmation(ctx context.Context, conf *domain.TelegramConfirmation, items []confirmationItem) error {
	text, markup, err := b.renderConfirmation(ctx, conf, items)
	if err != nil {
		return err
	}
	if err := b.svc.Store.UpdateConfirmation(ctx, nil, conf.ID, conf.MessageID, jsonItems(items)); err != nil {
		return err
	}
	_, err = b.api.EditMessageText(ctx, &tgbot.EditMessageTextParams{
		ChatID:      conf.ChatID,
		MessageID:   int(conf.MessageID),
		Text:        text,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: markup,
	})
	return err
}

func (b *Bot) handleUndoCallback(ctx context.Context, chatID int64, actor domain.Actor, action *domain.TelegramAction, cq *models.CallbackQuery) error {
	conf, err := b.svc.Store.GetConfirmation(ctx, action.ConfirmationID)
	if err != nil || conf.ChatID != chatID {
		return b.answerCallback(ctx, cq, "Повідомлення недоступне.")
	}
	tx, err := b.svc.Store.GetTransactionByID(ctx, action.TransactionID)
	if err != nil {
		return b.answerCallback(ctx, cq, "Транзакцію не знайдено.")
	}
	restored, err := b.svc.UndoTransaction(ctx, actor, tx.UID)
	if err != nil {
		return b.answerCallback(ctx, cq, "Не вдалося скасувати: "+err.Error())
	}
	items := parseItems(conf.ItemsJSON)
	if restored != nil {
		for i := range items {
			if items[i].UID == tx.UID {
				items[i].UID = restored.UID
				break
			}
		}
	}
	if err := b.editConfirmation(ctx, conf, items); err != nil {
		b.log.Warn("edit confirmation failed", "error", redactToken(err, b.cfg.TelegramBotToken))
	}
	_ = b.reply(ctx, chatID, "↩ Скасовано: "+escape(tx.CategoryName)+" "+money.FormatAmount(tx.AmountMinor, tx.Currency), nil)
	return b.answerCallback(ctx, cq, "Скасовано")
}

func (b *Bot) handleCommentCallback(ctx context.Context, chatID int64, actor domain.Actor, action *domain.TelegramAction, cq *models.CallbackQuery) error {
	conf, err := b.svc.Store.GetConfirmation(ctx, action.ConfirmationID)
	if err != nil || conf.ChatID != chatID {
		return b.answerCallback(ctx, cq, "Повідомлення недоступне.")
	}
	tx, err := b.svc.Store.GetTransactionByID(ctx, action.TransactionID)
	if err != nil || tx.Deleted() {
		return b.answerCallback(ctx, cq, "Транзакція вже недоступна.")
	}
	prompt, err := b.api.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID: chatID,
		Text: fmt.Sprintf("✏️ <a href=\"tg://user?id=%d\">Коментар</a> до «%s» — %s. Напишіть текст у відповіді на це повідомлення.",
			actor.TelegramUserID, escape(tx.CategoryName), money.FormatAmount(tx.AmountMinor, tx.Currency)),
		ParseMode: models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{
			MessageID:                int(conf.MessageID),
			AllowSendingWithoutReply: true,
		},
		ReplyMarkup: &models.ForceReply{ForceReply: true, Selective: true},
	})
	if err != nil {
		return b.answerCallback(ctx, cq, "Не вдалося відкрити коментар.")
	}
	if err := b.svc.Store.CreatePendingEdit(ctx, chatID, int64(prompt.ID), conf.ID, tx.ID, actor.TelegramUserID, b.svc.Now().Add(15*time.Minute)); err != nil {
		return b.answerCallback(ctx, cq, "Не вдалося зберегти стан коментаря.")
	}
	return b.answerCallback(ctx, cq, "Напишіть коментар у відповіді.")
}

func (b *Bot) tryCommentReply(ctx context.Context, msg *models.Message, actor domain.Actor) (bool, error) {
	if msg.ReplyToMessage == nil {
		return false, nil
	}
	pending, err := b.svc.Store.GetPendingEdit(ctx, msg.Chat.ID, int64(msg.ReplyToMessage.ID))
	if err != nil {
		return false, nil
	}
	if pending.ActorTelegramUserID != msg.From.ID {
		_ = b.reply(ctx, msg.Chat.ID, "Цей запит коментаря адресовано іншому користувачу.", nil)
		return true, nil
	}
	if b.svc.Now().After(pending.ExpiresAt) {
		_ = b.svc.Store.DeletePendingEdit(ctx, msg.Chat.ID, int64(msg.ReplyToMessage.ID))
		_ = b.reply(ctx, msg.Chat.ID, "Час на коментар вичерпано. Натисніть «Коментар» ще раз.", nil)
		return true, nil
	}
	tx, err := b.svc.Store.GetTransactionByID(ctx, pending.TransactionID)
	if err != nil || tx.Deleted() {
		_ = b.svc.Store.DeletePendingEdit(ctx, msg.Chat.ID, int64(msg.ReplyToMessage.ID))
		_ = b.reply(ctx, msg.Chat.ID, "Транзакція вже недоступна.", nil)
		return true, nil
	}
	updated, err := b.svc.EditTransactionNote(ctx, actor, tx.UID, msg.Text)
	if err != nil {
		_ = b.reply(ctx, msg.Chat.ID, "Не вдалося зберегти коментар: "+escape(err.Error()), nil)
		return true, err
	}
	conf, err := b.svc.Store.GetConfirmation(ctx, pending.ConfirmationID)
	if err != nil {
		return true, err
	}
	items := parseItems(conf.ItemsJSON)
	for i := range items {
		if items[i].UID == tx.UID {
			items[i].UID = updated.UID
			break
		}
	}
	if err := b.editConfirmation(ctx, conf, items); err != nil {
		b.log.Warn("edit confirmation after comment failed", "error", redactToken(err, b.cfg.TelegramBotToken))
	}
	_ = b.svc.Store.DeletePendingEdit(ctx, msg.Chat.ID, int64(msg.ReplyToMessage.ID))
	if _, err := b.api.EditMessageText(ctx, &tgbot.EditMessageTextParams{
		ChatID:      msg.Chat.ID,
		MessageID:   msg.ReplyToMessage.ID,
		Text:        "✅ Коментар збережено.",
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}},
	}); err != nil {
		b.log.Warn("edit comment prompt failed", "error", redactToken(err, b.cfg.TelegramBotToken))
	}
	return true, nil
}

// handleCategoryCallback drives the "change category" button. The token is
// reused across the flow: "k:<tok>" opens the picker, "k:<tok>|<categoryID>"
// applies a choice and "k:<tok>|back" restores the confirmation. Only applying
// a choice consumes the token.
func (b *Bot) handleCategoryCallback(ctx context.Context, chatID int64, actor domain.Actor, action *domain.TelegramAction, cq *models.CallbackQuery, arg string) error {
	conf, err := b.svc.Store.GetConfirmation(ctx, action.ConfirmationID)
	if err != nil || conf.ChatID != chatID {
		return b.answerCallback(ctx, cq, "Повідомлення недоступне.")
	}
	tx, err := b.svc.Store.GetTransactionByID(ctx, action.TransactionID)
	if err != nil || tx.Deleted() {
		return b.answerCallback(ctx, cq, "Транзакція вже недоступна.")
	}
	items := parseItems(conf.ItemsJSON)
	if arg == "" {
		return b.showCategoryPicker(ctx, conf, items, tx, action.Token, actor, cq)
	}
	if arg == "back" {
		if err := b.editConfirmation(ctx, conf, items); err != nil {
			b.log.Warn("edit confirmation failed", "error", redactToken(err, b.cfg.TelegramBotToken))
		}
		return b.answerCallback(ctx, cq, "")
	}
	categoryID, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return b.answerCallback(ctx, cq, "Невідома дія.")
	}
	if categoryID == tx.CategoryID {
		if err := b.editConfirmation(ctx, conf, items); err != nil {
			b.log.Warn("edit confirmation failed", "error", redactToken(err, b.cfg.TelegramBotToken))
		}
		return b.answerCallback(ctx, cq, "Категорію не змінено.")
	}
	used, err := b.svc.Store.UseAction(ctx, action.Token)
	if err != nil || !used {
		return b.answerCallback(ctx, cq, "Кнопка вже використана.")
	}
	updated, err := b.svc.EditTransactionCategory(ctx, actor, tx.UID, categoryID)
	if err != nil {
		return b.answerCallback(ctx, cq, "Не вдалося змінити категорію: "+err.Error())
	}
	for i := range items {
		if items[i].UID == tx.UID {
			items[i].UID = updated.UID
			break
		}
	}
	if err := b.editConfirmation(ctx, conf, items); err != nil {
		b.log.Warn("edit confirmation after category change failed", "error", redactToken(err, b.cfg.TelegramBotToken))
	}
	return b.answerCallback(ctx, cq, "Категорія: "+updated.CategoryName)
}

func (b *Bot) showCategoryPicker(ctx context.Context, conf *domain.TelegramConfirmation, items []confirmationItem, tx *domain.Transaction, token string, actor domain.Actor, cq *models.CallbackQuery) error {
	categories, err := b.svc.ListCategories(ctx, actor, false)
	if err != nil {
		return b.answerCallback(ctx, cq, "Не вдалося завантажити категорії.")
	}
	var lines []string
	n := 0
	for i, item := range items {
		cur, err := b.svc.Store.GetTransactionByUID(ctx, item.UID)
		if err != nil {
			continue
		}
		lines = append(lines, confirmationLine(i+1, cur))
		if cur.ID == tx.ID {
			n = i + 1
		}
	}
	current := strings.TrimSpace(tx.CategoryEmoji + " " + tx.CategoryName)
	text := confirmationText(lines) + fmt.Sprintf("\n\n🏷 <b>Нова категорія для %d</b> (%s — %s):",
		n, escape(current), money.FormatAmount(tx.AmountMinor, tx.Currency))
	_, err = b.api.EditMessageText(ctx, &tgbot.EditMessageTextParams{
		ChatID:      conf.ChatID,
		MessageID:   int(conf.MessageID),
		Text:        text,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: categoryPickerMarkup(categories, tx, token),
	})
	if err != nil {
		b.log.Warn("show category picker failed", "error", redactToken(err, b.cfg.TelegramBotToken))
		return b.answerCallback(ctx, cq, "Не вдалося відкрити список категорій.")
	}
	return b.answerCallback(ctx, cq, "Оберіть категорію.")
}

// categoryPickerMarkup lists the active categories of the transaction's kind,
// two per row, with the current one checked.
func categoryPickerMarkup(categories []domain.Category, tx *domain.Transaction, token string) *models.InlineKeyboardMarkup {
	var keyboard [][]models.InlineKeyboardButton
	var row []models.InlineKeyboardButton
	for _, c := range categories {
		if c.Kind != tx.Kind {
			continue
		}
		label := strings.TrimSpace(c.Emoji + " " + c.Name)
		if c.ID == tx.CategoryID {
			label = "✓ " + label
		}
		row = append(row, models.InlineKeyboardButton{
			Text:         label,
			CallbackData: fmt.Sprintf("%s|%d", token, c.ID),
		})
		if len(row) == 2 {
			keyboard = append(keyboard, row)
			row = nil
		}
	}
	if len(row) > 0 {
		keyboard = append(keyboard, row)
	}
	keyboard = append(keyboard, []models.InlineKeyboardButton{{Text: "← Назад", CallbackData: token + "|back"}})
	return &models.InlineKeyboardMarkup{InlineKeyboard: keyboard}
}
