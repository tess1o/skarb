package mcp

import (
	"strconv"
	"strings"

	"skarb/internal/domain"
	"skarb/internal/money"
)

type addTransactionItem struct {
	Amount      string `json:"amount"`
	Currency    string `json:"currency,omitempty"`
	Date        string `json:"date,omitempty"`
	Category    string `json:"category"`
	Description string `json:"description,omitempty"`
	Note        string `json:"note,omitempty"`
}

type addTransactionsIn struct {
	Transactions   []addTransactionItem `json:"transactions"`
	User           string               `json:"user,omitempty"`
	OriginalText   string               `json:"original_text,omitempty"`
	IdempotencyKey string               `json:"idempotency_key,omitempty"`
}

type addTransactionsOut struct {
	Created []transactionOut `json:"created"`
}

type listTransactionsIn struct {
	From           string `json:"from,omitempty"`
	To             string `json:"to,omitempty"`
	User           string `json:"user,omitempty"`
	Category       string `json:"category,omitempty"`
	Kind           string `json:"kind,omitempty"`
	Source         string `json:"source,omitempty"`
	IncludeDeleted bool   `json:"include_deleted,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	Cursor         string `json:"cursor,omitempty"`
}

type listTransactionsOut struct {
	Transactions []transactionOut `json:"transactions"`
	Total        int              `json:"total"`
	NextCursor   string           `json:"next_cursor,omitempty"`
}

type updateTransactionIn struct {
	UID            string  `json:"uid"`
	Amount         *string `json:"amount,omitempty"`
	Currency       *string `json:"currency,omitempty"`
	Date           *string `json:"date,omitempty"`
	Category       *string `json:"category,omitempty"`
	Description    *string `json:"description,omitempty"`
	Note           *string `json:"note,omitempty"`
	User           *string `json:"user,omitempty"`
	IdempotencyKey string  `json:"idempotency_key,omitempty"`
}

type deleteTransactionIn struct {
	UID            string `json:"uid"`
	Reason         string `json:"reason,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type statsSummaryIn struct {
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	User     string `json:"user,omitempty"`
	Category string `json:"category,omitempty"`
}

type currencyTotalOut struct {
	Currency string `json:"currency"`
	Income   string `json:"income"`
	Expenses string `json:"expenses"`
	Count    int    `json:"count"`
}

type summaryOut struct {
	From        string                      `json:"from"`
	To          string                      `json:"to"`
	Income      string                      `json:"income_uah"`
	Expenses    string                      `json:"expenses_uah"`
	Net         string                      `json:"net_uah"`
	Count       int                         `json:"count"`
	MissingRate int                         `json:"missing_rate,omitempty"`
	ByCurrency  map[string]currencyTotalOut `json:"by_currency"`
}

type statsCategoryIn struct {
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
	Kind  string `json:"kind"`
	User  string `json:"user,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type categoryTotalOut struct {
	Category string  `json:"category"`
	Kind     string  `json:"kind"`
	Amount   string  `json:"amount_uah"`
	Count    int     `json:"count"`
	Percent  float64 `json:"percent"`
}

type statsCategoryOut struct {
	From string             `json:"from"`
	To   string             `json:"to"`
	Rows []categoryTotalOut `json:"rows"`
}

type statsUsersIn struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

type userTotalOut struct {
	User     string `json:"user"`
	Income   string `json:"income_uah"`
	Expenses string `json:"expenses_uah"`
	Net      string `json:"net_uah"`
	Count    int    `json:"count"`
}

type statsUsersOut struct {
	From string         `json:"from"`
	To   string         `json:"to"`
	Rows []userTotalOut `json:"rows"`
}

type statsCompareIn struct {
	AFrom string `json:"a_from,omitempty"`
	ATo   string `json:"a_to,omitempty"`
	BFrom string `json:"b_from,omitempty"`
	BTo   string `json:"b_to,omitempty"`
	User  string `json:"user,omitempty"`
}

type categoryDeltaOut struct {
	Category string `json:"category"`
	Kind     string `json:"kind"`
	Current  string `json:"current_uah"`
	Previous string `json:"previous_uah"`
	Delta    string `json:"delta_uah"`
}

type statsCompareOut struct {
	A            summaryOut         `json:"a"`
	B            summaryOut         `json:"b"`
	IncomeDelta  string             `json:"income_delta_uah"`
	ExpenseDelta string             `json:"expense_delta_uah"`
	NetDelta     string             `json:"net_delta_uah"`
	ByCategory   []categoryDeltaOut `json:"by_category"`
}

type statsTrendIn struct {
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
	Granularity string `json:"granularity,omitempty"`
}

type trendPointOut struct {
	Date     string `json:"date"`
	Label    string `json:"label"`
	Income   string `json:"income_uah"`
	Expenses string `json:"expenses_uah"`
}

type statsTrendOut struct {
	Points []trendPointOut `json:"points"`
}

type netWorthIn struct {
	AsOf string `json:"as_of,omitempty"`
}

type accountBalanceOut struct {
	Account    string `json:"account"`
	Owner      string `json:"owner"`
	Currency   string `json:"currency"`
	Balance    string `json:"balance"`
	RateDate   string `json:"rate_date,omitempty"`
	BalanceUAH string `json:"balance_uah,omitempty"`
	HasRate    bool   `json:"has_rate"`
}

type netWorthOut struct {
	AsOf     string              `json:"as_of"`
	TotalUAH string              `json:"total_uah"`
	Accounts []accountBalanceOut `json:"accounts"`
	Warnings []string            `json:"warnings,omitempty"`
}

type savingsHistoryIn struct {
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
	Granularity string `json:"granularity,omitempty"`
}

type savingsPointOut struct {
	Date     string  `json:"date"`
	TotalUAH string  `json:"total_uah"`
	DeltaUAH string  `json:"delta_uah"`
	Pct      float64 `json:"pct"`
	Missing  int     `json:"missing_rates,omitempty"`
}

type savingsHistoryOut struct {
	Points []savingsPointOut `json:"points"`
}

type listCategoriesIn struct {
	IncludeArchived bool `json:"include_archived,omitempty"`
}

type categoryOut struct {
	UID      string   `json:"uid"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Color    string   `json:"color"`
	Emoji    string   `json:"emoji"`
	Sort     int      `json:"sort_order"`
	Aliases  []string `json:"aliases"`
	Archived bool     `json:"archived"`
}

type listCategoriesOut struct {
	Categories []categoryOut `json:"categories"`
}

type createCategoryIn struct {
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Color          string   `json:"color,omitempty"`
	Emoji          string   `json:"emoji,omitempty"`
	Aliases        []string `json:"aliases,omitempty"`
	SortOrder      int      `json:"sort_order,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
}

type updateCategoryIn struct {
	UID            string    `json:"uid"`
	Name           *string   `json:"name,omitempty"`
	Color          *string   `json:"color,omitempty"`
	Emoji          *string   `json:"emoji,omitempty"`
	Aliases        *[]string `json:"aliases,omitempty"`
	SortOrder      *int      `json:"sort_order,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
}

type deleteCategoryIn struct {
	UID            string `json:"uid"`
	ReassignTo     string `json:"reassign_to,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type deleteOut struct {
	Deleted bool   `json:"deleted"`
	UID     string `json:"uid,omitempty"`
	Message string `json:"message,omitempty"`
}

type listAccountsIn struct {
	IncludeArchived bool `json:"include_archived,omitempty"`
}

type accountOut struct {
	UID        string `json:"uid"`
	Owner      string `json:"owner"`
	Name       string `json:"name"`
	Currency   string `json:"currency"`
	Kind       string `json:"kind"`
	InNetWorth bool   `json:"include_in_net_worth"`
	Archived   bool   `json:"archived"`
	SortOrder  int    `json:"sort_order"`
}

type listAccountsOut struct {
	Accounts []accountOut `json:"accounts"`
}

type createAccountIn struct {
	Owner             string `json:"owner"`
	Name              string `json:"name"`
	Currency          string `json:"currency"`
	Kind              string `json:"kind,omitempty"`
	IncludeInNetWorth *bool  `json:"include_in_net_worth,omitempty"`
	SortOrder         int    `json:"sort_order,omitempty"`
	IdempotencyKey    string `json:"idempotency_key,omitempty"`
}

type updateAccountIn struct {
	UID               string  `json:"uid"`
	Name              *string `json:"name,omitempty"`
	Currency          *string `json:"currency,omitempty"`
	Kind              *string `json:"kind,omitempty"`
	IncludeInNetWorth *bool   `json:"include_in_net_worth,omitempty"`
	SortOrder         *int    `json:"sort_order,omitempty"`
	Archived          *bool   `json:"archived,omitempty"`
	IdempotencyKey    string  `json:"idempotency_key,omitempty"`
}

type deleteAccountIn struct {
	UID            string `json:"uid"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type upsertSnapshotIn struct {
	Account        string `json:"account"`
	User           string `json:"user,omitempty"`
	Date           string `json:"date,omitempty"`
	Amount         string `json:"amount"`
	Note           string `json:"note,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type snapshotOut struct {
	UID         string `json:"uid"`
	Account     string `json:"account"`
	AccountName string `json:"account_name"`
	Date        string `json:"date"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	Note        string `json:"note,omitempty"`
}

type deleteSnapshotIn struct {
	UID            string `json:"uid"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type listExchangeRatesIn struct {
	Currency string `json:"currency,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
}

type rateOut struct {
	Currency string `json:"currency"`
	Date     string `json:"date"`
	Rate     string `json:"uah_per_unit"`
	Source   string `json:"source"`
}

type listExchangeRatesOut struct {
	Rates []rateOut `json:"rates"`
}

type listUsersIn struct {
	IncludeInactive bool `json:"include_inactive,omitempty"`
}

type userOut struct {
	UID         string `json:"uid"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Active      bool   `json:"active"`
	TelegramID  string `json:"telegram_user_id,omitempty"`
	Linked      bool   `json:"telegram_linked"`
}

type listUsersOut struct {
	Users []userOut `json:"users"`
}

type createUserIn struct {
	Username       string `json:"username"`
	DisplayName    string `json:"display_name"`
	Password       string `json:"password,omitempty"`
	TelegramUserID *int64 `json:"telegram_user_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type createUserOut struct {
	User              userOut `json:"user"`
	TemporaryPassword string  `json:"temporary_password"`
}

type updateUserIn struct {
	UID            string  `json:"uid"`
	DisplayName    *string `json:"display_name,omitempty"`
	TelegramUserID *int64  `json:"telegram_user_id,omitempty"`
	Active         *bool   `json:"active,omitempty"`
	IdempotencyKey string  `json:"idempotency_key,omitempty"`
}

func transactionToOut(t domain.Transaction) transactionOut {
	return transactionOut{
		UID:          t.UID,
		Kind:         string(t.Kind),
		Amount:       money.PlainAmount(t.AmountMinor, money.MinorUnits(t.Currency)),
		Currency:     t.Currency,
		Date:         t.OccurredOn.String(),
		Category:     t.CategoryName,
		Owner:        t.OwnerName,
		Description:  t.Description,
		Note:         t.Note,
		OriginalText: t.OriginalText,
		Source:       string(t.Source),
		Deleted:      t.Deleted(),
	}
}

type transactionOut struct {
	UID          string `json:"uid"`
	Kind         string `json:"kind"`
	Amount       string `json:"amount"`
	Currency     string `json:"currency"`
	Date         string `json:"date"`
	Category     string `json:"category"`
	Owner        string `json:"owner"`
	Description  string `json:"description"`
	Note         string `json:"note,omitempty"`
	OriginalText string `json:"original_text,omitempty"`
	Source       string `json:"source"`
	Deleted      bool   `json:"deleted,omitempty"`
}

func summaryToOut(s *domain.Summary) summaryOut {
	out := summaryOut{
		From:        s.From.String(),
		To:          s.To.String(),
		Income:      money.PlainAmount(s.IncomeUAH, 2),
		Expenses:    money.PlainAmount(s.ExpenseUAH, 2),
		Net:         money.PlainAmount(s.NetUAH, 2),
		Count:       s.Count,
		MissingRate: s.MissingRate,
		ByCurrency:  map[string]currencyTotalOut{},
	}
	for code, ct := range s.ByCurrency {
		out.ByCurrency[code] = currencyTotalOut{
			Currency: code,
			Income:   money.PlainAmount(ct.Income, money.MinorUnits(code)),
			Expenses: money.PlainAmount(ct.Expense, money.MinorUnits(code)),
			Count:    ct.Count,
		}
	}
	return out
}

func categoryToOut(c domain.Category) categoryOut {
	return categoryOut{
		UID: c.UID, Name: c.Name, Kind: string(c.Kind), Color: c.Color, Emoji: c.Emoji,
		Sort: c.SortOrder, Aliases: c.Aliases, Archived: c.Archived(),
	}
}

func accountToOut(a domain.Account) accountOut {
	return accountOut{
		UID: a.UID, Owner: a.OwnerName, Name: a.Name, Currency: a.Currency,
		Kind: string(a.Kind), InNetWorth: a.IncludeInNetWorth, Archived: a.Archived(), SortOrder: a.SortOrder,
	}
}

func snapshotToOut(s domain.Snapshot, account domain.Account) snapshotOut {
	return snapshotOut{
		UID: s.UID, Account: account.UID, AccountName: account.Name,
		Date: s.SnapshotDate.String(), Amount: money.PlainAmount(s.BalanceMinor, money.MinorUnits(account.Currency)),
		Currency: account.Currency, Note: s.Note,
	}
}

func userToOut(u domain.User) userOut {
	out := userOut{
		UID: u.UID, Username: u.Username, DisplayName: u.DisplayName, Role: string(u.Role), Active: u.IsActive,
	}
	if u.TelegramUserID != nil {
		out.TelegramID = itoa(*u.TelegramUserID)
		out.Linked = true
	}
	return out
}

func kindFromString(s string) (domain.Kind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "income":
		return domain.KindIncome, nil
	case "expense":
		return domain.KindExpense, nil
	default:
		return "", validationError("kind must be income or expense")
	}
}

func accountKindFromString(s string) domain.AccountKind {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "bank":
		return domain.AccountBank
	case "cash":
		return domain.AccountCash
	case "investment":
		return domain.AccountInvestment
	default:
		return domain.AccountOther
	}
}

func parseAccountKind(s string) (domain.AccountKind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return domain.AccountOther, nil
	case "bank":
		return domain.AccountBank, nil
	case "cash":
		return domain.AccountCash, nil
	case "investment":
		return domain.AccountInvestment, nil
	case "other":
		return domain.AccountOther, nil
	default:
		return "", validationError("kind must be bank, cash, investment, or other")
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func validationError(msg string) error { return &domain.ValidationError{Message: msg} }
