package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"skarb/internal/config"
	"skarb/internal/domain"
	"skarb/internal/service"
	"skarb/internal/store"
)

type Server struct {
	svc         *service.Service
	cfg         config.Config
	log         *slog.Logger
	schemaCache *mcpsdk.SchemaCache
}

var adminOnlyTools = []string{
	"create_category", "update_category", "delete_category",
	"create_account", "update_account", "delete_account",
	"upsert_snapshot", "delete_snapshot",
	"create_user", "update_user",
}

func New(svc *service.Service, cfg config.Config, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{svc: svc, cfg: cfg, log: logger, schemaCache: mcpsdk.NewSchemaCache()}
}

func (s *Server) HTTPHandler() http.Handler {
	opts := &mcpsdk.StreamableHTTPOptions{
		Stateless:           true,
		JSONResponse:        true,
		Logger:              s.log,
		MaxRequestBodyBytes: 4 << 20,
	}
	handler := mcpsdk.NewStreamableHTTPHandler(func(r *http.Request) *mcpsdk.Server {
		info := sdkauth.TokenInfoFromContext(r.Context())
		if info == nil {
			return nil
		}
		user, _ := info.Extra["user"].(*domain.User)
		if user == nil {
			return nil
		}
		return s.serverFor(user)
	}, opts)
	return sdkauth.RequireBearerToken(s.verify, &sdkauth.RequireBearerTokenOptions{
		AllowMissingExpiration: true,
	})(handler)
}

func (s *Server) verify(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
	apiToken, err := s.svc.AuthenticateAPIToken(ctx, token)
	if err != nil {
		return nil, sdkauth.ErrInvalidToken
	}
	info := &sdkauth.TokenInfo{
		UserID: apiToken.User.UID,
		Extra:  map[string]any{"user": &apiToken.User},
	}
	if apiToken.ExpiresAt != nil {
		info.Expiration = *apiToken.ExpiresAt
	}
	return info, nil
}

func (s *Server) serverFor(user *domain.User) *mcpsdk.Server {
	return s.serverForActor(s.svc.ActorForUser(user, domain.SourceMCP))
}

func (s *Server) serverForActor(actor domain.Actor) *mcpsdk.Server {
	instructions := "Skarb is a private family money tracker. Amounts are decimal strings, dates are ISO YYYY-MM-DD, " +
		"currencies are ISO 4217. All authenticated users can read everything and edit any transaction. " +
		"Only admins may manage categories, accounts/snapshots, users, and settings. " +
		"Use stats tools for computed numbers; never invent totals."
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:        "skarb",
		Title:       "Skarb",
		Description: "Family money tracker: transactions, stats, savings snapshots",
		Version:     "1.0.0",
	}, &mcpsdk.ServerOptions{Instructions: instructions, Logger: s.log, SchemaCache: s.schemaCache})
	s.addTransactionTools(srv, actor)
	s.addStatsTools(srv, actor)
	s.addAdminTools(srv, actor)
	if actor.Role != domain.RoleAdmin {
		srv.RemoveTools(adminOnlyTools...)
	}
	return srv
}

func (s *Server) resolveUserRef(ctx context.Context, ref string) (*domain.User, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	if u, err := s.svc.UserByUID(ctx, ref); err == nil {
		return u, nil
	}
	u, err := s.svc.Store.GetUserByUsername(ctx, ref)
	if err != nil {
		return nil, validationError("user not found")
	}
	return u, nil
}

func (s *Server) resolveOwner(ctx context.Context, actor domain.Actor, ref string) (*int64, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, nil
	}
	u, err := s.resolveUserRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}
	if actor.Role != domain.RoleAdmin && u.ID != actor.UserID {
		return nil, domain.ErrForbidden
	}
	return &u.ID, nil
}

func (s *Server) resolveUserFilter(ctx context.Context, ref string) (*int64, error) {
	u, err := s.resolveUserRef(ctx, ref)
	if err != nil || u == nil {
		return nil, err
	}
	return &u.ID, nil
}

func (s *Server) resolveCategoryFilter(ctx context.Context, ref string) (*int64, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	if c, err := s.svc.Store.GetCategoryByUID(ctx, ref); err == nil {
		return &c.ID, nil
	}
	c, err := s.svc.Store.ResolveCategory(ctx, ref, nil)
	if err != nil {
		return nil, validationError("category not found")
	}
	return &c.ID, nil
}

func parseDateDefault(raw string, def domain.Date) (domain.Date, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	d, err := domain.ParseDate(raw)
	if err != nil {
		return domain.Date{}, validationError("invalid date, expected YYYY-MM-DD")
	}
	return d, nil
}

func (s *Server) dateRange(fromRaw, toRaw string) (domain.DateRange, error) {
	today := s.svc.Today()
	from, err := parseDateDefault(fromRaw, domain.Date{})
	if err != nil {
		return domain.DateRange{}, err
	}
	to, err := parseDateDefault(toRaw, domain.Date{})
	if err != nil {
		return domain.DateRange{}, err
	}
	if from.IsZero() && to.IsZero() {
		r := service.CurrentMonth(today)
		return r, nil
	}
	if from.IsZero() {
		from = to.AddDays(-30)
	}
	if to.IsZero() {
		to = today
	}
	if to.Before(from) {
		return domain.DateRange{}, validationError("end date is before start date")
	}
	return domain.DateRange{From: from, To: to}, nil
}

func decodeCursor(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, validationError("invalid cursor")
	}
	n, err := strconv.Atoi(string(b))
	if err != nil || n < 0 {
		return 0, validationError("invalid cursor")
	}
	return n, nil
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func clampLimit(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func toolError(err error) error {
	switch {
	case errors.Is(err, domain.ErrForbidden), errors.Is(err, domain.ErrUnauthorized):
		return fmt.Errorf("forbidden")
	case errors.Is(err, domain.ErrNotFound):
		return fmt.Errorf("not_found")
	default:
		return err
	}
}

func (s *Server) transactionFilter(ctx context.Context, in listTransactionsIn, actor domain.Actor) (store.TransactionFilter, int, error) {
	f := store.TransactionFilter{Search: ""}
	from, err := parseDateDefault(in.From, domain.Date{})
	if err != nil {
		return f, 0, err
	}
	to, err := parseDateDefault(in.To, domain.Date{})
	if err != nil {
		return f, 0, err
	}
	if !from.IsZero() {
		f.From = &from
	}
	if !to.IsZero() {
		f.To = &to
	}
	if uid, err := s.resolveUserFilter(ctx, in.User); err != nil {
		return f, 0, err
	} else if uid != nil {
		f.OwnerUserID = uid
	}
	if cid, err := s.resolveCategoryFilter(ctx, in.Category); err != nil {
		return f, 0, err
	} else if cid != nil {
		f.CategoryID = cid
	}
	if strings.TrimSpace(in.Kind) != "" {
		k, err := kindFromString(in.Kind)
		if err != nil {
			return f, 0, err
		}
		f.Kind = &k
	}
	switch strings.ToLower(strings.TrimSpace(in.Source)) {
	case "":
	case "web":
		src := domain.SourceWeb
		f.Source = &src
	case "telegram":
		src := domain.SourceTelegram
		f.Source = &src
	case "mcp":
		src := domain.SourceMCP
		f.Source = &src
	default:
		return f, 0, validationError("source must be web, telegram, or mcp")
	}
	if in.IncludeDeleted {
		if actor.Role != domain.RoleAdmin {
			return f, 0, domain.ErrForbidden
		}
		f.IncludeDeleted = true
	}
	offset, err := decodeCursor(in.Cursor)
	if err != nil {
		return f, 0, err
	}
	f.Offset = offset
	f.Limit = clampLimit(in.Limit, 50, 200)
	f.OrderBy = "date_desc"
	return f, offset, nil
}

func (s *Server) requireAdmin(actor domain.Actor) error {
	if actor.Role != domain.RoleAdmin {
		return domain.ErrForbidden
	}
	return nil
}

// timeNow is a variable to keep handlers deterministic in tests if needed.
var timeNow = time.Now
