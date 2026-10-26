package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type seedCategory struct {
	name    string
	kind    string
	color   string
	emoji   string
	sort    int
	aliases []string
}

var seedCategories = []seedCategory{
	{name: "Їжа", kind: "expense", color: "#e76f51", emoji: "🍽️", sort: 10, aliases: []string{"їжа", "їжу", "їжі", "їжею", "food", "продукти", "groceries", "обід", "вечеря", "сніданок", "lunch", "dinner"}},
	{name: "Кава", kind: "expense", color: "#b56576", emoji: "☕", sort: 20, aliases: []string{"кава", "каву", "каві", "кавою", "coffee", "кофе", "кав'ярня", "cafe"}},
	{name: "Транспорт", kind: "expense", color: "#457b9d", emoji: "🚌", sort: 30, aliases: []string{"транспорт", "transport", "таксі", "taxi", "метро", "metro", "пальне", "fuel", "бензин", "паркінг", "parking", "шиномонтаж", "шини", "колеса", "автосервіс", "ремонт авто", "мийка", "парковка", "заправка", "проїзд", "квиток", "потяг", "автобус", "tire service", "tire", "tyre", "car service", "car wash", "gas station", "bus", "train"}},
	{name: "Житло", kind: "expense", color: "#6d597a", emoji: "🏠", sort: 40, aliases: []string{"житло", "житла", "оренда", "оренду", "оренди", "housing", "rent", "квартира", "apartment"}},
	{name: "Комуналка", kind: "expense", color: "#e9c46a", emoji: "💡", sort: 50, aliases: []string{"комуналка", "комуналку", "комуналки", "utilities", "світло", "electricity", "газ", "gas", "вода", "water", "інтернет", "internet"}},
	{name: "Здоров'я", kind: "expense", color: "#2a9d8f", emoji: "💊", sort: 60, aliases: []string{"здоров'я", "аптека", "аптеку", "аптеки", "лікар", "лікаря", "health", "pharmacy", "doctor", "медицина", "medical"}},
	{name: "Розваги", kind: "expense", color: "#f4a261", emoji: "🎬", sort: 70, aliases: []string{"розваги", "entertainment", "кіно", "cinema", "концерт", "concert", "гра", "game", "fun"}},
	{name: "Покупки", kind: "expense", color: "#e76f51", emoji: "🛍️", sort: 80, aliases: []string{"покупки", "покупку", "shopping", "одяг", "clothes", "взуття", "shoes", "техніка", "electronics"}},
	{name: "Освіта", kind: "expense", color: "#4d908e", emoji: "📚", sort: 90, aliases: []string{"освіта", "освіту", "навчання", "курси", "courses", "книги", "books"}},
	{name: "Інше", kind: "expense", color: "#8d99ae", emoji: "📦", sort: 100, aliases: []string{"інше", "іншого", "other", "misc", "різне"}},
	{name: "Зарплата", kind: "income", color: "#2a9d8f", emoji: "💼", sort: 10, aliases: []string{"зарплата", "зарплату", "зарплати", "salary", "зарплатня", "paycheck"}},
	{name: "Фріланс", kind: "income", color: "#457b9d", emoji: "💻", sort: 20, aliases: []string{"фріланс", "фриланс", "freelance", "проєкт", "project"}},
	{name: "Подарунки", kind: "income", color: "#e9c46a", emoji: "🎁", sort: 30, aliases: []string{"подарунки", "подарунок", "подарунка", "gifts", "gift"}},
	{name: "Повернення", kind: "income", color: "#8d99ae", emoji: "↩️", sort: 40, aliases: []string{"повернення", "повернення коштів", "refund", "кешбек", "cashback"}},
	{name: "Інший дохід", kind: "income", color: "#6d597a", emoji: "💰", sort: 50, aliases: []string{"інший дохід", "other income", "дохід", "income", "бонус", "bonus"}},
}

func (s *Store) Seed(ctx context.Context) error {
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM categories`).Scan(&count); err != nil {
		return fmt.Errorf("count categories: %w", err)
	}
	if count == 0 {
		if err := s.seedCategories(ctx); err != nil {
			return err
		}
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO reminder_configs
		 (kind, cron_expr, enabled, timezone, window_days, updated_at)
		 VALUES ('transactions', '0 10 * * 1', 0, 'Europe/Kyiv', 7, ?)`,
		Now(),
	); err != nil {
		return fmt.Errorf("seed transaction reminder: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO reminder_configs
		 (kind, cron_expr, enabled, timezone, window_days, updated_at)
		 VALUES ('snapshot', '0 10 1 * *', 0, 'Europe/Kyiv', 31, ?)`,
		Now(),
	); err != nil {
		return fmt.Errorf("seed snapshot reminder: %w", err)
	}
	return nil
}

func (s *Store) seedCategories(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range seedCategories {
		uid := uuid.NewString()
		res, err := tx.ExecContext(ctx,
			`INSERT INTO categories(uid, name, kind, color, emoji, sort_order, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			uid, c.name, c.kind, c.color, c.emoji, c.sort, Now(), Now(),
		)
		if err != nil {
			return fmt.Errorf("seed category %s: %w", c.name, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for _, alias := range c.aliases {
			norm := NormalizeAlias(alias)
			if norm == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO category_aliases(alias_norm, alias_text, category_id)
				 VALUES (?, ?, ?)`, norm, alias, id,
			); err != nil {
				return fmt.Errorf("seed alias %s: %w", alias, err)
			}
		}
	}
	return tx.Commit()
}

func NormalizeAlias(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, "’", "'")
	s = strings.ReplaceAll(s, "ʼ", "'")
	s = strings.Join(strings.Fields(s), " ")
	return s
}
