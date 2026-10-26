package telegram

import (
	"strings"

	"github.com/go-telegram/bot/models"
)

// isAddressedToBot reports whether a message should enter full tool mode. In a
// private chat every message does; in a group the bot must be mentioned or
// explicitly replied to.
func (b *Bot) isAddressedToBot(msg *models.Message) bool {
	if msg == nil {
		return false
	}
	if msg.Chat.Type == models.ChatTypePrivate {
		return true
	}
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil && msg.ReplyToMessage.From.IsBot && msg.ReplyToMessage.From.ID == b.api.ID() {
		return true
	}
	for _, entity := range msg.Entities {
		if entity.Type == models.MessageEntityTypeTextMention && entity.User != nil && entity.User.ID == b.api.ID() {
			return true
		}
	}
	if b.username == "" {
		return false
	}
	lower := strings.ToLower(msg.Text)
	mention := "@" + strings.ToLower(b.username)
	offset := 0
	for {
		i := strings.Index(lower[offset:], mention)
		if i < 0 {
			return false
		}
		i += offset
		end := i + len(mention)
		if (i == 0 || !isUsernameChar(lower[i-1])) && (end == len(lower) || !isUsernameChar(lower[end])) {
			return true
		}
		offset = end
	}
}

// stripBotMention removes @botname tokens from the message before it is sent
// to the LLM. Username characters after the mention are treated as part of a
// different mention (for example @botname2).
func (b *Bot) stripBotMention(text string) string {
	if b.username == "" {
		return strings.TrimSpace(text)
	}
	mention := strings.ToLower("@" + b.username)
	lower := strings.ToLower(text)
	var out strings.Builder
	last := 0
	offset := 0
	for {
		i := strings.Index(lower[offset:], mention)
		if i < 0 {
			break
		}
		i += offset
		end := i + len(mention)
		if (i > 0 && isUsernameChar(lower[i-1])) || (end != len(lower) && isUsernameChar(lower[end])) {
			out.WriteString(text[last:end])
			last = end
			offset = end
			continue
		}
		out.WriteString(text[last:i])
		last = end
		offset = end
	}
	out.WriteString(text[last:])
	return strings.TrimSpace(out.String())
}

func isUsernameChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}
