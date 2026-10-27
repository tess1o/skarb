package web

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"skarb/internal/config"
	"skarb/internal/domain"
	"skarb/internal/money"
	"skarb/internal/service"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Server struct {
	svc                *service.Service
	cfg                config.Config
	log                *slog.Logger
	pages              map[string]*template.Template
	static             fs.FS
	limiter            *loginLimiter
	trustedProxies     []*net.IPNet
	MCPHandler         http.Handler
	OnRemindersChanged func()
	OnTestReminder     func(kind string, telegramUserID int64) error
}

func init() {
	// Go's built-in table lacks the web app manifest type.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

func New(svc *service.Service, cfg config.Config, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	s := &Server{
		svc:     svc,
		cfg:     cfg,
		log:     logger,
		static:  staticFS,
		limiter: newLoginLimiter(),
	}
	for _, raw := range cfg.TrustedProxyCIDRs {
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", raw, err)
		}
		s.trustedProxies = append(s.trustedProxies, network)
	}
	funcs := s.templateFuncs()
	pageNames := []string{
		"login.html", "dashboard.html", "transactions.html", "transaction_form.html",
		"transaction_detail.html", "stats.html", "snapshots.html", "categories.html",
		"category_form.html", "users.html", "user_form.html", "settings.html",
		"own_settings.html", "audit.html", "error.html", "menu.html",
	}
	s.pages = map[string]*template.Template{}
	for _, page := range pageNames {
		t, err := template.New("layout").Funcs(funcs).ParseFS(assets,
			"templates/layout.html", "templates/partials.html", "templates/"+page)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", page, err)
		}
		s.pages[page] = t
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(s.static))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginSubmit)

	mux.Handle("POST /logout", s.requireAuth(http.HandlerFunc(s.logout)))
	mux.Handle("GET /{$}", s.requireAuth(http.HandlerFunc(s.dashboard)))
	mux.Handle("GET /menu", s.requireAuth(http.HandlerFunc(s.menuPage)))

	mux.Handle("GET /transactions", s.requireAuth(http.HandlerFunc(s.transactionsPage)))
	mux.Handle("GET /transactions/new", s.requireAuth(http.HandlerFunc(s.transactionNewPage)))
	mux.Handle("POST /transactions/new", s.requireAuth(http.HandlerFunc(s.transactionCreate)))
	mux.Handle("GET /transactions/{uid}", s.requireAuth(http.HandlerFunc(s.transactionDetailPage)))
	mux.Handle("GET /transactions/{uid}/edit", s.requireAuth(http.HandlerFunc(s.transactionEditPage)))
	mux.Handle("POST /transactions/{uid}/edit", s.requireAuth(http.HandlerFunc(s.transactionUpdate)))
	mux.Handle("POST /transactions/{uid}/delete", s.requireAuth(http.HandlerFunc(s.transactionDelete)))
	mux.Handle("POST /transactions/delete", s.requireAuth(http.HandlerFunc(s.transactionsBulkDelete)))
	mux.Handle("POST /transactions/restore", s.requireAuth(http.HandlerFunc(s.transactionsRestore)))
	mux.Handle("GET /transactions/export.csv", s.requireAuth(http.HandlerFunc(s.transactionsCSV)))

	mux.Handle("GET /stats", s.requireAuth(http.HandlerFunc(s.statsPage)))
	mux.Handle("GET /stats/export.csv", s.requireAuth(http.HandlerFunc(s.statsCSV)))

	mux.Handle("GET /snapshots", s.requireAuth(http.HandlerFunc(s.snapshotsPage)))
	mux.Handle("POST /accounts/new", s.requireAdmin(http.HandlerFunc(s.accountCreate)))
	mux.Handle("POST /accounts/{uid}/edit", s.requireAdmin(http.HandlerFunc(s.accountUpdate)))
	mux.Handle("POST /accounts/{uid}/delete", s.requireAdmin(http.HandlerFunc(s.accountDelete)))
	mux.Handle("POST /snapshots/upsert", s.requireAdmin(http.HandlerFunc(s.snapshotUpsert)))
	mux.Handle("POST /snapshots/{uid}/delete", s.requireAdmin(http.HandlerFunc(s.snapshotDelete)))

	mux.Handle("GET /admin/categories", s.requireAdmin(http.HandlerFunc(s.categoriesPage)))
	mux.Handle("POST /admin/categories", s.requireAdmin(http.HandlerFunc(s.categoryCreate)))
	mux.Handle("GET /admin/categories/{uid}/edit", s.requireAdmin(http.HandlerFunc(s.categoryEditPage)))
	mux.Handle("POST /admin/categories/{uid}/edit", s.requireAdmin(http.HandlerFunc(s.categoryUpdate)))
	mux.Handle("POST /admin/categories/{uid}/archive", s.requireAdmin(http.HandlerFunc(s.categoryArchive)))
	mux.Handle("POST /admin/categories/{uid}/delete", s.requireAdmin(http.HandlerFunc(s.categoryDelete)))

	mux.Handle("GET /admin/users", s.requireAdmin(http.HandlerFunc(s.usersPage)))
	mux.Handle("POST /admin/users", s.requireAdmin(http.HandlerFunc(s.userCreate)))
	mux.Handle("GET /admin/users/{uid}/edit", s.requireAdmin(http.HandlerFunc(s.userEditPage)))
	mux.Handle("POST /admin/users/{uid}/edit", s.requireAdmin(http.HandlerFunc(s.userUpdate)))
	mux.Handle("POST /admin/users/{uid}/reset", s.requireAdmin(http.HandlerFunc(s.userResetPassword)))
	mux.Handle("POST /admin/users/{uid}/active", s.requireAdmin(http.HandlerFunc(s.userSetActive)))
	mux.Handle("POST /admin/users/{uid}/delete", s.requireAdmin(http.HandlerFunc(s.userDelete)))

	mux.Handle("GET /admin/settings", s.requireAdmin(http.HandlerFunc(s.adminSettingsPage)))
	mux.Handle("POST /admin/settings/llm", s.requireAdmin(http.HandlerFunc(s.adminLLMUpdate)))
	mux.Handle("POST /admin/settings/llm/test", s.requireAdmin(http.HandlerFunc(s.adminLLMTest)))
	mux.Handle("POST /admin/settings/telegram/add", s.requireAdmin(http.HandlerFunc(s.adminTelegramAdd)))
	mux.Handle("POST /admin/settings/telegram/remove", s.requireAdmin(http.HandlerFunc(s.adminTelegramRemove)))
	mux.Handle("POST /admin/settings/reminders/{kind}", s.requireAdmin(http.HandlerFunc(s.adminReminderUpdate)))
	mux.Handle("POST /admin/settings/reminders/{kind}/test", s.requireAdmin(http.HandlerFunc(s.adminReminderTest)))
	mux.Handle("POST /admin/settings/fx/refresh", s.requireAdmin(http.HandlerFunc(s.adminFXRefresh)))
	mux.Handle("POST /admin/settings/fx/override", s.requireAdmin(http.HandlerFunc(s.adminFXOverride)))
	mux.Handle("POST /admin/settings/tokens", s.requireAdmin(http.HandlerFunc(s.adminTokenCreate)))
	mux.Handle("POST /admin/settings/tokens/{uid}/revoke", s.requireAdmin(http.HandlerFunc(s.adminTokenRevoke)))
	mux.Handle("GET /admin/audit", s.requireAdmin(http.HandlerFunc(s.auditPage)))
	mux.Handle("GET /admin/backup", s.requireAdmin(http.HandlerFunc(s.backupDownload)))

	mux.Handle("GET /settings", s.requireAuth(http.HandlerFunc(s.ownSettingsPage)))
	mux.Handle("POST /settings/password", s.requireAuth(http.HandlerFunc(s.ownPasswordChange)))
	mux.Handle("POST /settings/tokens", s.requireAuth(http.HandlerFunc(s.ownTokenCreate)))
	mux.Handle("POST /settings/tokens/{uid}/revoke", s.requireAuth(http.HandlerFunc(s.ownTokenRevoke)))

	if s.MCPHandler != nil {
		mux.Handle("/mcp", s.MCPHandler)
	}
	return s.securityHeaders(s.requestLogger(mux))
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Store.DB.PingContext(r.Context()); err != nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

type ctxKey int

const (
	userCtx ctxKey = iota
	sessionCtx
	requestIDCtx
)

func currentUser(ctx context.Context) *domain.User {
	u, _ := ctx.Value(userCtx).(*domain.User)
	return u
}

func currentSession(ctx context.Context) *domain.Session {
	s, _ := ctx.Value(sessionCtx).(*domain.Session)
	return s
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.sessionToken(r)
		sess, err := s.svc.AuthenticateSession(r.Context(), token)
		if err != nil {
			s.clearSessionCookie(w)
			http.Redirect(w, r, "/login?next="+template.URLQueryEscaper(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost && !s.validCSRF(r, sess.CSRFSecret) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		if sess.User.MustChangePassword && r.URL.Path != "/settings" &&
			r.URL.Path != "/settings/password" && r.URL.Path != "/logout" {
			s.setFlash(w, "warning", "Змініть тимчасовий пароль перед продовженням.")
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), userCtx, &sess.User)
		ctx = context.WithValue(ctx, sessionCtx, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r.Context())
		if u == nil || !u.IsAdmin() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (s *Server) actor(r *http.Request) domain.Actor {
	u := currentUser(r.Context())
	sess := currentSession(r.Context())
	if u == nil {
		return domain.Actor{Kind: domain.SourceWeb, IP: s.clientIP(r), UserAgent: r.UserAgent()}
	}
	actor := s.svc.ActorForUser(u, domain.SourceWeb)
	actor.IP = s.clientIP(r)
	actor.UserAgent = r.UserAgent()
	actor.RequestID = requestIDFrom(r.Context())
	if sess != nil {
		actor.UserID = sess.User.ID
	}
	return actor
}

func (s *Server) sessionToken(r *http.Request) string {
	c, err := r.Cookie("skarb_session")
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     "skarb_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure,
		MaxAge:   maxAge,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	s.setSessionCookie(w, "", -1)
}

func (s *Server) validCSRF(r *http.Request, secret string) bool {
	if secret == "" {
		return false
	}
	token := r.FormValue("csrf_token")
	if token == "" {
		token = r.Header.Get("X-CSRF-Token")
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1
}

func (s *Server) baseData(w http.ResponseWriter, r *http.Request) map[string]any {
	u := currentUser(r.Context())
	sess := currentSession(r.Context())
	data := map[string]any{
		"Path":       r.URL.Path,
		"RequestURI": r.URL.RequestURI(),
		"Year":       time.Now().Year(),
		"User":       u,
		"Flash":      s.getFlash(w, r),
	}
	if u != nil {
		data["IsAdmin"] = u.IsAdmin()
	}
	if sess != nil {
		data["CSRF"] = sess.CSRFSecret
	}
	return data
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	s.renderStatus(w, r, http.StatusOK, page, data)
}

// renderStatus executes the page into a buffer first, so a template error yields
// a clean 500 instead of a half-written page, and so headers set while building
// the data (e.g. clearing the flash cookie) are sent before the status line.
func (s *Server) renderStatus(w http.ResponseWriter, r *http.Request, status int, page string, data map[string]any) {
	base := s.baseData(w, r)
	for k, v := range base {
		if _, exists := data[k]; !exists {
			data[k] = v
		}
	}
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		s.log.Error("render template", "page", page, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, message string) {
	s.renderStatus(w, r, status, "error.html", map[string]any{"Title": "Помилка", "Message": message, "Status": status})
}

// errText turns err into a message safe to show a user. Validation errors are
// written for users and shown as is; the domain sentinels get short Ukrainian
// text; anything else (database, I/O, programming errors) is logged with the
// request ID and replaced by a generic message quoting that ID.
func (s *Server) errText(r *http.Request, err error) string {
	var verr *domain.ValidationError
	switch {
	case errors.As(err, &verr):
		return verr.Error()
	case errors.Is(err, domain.ErrNotFound):
		return "не знайдено"
	case errors.Is(err, domain.ErrForbidden), errors.Is(err, domain.ErrUnauthorized):
		return "недостатньо прав"
	case errors.Is(err, domain.ErrConflict):
		// Our own conflicts wrap a human-readable reason: "conflict: <reason>".
		if _, reason, ok := strings.Cut(err.Error(), ": "); ok {
			return reason
		}
		return "конфлікт даних"
	}
	id := requestIDFrom(r.Context())
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "request_id", id, "error", err)
	return "внутрішня помилка (код " + id + ")"
}

func (s *Server) redirect(w http.ResponseWriter, r *http.Request, path string) {
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func (s *Server) setFlash(w http.ResponseWriter, kind, message string) {
	s.writeFlash(w, flashMessage{Kind: kind, Message: message})
}

// setFlashUndo shows message with an undo button that restores uids. Larger
// selections get no button; admins can still restore them from the list.
func (s *Server) setFlashUndo(w http.ResponseWriter, message string, uids []string) {
	f := flashMessage{Kind: "success", Message: message}
	if len(uids) <= maxUndoUIDs {
		f.Undo = uids
	}
	s.writeFlash(w, f)
}

func (s *Server) writeFlash(w http.ResponseWriter, f flashMessage) {
	b, _ := json.Marshal(f)
	http.SetCookie(w, &http.Cookie{
		Name:     "skarb_flash",
		Value:    base64.RawURLEncoding.EncodeToString(b),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure,
		MaxAge:   60,
	})
}

type flashMessage struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	// Undo lists transaction UIDs the flash offers to restore.
	Undo []string `json:"undo,omitempty"`
}

// maxUndoUIDs keeps the flash cookie well under the 4 KB browser limit.
const maxUndoUIDs = 60

func (s *Server) getFlash(w http.ResponseWriter, r *http.Request) *flashMessage {
	c, err := r.Cookie("skarb_flash")
	if err != nil {
		return nil
	}
	http.SetCookie(w, &http.Cookie{
		Name: "skarb_flash", Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.cfg.CookieSecure, MaxAge: -1,
	})
	b, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return nil
	}
	var f flashMessage
	if json.Unmarshal(b, &f) != nil {
		return nil
	}
	return &f
}

// loginCSRF implements a signed double-submit token for the unauthenticated login form.
func (s *Server) loginCSRF(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie("skarb_login_csrf"); err == nil && s.validLoginCSRF(c.Value) {
		return c.Value
	}
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	token := hex.EncodeToString(raw) + "." + s.loginCSRFSig(hex.EncodeToString(raw))
	http.SetCookie(w, &http.Cookie{
		Name: "skarb_login_csrf", Value: token, Path: "/login", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.cfg.CookieSecure, MaxAge: 600,
	})
	return token
}

func (s *Server) loginCSRFSig(raw string) string {
	mac := hmac.New(sha256.New, s.cfg.SessionSecret)
	mac.Write([]byte(raw))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) validLoginCSRF(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	return hmac.Equal([]byte(parts[1]), []byte(s.loginCSRFSig(parts[0])))
}

func (s *Server) validLoginCSRFRequest(r *http.Request) bool {
	c, err := r.Cookie("skarb_login_csrf")
	if err != nil {
		return false
	}
	form := r.FormValue("csrf_token")
	if subtle.ConstantTimeCompare([]byte(form), []byte(c.Value)) != 1 {
		return false
	}
	return s.validLoginCSRF(c.Value)
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: map[string][]time.Time{}}
}

// maxLimiterKeys bounds the limiter's memory: past it, keys with no recent
// failures are swept on the next check.
const maxLimiterKeys = 10000

func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-15 * time.Minute)
	if len(l.attempts) > maxLimiterKeys {
		for k, times := range l.attempts {
			if len(times) == 0 || !times[len(times)-1].After(cutoff) {
				delete(l.attempts, k)
			}
		}
	}
	var recent []time.Time
	for _, t := range l.attempts[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	l.attempts[key] = recent
	return len(recent) < 5
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[key] = append(l.attempts[key], time.Now())
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote := net.ParseIP(host)
	trusted := false
	for _, network := range s.trustedProxies {
		if remote != nil && network.Contains(remote) {
			trusted = true
			break
		}
	}
	if trusted {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	return host
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		raw := make([]byte, 8)
		_, _ = rand.Read(raw)
		reqID := hex.EncodeToString(raw)
		w.Header().Set("X-Request-ID", reqID)
		r = r.WithContext(context.WithValue(r.Context(), requestIDCtx, reqID))
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if skipAccessLog(r, sw.status) {
			return
		}
		s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"duration", time.Since(start).Round(time.Millisecond), "ip", s.clientIP(r), "request_id", reqID)
	})
}

// skipAccessLog drops routine traffic from the access log: health probes,
// favicon, and static assets, which are re-fetched on every page view.
// Failures on app-served paths are still logged so a broken deploy doesn't
// go unnoticed.
func skipAccessLog(r *http.Request, status int) bool {
	path := r.URL.Path
	switch {
	case path == "/favicon.ico", strings.HasPrefix(path, "/_static/"):
		// Browser noise: the app serves no favicon, and /_static/ is probed by
		// browser tooling, not requested by the app.
		return true
	case status >= http.StatusBadRequest:
		return false
	case path == "/healthz", path == "/readyz":
		return true
	}
	return strings.HasPrefix(path, "/static/")
}

func requestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(requestIDCtx).(string)
	return v
}

// Template helpers.

func (s *Server) templateFuncs() template.FuncMap {
	return template.FuncMap{
		"fmtAmount": func(minor int64, currency string) string { return money.FormatAmount(minor, currency) },
		"fmtRate":   func(scaled int64) string { return money.FormatRate(scaled) },
		"fmtDate":   func(d domain.Date) string { return d.Format("02.01.2006") },
		"derefInt64": func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		},
		"fmtDateTime": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.In(s.svc.Loc).Format("02.01.2006 15:04")
		},
		"fmtTimePtr": func(t *time.Time) string {
			if t == nil || t.IsZero() {
				return "—"
			}
			return t.In(s.svc.Loc).Format("02.01.2006 15:04")
		},
		"fmtMonth": monthName,
		"fmtPct": func(v float64) string {
			return strings.ReplaceAll(fmt.Sprintf("%.1f", v), ".", ",")
		},
		"barPct": func(amount, total int64) int {
			if total <= 0 {
				return 0
			}
			v := int(float64(amount) / float64(total) * 100)
			if v < 1 && amount > 0 {
				return 1
			}
			if v > 100 {
				return 100
			}
			return v
		},
		"add": func(a, b int64) int64 { return a + b },
		"sub": func(a, b int64) int64 { return a - b },
		// trendTick thins axis labels on a daily bar chart: every fifth point,
		// plus the first and last. Points too close to the last label are
		// dropped so the final two labels never overlap.
		"trendTick": func(i, n int) bool {
			if n <= 8 || i == 0 || i == n-1 {
				return true
			}
			return i%5 == 0 && n-1-i >= 3
		},
		"plural": func(n int, one, few, many string) string {
			return pluralUK(n, one, few, many)
		},
		"kindLabel": func(k domain.Kind) string {
			if k == domain.KindIncome {
				return "Дохід"
			}
			return "Витрата"
		},
		"sourceLabel": func(src domain.Source) string {
			switch src {
			case domain.SourceTelegram:
				return "Telegram"
			case domain.SourceMCP:
				return "MCP"
			default:
				return "Веб"
			}
		},
		"roleLabel": func(r domain.Role) string {
			if r == domain.RoleAdmin {
				return "Адміністратор"
			}
			return "Користувач"
		},
		"accountKindLabel": func(k domain.AccountKind) string {
			switch k {
			case domain.AccountBank:
				return "Банк"
			case domain.AccountCash:
				return "Готівка"
			case domain.AccountInvestment:
				return "Інвестиції"
			default:
				return "Інше"
			}
		},
		"default": func(v, fallback string) string {
			if strings.TrimSpace(v) == "" {
				return fallback
			}
			return v
		},
		"urlquery":  template.URLQueryEscaper,
		"hasPrefix": strings.HasPrefix,
		"hasSuffix": strings.HasSuffix,
		// dict builds a map from key/value pairs so partials can take several arguments.
		"dict": func(kv ...any) (map[string]any, error) {
			if len(kv)%2 != 0 {
				return nil, fmt.Errorf("dict: odd number of arguments")
			}
			m := make(map[string]any, len(kv)/2)
			for i := 0; i < len(kv); i += 2 {
				k, ok := kv[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: key %v is not a string", kv[i])
				}
				m[k] = kv[i+1]
			}
			return m, nil
		},
	}
}

func pluralUK(n int, one, few, many string) string {
	mod10 := n % 10
	mod100 := n % 100
	switch {
	case mod10 == 1 && mod100 != 11:
		return one
	case mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14):
		return few
	default:
		return many
	}
}

var monthNames = []string{"", "Січень", "Лютий", "Березень", "Квітень", "Травень", "Червень", "Липень", "Серпень", "Вересень", "Жовтень", "Листопад", "Грудень"}

func monthName(d domain.Date) string {
	if d.IsZero() {
		return ""
	}
	return monthNames[int(d.Month())] + " " + fmt.Sprint(d.Year())
}
