package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"skarb/internal/domain"
)

const llmSettingKey = "llm.config"

type LLMConfig struct {
	BaseURL         string  `json:"base_url"`
	Model           string  `json:"model"`
	SchemaMode      string  `json:"schema_mode"`
	TimeoutSeconds  int     `json:"timeout_seconds"`
	MaxOutputTokens int     `json:"max_output_tokens"`
	MaxTransactions int     `json:"max_transactions"`
	MaxInputChars   int     `json:"max_input_chars"`
	Temperature     float64 `json:"temperature"`
}

func DefaultLLMConfig() LLMConfig {
	return LLMConfig{
		SchemaMode:      "auto",
		TimeoutSeconds:  15,
		MaxOutputTokens: 800,
		MaxTransactions: 10,
		MaxInputChars:   2000,
		Temperature:     0.1,
	}
}

func (s *Service) GetLLMConfig(ctx context.Context) (LLMConfig, error) {
	cfg := DefaultLLMConfig()
	raw, ok, err := s.Store.GetSetting(ctx, llmSettingKey)
	if err != nil {
		return cfg, err
	}
	if !ok {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return DefaultLLMConfig(), nil
	}
	return cfg, nil
}

func (s *Service) LLMConfigured(cfg LLMConfig) bool {
	return strings.TrimSpace(cfg.BaseURL) != "" && strings.TrimSpace(cfg.Model) != ""
}

func (s *Service) SetLLMConfig(ctx context.Context, actor domain.Actor, cfg LLMConfig) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.BaseURL != "" {
		u, err := url.ParseRequestURI(cfg.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return validation("base_url", "base URL must be an http(s) URL")
		}
	}
	if cfg.Model == "" && cfg.BaseURL != "" {
		return validation("model", "model is required when a base URL is set")
	}
	switch cfg.SchemaMode {
	case "", "auto":
		cfg.SchemaMode = "auto"
	case "json_schema", "json_object", "prompt":
	default:
		return validation("schema_mode", "schema mode must be auto, json_schema, json_object, or prompt")
	}
	if cfg.TimeoutSeconds < 5 || cfg.TimeoutSeconds > 120 {
		return validation("timeout_seconds", "timeout must be between 5 and 120 seconds")
	}
	if cfg.MaxOutputTokens < 100 || cfg.MaxOutputTokens > 4000 {
		return validation("max_output_tokens", "output cap must be between 100 and 4000")
	}
	if cfg.MaxTransactions < 1 || cfg.MaxTransactions > 20 {
		return validation("max_transactions", "max transactions must be between 1 and 20")
	}
	if cfg.MaxInputChars < 100 || cfg.MaxInputChars > 10000 {
		return validation("max_input_chars", "max input must be between 100 and 10000 characters")
	}
	if cfg.Temperature < 0 || cfg.Temperature > 1 {
		return validation("temperature", "temperature must be between 0 and 1")
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := s.Store.SetSetting(ctx, tx, llmSettingKey, string(b), &actor.UserID); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "settings.llm_updated", "settings", llmSettingKey, nil, cfg)
	})
	return err
}

func (s *Service) ListTelegramChats(ctx context.Context, actor domain.Actor) ([]domain.TelegramChat, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	return s.Store.ListTelegramChats(ctx)
}

func (s *Service) AddTelegramChat(ctx context.Context, actor domain.Actor, chatID int64, title, chatType string) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	if chatID == 0 {
		return validation("chat_id", "chat id is required")
	}
	chatType = strings.TrimSpace(chatType)
	if chatType == "" {
		chatType = "group"
	}
	if chatType != "group" && chatType != "supergroup" {
		return validation("chat_type", "only groups and supergroups are supported")
	}
	if err := s.Store.AddTelegramChat(ctx, chatID, strings.TrimSpace(title), chatType, &actor.UserID); err != nil {
		return err
	}
	return s.audit(ctx, nil, actor, "telegram.chat_added", "telegram_chat", fmt.Sprint(chatID), nil, map[string]any{"title": title})
}

func (s *Service) RemoveTelegramChat(ctx context.Context, actor domain.Actor, chatID int64) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	if err := s.Store.RemoveTelegramChat(ctx, chatID); err != nil {
		return err
	}
	return s.audit(ctx, nil, actor, "telegram.chat_removed", "telegram_chat", fmt.Sprint(chatID), nil, nil)
}

func (s *Service) IsTelegramChatAllowed(ctx context.Context, chatID int64) (bool, error) {
	return s.Store.IsTelegramChatAllowed(ctx, chatID)
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func ValidateCron(expr, tz string) error {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return fmt.Errorf("cron must have exactly five fields")
	}
	expr = strings.Join(fields, " ")
	if _, err := cronParser.Parse(expr); err != nil {
		return fmt.Errorf("invalid cron: %w", err)
	}
	if tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return fmt.Errorf("invalid timezone: %w", err)
		}
	}
	return nil
}

func (s *Service) ListReminderConfigs(ctx context.Context, actor domain.Actor) ([]domain.ReminderConfig, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	return s.Store.ListReminderConfigs(ctx)
}

func (s *Service) GetReminderConfig(ctx context.Context, kind string) (*domain.ReminderConfig, error) {
	return s.Store.GetReminderConfig(ctx, kind)
}

func (s *Service) UpdateReminderConfig(ctx context.Context, actor domain.Actor, cfg domain.ReminderConfig) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	if cfg.Kind != "transactions" && cfg.Kind != "snapshot" {
		return validation("kind", "reminder kind must be transactions or snapshot")
	}
	if strings.TrimSpace(cfg.Timezone) == "" {
		cfg.Timezone = "Europe/Kyiv"
	}
	if err := ValidateCron(cfg.CronExpr, cfg.Timezone); err != nil {
		return validation("cron", err.Error())
	}
	if cfg.WindowDays < 1 || cfg.WindowDays > 365 {
		return validation("window_days", "window must be between 1 and 365 days")
	}
	if cfg.TargetUserID != nil {
		u, err := s.Store.GetUserByID(ctx, *cfg.TargetUserID)
		if err != nil || !u.IsActive {
			return validation("target_user", "target user not found or inactive")
		}
	}
	if cfg.TargetChatID != nil {
		allowed, err := s.Store.IsTelegramChatAllowed(ctx, *cfg.TargetChatID)
		if err != nil {
			return err
		}
		if !allowed {
			return validation("target_chat", "target chat is not allowlisted")
		}
	}
	var before *domain.ReminderConfig
	before, _ = s.Store.GetReminderConfig(ctx, cfg.Kind)
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := s.Store.UpdateReminderConfig(ctx, tx, cfg); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "reminder.updated", "reminder", cfg.Kind, before, cfg)
	})
	return err
}
