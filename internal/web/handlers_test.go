package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"skarb/internal/config"
	"skarb/internal/domain"
	"skarb/internal/fx"
	"skarb/internal/service"
	"skarb/internal/store"
)

// testUser is a logged-in browser: its session cookie and CSRF token.
type testUser struct {
	cookie *http.Cookie
	csrf   string
	actor  domain.Actor
}

type testApp struct {
	t       *testing.T
	svc     *service.Service
	handler http.Handler
	server  *Server
	admin   testUser
	member  testUser
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "skarb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	loc, _ := time.LoadLocation("Europe/Kyiv")
	cfg := config.Config{
		DataDir: dir, PublicBaseURL: "http://localhost", AdminUsername: "admin",
		AdminPassword: "adminpassword123", MaxActiveUsers: 2, Timezone: "Europe/Kyiv",
		SessionSecret: []byte("test-secret-012345678901234567890"),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := service.New(st, fx.New(st, nil, logger, loc), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.BootstrapAdmin(ctx); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 10, 26, 12, 0, 0, 0, loc)
	svc.Now = func() time.Time { return fixed }
	srv, err := New(svc, cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	app := &testApp{t: t, svc: svc, handler: srv.Handler(), server: srv}
	app.admin = app.login("admin", "adminpassword123")

	user, temp, err := svc.CreateUser(ctx, app.admin.actor, service.CreateUserInput{Username: "member", DisplayName: "Member"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ChangePassword(ctx, svc.ActorForUser(user, domain.SourceWeb), temp, "memberpassword123"); err != nil {
		t.Fatal(err)
	}
	app.member = app.login("member", "memberpassword123")
	return app
}

func (a *testApp) login(username, password string) testUser {
	a.t.Helper()
	sess, token, err := a.svc.Login(context.Background(), username, password, "127.0.0.1", "test")
	if err != nil {
		a.t.Fatal(err)
	}
	return testUser{
		cookie: &http.Cookie{Name: "skarb_session", Value: token},
		csrf:   sess.CSRFSecret,
		actor:  a.svc.ActorForUser(&sess.User, domain.SourceWeb),
	}
}

// do sends a request as u (nil for anonymous). A POST carries u's CSRF token
// unless form already sets csrf_token.
func (a *testApp) do(u *testUser, method, path string, form url.Values) *httptest.ResponseRecorder {
	a.t.Helper()
	var body io.Reader
	if method == http.MethodPost {
		if form == nil {
			form = url.Values{}
		}
		if u != nil && !form.Has("csrf_token") {
			form.Set("csrf_token", u.csrf)
		}
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if u != nil {
		req.AddCookie(u.cookie)
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

func (a *testApp) addExpense(actor domain.Actor, desc string) *domain.Transaction {
	a.t.Helper()
	ctx := context.Background()
	food, err := a.svc.Store.ResolveCategory(ctx, "Їжа", nil)
	if err != nil {
		a.t.Fatal(err)
	}
	tx, err := a.svc.CreateTransaction(ctx, actor, service.TransactionItemInput{
		Amount: "25", Currency: "UAH", Date: a.svc.Today().String(), Category: food.UID, Description: desc,
	}, domain.SourceWeb, desc)
	if err != nil {
		a.t.Fatal(err)
	}
	return tx
}

func (a *testApp) deleted(uid string) bool {
	a.t.Helper()
	tx, err := a.svc.GetTransaction(context.Background(), a.admin.actor, uid)
	if err != nil {
		a.t.Fatal(err)
	}
	return tx.Deleted()
}

func flashFrom(t *testing.T, rec *httptest.ResponseRecorder) flashMessage {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == "skarb_flash" && c.Value != "" {
			raw, err := base64.RawURLEncoding.DecodeString(c.Value)
			if err != nil {
				t.Fatal(err)
			}
			var f flashMessage
			if err := json.Unmarshal(raw, &f); err != nil {
				t.Fatal(err)
			}
			return f
		}
	}
	t.Fatal("no flash cookie set")
	return flashMessage{}
}

func TestAnonymousIsSentToLogin(t *testing.T) {
	app := newTestApp(t)
	rec := app.do(nil, http.MethodGet, "/transactions?kind=expense", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	want := "/login?next=" + url.QueryEscape("/transactions?kind=expense")
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

func TestPostWithoutValidCSRFIsRejected(t *testing.T) {
	app := newTestApp(t)
	tx := app.addExpense(app.admin.actor, "lunch")
	rec := app.do(&app.admin, http.MethodPost, "/transactions/"+tx.UID+"/delete", url.Values{"csrf_token": {"wrong"}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if app.deleted(tx.UID) {
		t.Fatal("transaction deleted despite a bad CSRF token")
	}
}

func TestMemberIsForbiddenFromAdminRoutes(t *testing.T) {
	app := newTestApp(t)
	for _, path := range []string{"/admin/settings", "/admin/users", "/admin/categories", "/admin/audit", "/admin/backup"} {
		if rec := app.do(&app.member, http.MethodGet, path, nil); rec.Code != http.StatusForbidden {
			t.Errorf("GET %s: status = %d, want 403", path, rec.Code)
		}
	}
	rec := app.do(&app.member, http.MethodPost, "/accounts/new", url.Values{"name": {"x"}, "currency": {"UAH"}})
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /accounts/new: status = %d, want 403", rec.Code)
	}
	if rec := app.do(&app.admin, http.MethodGet, "/admin/settings", nil); rec.Code != http.StatusOK {
		t.Errorf("admin GET /admin/settings: status = %d, want 200", rec.Code)
	}
}

func TestAdminCanEnableLLMForAllGroupMessages(t *testing.T) {
	app := newTestApp(t)
	rec := app.do(&app.admin, http.MethodPost, "/admin/settings/llm", url.Values{
		"schema_mode":               {"auto"},
		"timeout_seconds":           {"15"},
		"max_output_tokens":         {"800"},
		"max_transactions":          {"10"},
		"max_input_chars":           {"2000"},
		"temperature":               {"0.1"},
		"listen_all_group_messages": {"1"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	cfg, err := app.svc.GetLLMConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ListenAllGroupMessages {
		t.Fatal("all-group-messages setting was not saved")
	}
}

func TestDeleteRedirectsBackAndOffersUndo(t *testing.T) {
	app := newTestApp(t)
	tx := app.addExpense(app.member.actor, "coffee")
	rec := app.do(&app.member, http.MethodPost, "/transactions/"+tx.UID+"/delete", url.Values{"next": {"/transactions?kind=expense&page=2"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/transactions?kind=expense&page=2" {
		t.Fatalf("redirect: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if !app.deleted(tx.UID) {
		t.Fatal("transaction not deleted")
	}
	flash := flashFrom(t, rec)
	if len(flash.Undo) != 1 || flash.Undo[0] != tx.UID {
		t.Fatalf("undo = %v, want [%s]", flash.Undo, tx.UID)
	}

	rec = app.do(&app.member, http.MethodPost, "/transactions/restore", url.Values{"uid": flash.Undo, "next": {"/"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("restore redirect: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if app.deleted(tx.UID) {
		t.Fatal("undo did not restore the transaction")
	}
}

func TestBulkDeleteAndUndo(t *testing.T) {
	app := newTestApp(t)
	a := app.addExpense(app.admin.actor, "a")
	b := app.addExpense(app.admin.actor, "b")
	keep := app.addExpense(app.admin.actor, "keep")

	rec := app.do(&app.admin, http.MethodPost, "/transactions/delete", url.Values{"uid": {a.UID, b.UID}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if !app.deleted(a.UID) || !app.deleted(b.UID) || app.deleted(keep.UID) {
		t.Fatal("bulk delete touched the wrong rows")
	}
	flash := flashFrom(t, rec)
	if flash.Message != "Видалено 2 транзакції." || len(flash.Undo) != 2 {
		t.Fatalf("flash = %+v", flash)
	}

	app.do(&app.admin, http.MethodPost, "/transactions/restore", url.Values{"uid": flash.Undo})
	if app.deleted(a.UID) || app.deleted(b.UID) {
		t.Fatal("undo did not restore both rows")
	}
}

func TestUnsafeNextIsIgnored(t *testing.T) {
	app := newTestApp(t)
	for _, next := range []string{"//evil.example/", "https://evil.example/", `/\evil.example`, "javascript:alert(1)"} {
		tx := app.addExpense(app.admin.actor, "x")
		rec := app.do(&app.admin, http.MethodPost, "/transactions/"+tx.UID+"/delete", url.Values{"next": {next}})
		if got := rec.Header().Get("Location"); got != "/transactions" {
			t.Errorf("next=%q: Location = %q, want /transactions", next, got)
		}
	}
}

func TestMissingTransactionIs404(t *testing.T) {
	app := newTestApp(t)
	rec := app.do(&app.admin, http.MethodGet, "/transactions/does-not-exist", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Транзакцію не знайдено.") {
		t.Fatal("404 page lacks the message")
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("response has no X-Request-ID")
	}
}

func TestErrTextHidesInternalErrors(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), requestIDCtx, "abc123"))
	cases := []struct {
		err  error
		want string
	}{
		{errors.New("sql: no such table: secrets at /data/skarb.db"), "внутрішня помилка (код abc123)"},
		{&domain.ValidationError{Field: "amount", Message: "must be positive"}, "amount: must be positive"},
		{fmt.Errorf("%w: transaction", domain.ErrNotFound), "не знайдено"},
		{domain.ErrForbidden, "недостатньо прав"},
		{fmt.Errorf("%w: maximum active users reached", domain.ErrConflict), "maximum active users reached"},
	}
	for _, c := range cases {
		if got := app.server.errText(req, c.err); got != c.want {
			t.Errorf("errText(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestDashboardMonthNavigation(t *testing.T) {
	app := newTestApp(t)
	rec := app.do(&app.admin, http.MethodGet, "/?month=2026-09", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Вересень 2026", `href="/?month=2026-08"`, `href="/?month=2026-10"`, "Поточний місяць"} {
		if !strings.Contains(body, want) {
			t.Errorf("past month page lacks %q", want)
		}
	}
	body = app.do(&app.admin, http.MethodGet, "/", nil).Body.String()
	if !strings.Contains(body, "Жовтень 2026") || strings.Contains(body, "Поточний місяць") {
		t.Error("current month page should show October without the back-to-current link")
	}
}

func TestMobileNavigation(t *testing.T) {
	app := newTestApp(t)
	menu := app.do(&app.member, http.MethodGet, "/menu", nil).Body.String()
	if !strings.Contains(menu, "Заощадження") || strings.Contains(menu, "Адміністрування") {
		t.Error("member menu should list sections without the admin group")
	}
	if !strings.Contains(app.do(&app.admin, http.MethodGet, "/menu", nil).Body.String(), "Адміністрування") {
		t.Error("admin menu lacks the admin group")
	}
	if !strings.Contains(app.do(&app.member, http.MethodGet, "/", nil).Body.String(), `class="tabbar"`) {
		t.Error("dashboard lacks the bottom bar")
	}
	if strings.Contains(app.do(&app.member, http.MethodGet, "/transactions/new", nil).Body.String(), `class="tabbar"`) {
		t.Error("the new-transaction form should hide the bottom bar")
	}
}

func TestNewTransactionFormDefaultsAndAddAnother(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	taxi, err := app.svc.Store.ResolveCategory(ctx, "Таксі", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.svc.CreateTransaction(ctx, app.member.actor, service.TransactionItemInput{
		Amount: "150", Currency: "UAH", Date: "2026-10-20", Category: taxi.UID, Description: "uber",
	}, domain.SourceWeb, "uber"); err != nil {
		t.Fatal(err)
	}
	body := app.do(&app.member, http.MethodGet, "/transactions/new", nil).Body.String()
	if !strings.Contains(body, fmt.Sprintf(`<option value="%d" selected>`, taxi.ID)) {
		t.Fatal("form should preselect the category of the user's latest expense")
	}

	rec := app.do(&app.member, http.MethodPost, "/transactions/new", url.Values{
		"amount": {"80"}, "currency": {"UAH"}, "date": {"2026-10-24"}, "category": {fmt.Sprint(taxi.ID)}, "again": {"1"},
	})
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusSeeOther || loc.Path != "/transactions/new" ||
		loc.Query().Get("date") != "2026-10-24" || loc.Query().Get("category") != fmt.Sprint(taxi.ID) {
		t.Fatalf("add-another redirect: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	body = app.do(&app.member, http.MethodGet, loc.String(), nil).Body.String()
	if !strings.Contains(body, `value="2026-10-24"`) {
		t.Fatal("add-another form should keep the date")
	}
}

func TestManifestIsServedWithItsType(t *testing.T) {
	app := newTestApp(t)
	rec := app.do(nil, http.MethodGet, "/static/manifest.webmanifest", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/manifest+json" {
		t.Fatalf("manifest: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}
