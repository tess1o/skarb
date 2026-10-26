package domain

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"time"
)

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

type Kind string

const (
	KindIncome  Kind = "income"
	KindExpense Kind = "expense"
)

type Source string

const (
	SourceWeb      Source = "web"
	SourceTelegram Source = "telegram"
	SourceMCP      Source = "mcp"
	SourceSystem   Source = "system"
)

type AccountKind string

const (
	AccountBank       AccountKind = "bank"
	AccountCash       AccountKind = "cash"
	AccountInvestment AccountKind = "investment"
	AccountOther      AccountKind = "other"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrForbidden    = errors.New("forbidden")
	ErrUnauthorized = errors.New("unauthorized")
	ErrConflict     = errors.New("conflict")
	ErrValidation   = errors.New("validation failed")
)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

func (e *ValidationError) Unwrap() error { return ErrValidation }

// Date is a calendar date in ISO YYYY-MM-DD form. It deliberately has no time zone.
type Date struct {
	time.Time
}

func NewDate(y int, m time.Month, d int) Date {
	return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return NewDate(y, m, d)
}

func ParseDate(s string) (Date, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Date{}, fmt.Errorf("invalid date %q: %w", s, err)
	}
	return Date{t}, nil
}

func (d Date) String() string { return d.Format("2006-01-02") }

func (d Date) AddDays(n int) Date { return Date{d.Time.AddDate(0, 0, n)} }

func (d Date) Before(other Date) bool { return d.Time.Before(other.Time) }
func (d Date) After(other Date) bool  { return d.Time.After(other.Time) }
func (d Date) Equal(other Date) bool  { return d.Time.Equal(other.Time) }
func (d Date) IsZero() bool           { return d.Time.IsZero() }

func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case string:
		parsed, err := ParseDate(v)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	case []byte:
		return d.Scan(string(v))
	case time.Time:
		*d = DateOf(v)
		return nil
	case nil:
		*d = Date{}
		return nil
	default:
		return fmt.Errorf("cannot scan %T into Date", src)
	}
}

func (d Date) Value() (driver.Value, error) {
	if d.IsZero() {
		return nil, nil
	}
	return d.String(), nil
}

// TodayIn returns today's date in the supplied location.
func TodayIn(loc *time.Location, now time.Time) Date {
	return DateOf(now.In(loc))
}

type User struct {
	ID                 int64
	UID                string
	Username           string
	DisplayName        string
	PasswordHash       string
	Role               Role
	IsActive           bool
	TelegramUserID     *int64
	MustChangePassword bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

type Category struct {
	ID         int64
	UID        string
	Name       string
	Kind       Kind
	Color      string
	Emoji      string
	SortOrder  int
	ArchivedAt *time.Time
	Aliases    []string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (c Category) Archived() bool { return c.ArchivedAt != nil }

type Transaction struct {
	ID              int64
	UID             string
	Kind            Kind
	AmountMinor     int64
	Currency        string
	OccurredOn      Date
	CategoryID      int64
	CategoryName    string
	CategoryEmoji   string
	CategoryColor   string
	OwnerUserID     int64
	OwnerName       string
	Description     string
	Note            string
	OriginalText    string
	Source          Source
	CreatedByUserID *int64
	UpdatedByUserID *int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       *time.Time
	DeletedByUserID *int64
	DeleteReason    string
	ReplacesID      *int64
}

func (t Transaction) Deleted() bool { return t.DeletedAt != nil }

type Account struct {
	ID                int64
	UID               string
	OwnerUserID       int64
	OwnerName         string
	Name              string
	Currency          string
	Kind              AccountKind
	IncludeInNetWorth bool
	SortOrder         int
	ArchivedAt        *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (a Account) Archived() bool { return a.ArchivedAt != nil }

type Snapshot struct {
	ID              int64
	UID             string
	AccountID       int64
	SnapshotDate    Date
	BalanceMinor    int64
	Note            string
	CreatedByUserID *int64
	UpdatedByUserID *int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       *time.Time
	DeletedByUserID *int64
}

type Actor struct {
	Kind           Source
	UserID         int64
	UserUID        string
	Role           Role
	TelegramUserID int64
	ChatID         int64
	TokenUID       string
	RequestID      string
	IP             string
	UserAgent      string
}

func (a Actor) IsAdmin() bool  { return a.Role == RoleAdmin }
func (a Actor) IsSystem() bool { return a.Kind == SourceSystem }

type Summary struct {
	From        Date
	To          Date
	IncomeUAH   int64
	ExpenseUAH  int64
	NetUAH      int64
	Count       int
	MissingRate int
	ByCurrency  map[string]CurrencyTotal
}

type CurrencyTotal struct {
	Currency string
	Income   int64
	Expense  int64
	Count    int
}

type CategoryTotal struct {
	CategoryID int64
	UID        string
	Name       string
	Emoji      string
	Color      string
	Kind       Kind
	AmountUAH  int64
	Count      int
	Percent    float64
}

type UserTotal struct {
	UserID      int64
	DisplayName string
	IncomeUAH   int64
	ExpenseUAH  int64
	NetUAH      int64
	Count       int
}

type TrendPoint struct {
	Date       Date
	Label      string
	IncomeUAH  int64
	ExpenseUAH int64
}

type Comparison struct {
	A            Summary
	B            Summary
	IncomeDelta  int64
	ExpenseDelta int64
	NetDelta     int64
	ByCategory   []CategoryComparison
}

type CategoryComparison struct {
	CategoryID int64
	Name       string
	Kind       Kind
	AUAH       int64
	BUAH       int64
	Delta      int64
}

type NetWorth struct {
	AsOf        Date
	TotalUAH    int64
	Accounts    []AccountBalance
	MissingRate []string
}

type AccountBalance struct {
	AccountID    int64
	AccountUID   string
	AccountName  string
	OwnerName    string
	Currency     string
	BalanceMinor int64
	RateDate     Date
	BalanceUAH   int64
	HasRate      bool
}

type SavingsPoint struct {
	Date        Date
	TotalUAH    int64
	DeltaUAH    int64
	Pct         float64
	MissingRate int
}

type DateRange struct {
	From Date
	To   Date
}

func (r DateRange) Contains(d Date) bool {
	return !d.Before(r.From) && !d.After(r.To)
}

type ExchangeRate struct {
	Currency   string
	RateDate   Date
	RateScaled int64
	Source     string
	FetchedAt  time.Time
}

type Session struct {
	TokenHash  string
	UserID     int64
	CSRFSecret string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	IP         string
	UserAgent  string
	User       User
}

type APIToken struct {
	ID          int64
	UID         string
	TokenPrefix string
	Last4       string
	UserID      int64
	Name        string
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
	CreatedAt   time.Time
	User        User
}

type ReminderConfig struct {
	Kind         string
	CronExpr     string
	Enabled      bool
	Timezone     string
	WindowDays   int
	TargetChatID *int64
	TargetUserID *int64
	LastRunAt    *time.Time
	LastSentAt   *time.Time
	UpdatedAt    time.Time
}

type TelegramChat struct {
	ChatID        int64
	Title         string
	ChatType      string
	AddedByUserID *int64
	CreatedAt     time.Time
}

type TelegramConfirmation struct {
	ID                  int64
	ChatID              int64
	MessageID           int64
	ActorTelegramUserID int64
	ItemsJSON           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type TelegramAction struct {
	Token               string
	ConfirmationID      int64
	TransactionID       int64
	Action              string
	ActorTelegramUserID int64
	ExpiresAt           time.Time
	UsedAt              *time.Time
	CreatedAt           time.Time
}

type TelegramPendingEdit struct {
	ChatID              int64
	PromptMessageID     int64
	ConfirmationID      int64
	TransactionID       int64
	ActorTelegramUserID int64
	ExpiresAt           time.Time
	CreatedAt           time.Time
}

// TelegramToolAction stores a pending MCP tool invocation or a paginated tool
// result shown by the Telegram bot.
type TelegramToolAction struct {
	Token               string
	ChatID              int64
	MessageID           int64
	ActorTelegramUserID int64
	Action              string
	ToolName            string
	ArgumentsJSON       string
	ResultJSON          string
	NextCursor          string
	PageSize            int
	PageNumber          int
	Status              string
	ExpiresAt           time.Time
	UsedAt              *time.Time
	CreatedAt           time.Time
}
