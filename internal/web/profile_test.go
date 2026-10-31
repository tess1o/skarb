package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"skarb/internal/domain"
	"skarb/internal/service"
)

func TestOwnProfileIdentityAndPermissions(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	token, _, err := app.svc.CreateAPIToken(ctx, app.member.actor, service.APITokenCreateInput{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	rec := app.do(&app.member, "POST", "/settings/profile", url.Values{"display_name": {"  Нове ім’я  "}, "uid": {app.admin.actor.UserUID}, "telegram_id": {"12345"}, "role": {"admin"}})
	if rec.Code != 303 || rec.Header().Get("Location") != "/settings#profile" {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	member, err := app.svc.Store.GetUserByID(ctx, app.member.actor.UserID)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := app.svc.Store.GetUserByID(ctx, app.admin.actor.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if member.DisplayName != "Нове ім’я" || member.Role != domain.RoleMember || member.TelegramUserID != nil || admin.DisplayName == member.DisplayName {
		t.Fatal("profile update changed protected fields")
	}
	body := app.do(&app.member, "GET", "/settings", nil).Body.String()
	if !strings.Contains(body, "Нове ім’я") || !strings.Contains(body, "Прив’язку змінює адміністратор.") || strings.Contains(body, `action="/settings/telegram`) {
		t.Fatal("wrong member profile")
	}
	if _, err := app.svc.AuthenticateAPIToken(ctx, token); err != nil {
		t.Fatal("profile change revoked token")
	}
	for _, path := range []string{"/settings/telegram", "/admin/users/" + admin.UID + "/edit"} {
		rec := app.do(&app.member, "POST", path, url.Values{"display_name": {"hacked"}, "telegram_id": {"12345"}})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: %d", path, rec.Code)
		}
	}
	if err := app.svc.SetOwnTelegramID(ctx, app.member.actor, nil); err == nil {
		t.Fatal("service allows member Telegram update")
	}
	rec = app.do(&app.member, "POST", "/settings/profile", url.Values{"display_name": {"   "}})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), `value="   "`) {
		t.Fatal("invalid profile not retained")
	}
	rec = app.do(&app.member, "POST", "/settings/profile", url.Values{"display_name": {"bad"}, "csrf_token": {"bad"}})
	if rec.Code != 403 {
		t.Fatal("missing CSRF protection")
	}
}
func TestTelegramValidationAndAtomicUserEdit(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	path := "/admin/users/" + app.member.actor.UserUID + "/edit"
	for _, raw := range []string{"abc", "0", "-7", "9223372036854775808"} {
		rec := app.do(&app.admin, "POST", "/settings/telegram", url.Values{"telegram_id": {raw}})
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), `value="`+raw+`"`) {
			t.Fatalf("invalid Telegram %s", raw)
		}
	}
	rec := app.do(&app.admin, "POST", "/settings/telegram", url.Values{"telegram_id": {"12345"}})
	if rec.Code != 303 {
		t.Fatal(rec.Body.String())
	}
	if err := app.svc.UpdateOwnDisplayName(ctx, app.admin.actor, "Renamed admin"); err != nil {
		t.Fatal(err)
	}
	updated, err := app.svc.Store.GetUserByID(ctx, app.admin.actor.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.TelegramUserID == nil || *updated.TelegramUserID != 12345 {
		t.Fatal("name-only update changed Telegram binding")
	}
	rec = app.do(&app.admin, "POST", path, url.Values{"display_name": {"Unsaved name"}, "telegram_id": {"12345"}})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "уже прив’язаний") || !strings.Contains(rec.Body.String(), `value="Unsaved name"`) {
		t.Fatal("duplicate Telegram error not retained")
	}
	member, err := app.svc.Store.GetUserByID(ctx, app.member.actor.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if member.DisplayName != "Member" || member.TelegramUserID != nil {
		t.Fatal("invalid edit partially applied")
	}
	rec = app.do(&app.admin, "POST", path, url.Values{"display_name": {"Updated Member"}, "telegram_id": {"67890"}})
	if rec.Code != 303 || !strings.HasSuffix(rec.Header().Get("Location"), "/edit#personal") {
		t.Fatal("user edit redirect")
	}
	rec = app.do(&app.admin, "POST", path, url.Values{"display_name": {"Updated Member"}, "telegram_id": {"invalid"}})
	if rec.Code != 422 {
		t.Fatal("invalid Telegram silently cleared")
	}
	member, err = app.svc.Store.GetUserByID(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if member.TelegramUserID == nil || *member.TelegramUserID != 67890 {
		t.Fatal("binding changed on invalid input")
	}
	badID := int64(-7)
	if err := app.svc.UpdateUser(ctx, app.admin.actor, member.UID, "should not save", &badID); err == nil {
		t.Fatal("service accepted negative Telegram ID")
	}
	rec = app.do(&app.admin, "POST", "/settings/telegram", url.Values{"telegram_id": {"67890"}})
	if rec.Code != 422 {
		t.Fatal("own duplicate accepted")
	}
	rec = app.do(&app.admin, "POST", "/settings/telegram", url.Values{"telegram_id": {""}})
	if rec.Code != 303 {
		t.Fatal("unbind failed")
	}
	admin, err := app.svc.Store.GetUserByID(ctx, app.admin.actor.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if admin.TelegramUserID != nil {
		t.Fatal("Telegram not unbound")
	}
	if admin.DisplayName != "Renamed admin" {
		t.Fatal("Telegram-only update changed display name")
	}

}
func TestUserCardResetAndNavigation(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	base := "/admin/users/" + app.member.actor.UserUID
	body := app.do(&app.admin, "GET", "/admin/users", nil).Body.String()
	if strings.Contains(body, "Скинути пароль") || !strings.Contains(body, "Відкрити") || !strings.Contains(body, `<details class="card create-user"`) {
		t.Fatal("list still contains management actions")
	}
	body = app.do(&app.admin, "GET", base+"/edit", nil).Body.String()
	for _, text := range []string{"Доступ", "Скинути пароль", "Деактивувати", "тимчасовий пароль"} {
		if !strings.Contains(body, text) {
			t.Fatalf("card lacks %s", text)
		}
	}
	ownBase := "/admin/users/" + app.admin.actor.UserUID
	body = app.do(&app.admin, "GET", ownBase+"/edit", nil).Body.String()
	if strings.Contains(body, "Скинути пароль") || !strings.Contains(body, "Змінити мій пароль") {
		t.Fatal("self reset shown")
	}
	rec := app.do(&app.admin, "POST", ownBase+"/reset", nil)
	if rec.Header().Get("Location") != "/settings#password" {
		t.Fatal("self reset should open password form")
	}
	token, _, err := app.svc.CreateAPIToken(ctx, app.member.actor, service.APITokenCreateInput{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	rec = app.do(&app.admin, "POST", base+"/reset", nil)
	if rec.Code != 303 || rec.Header().Get("Location") != base+"/edit#access" {
		t.Fatal("reset should return to card")
	}
	flash := flashFrom(t, rec)
	prefix := "Новий тимчасовий пароль: "
	if !strings.HasPrefix(flash.Message, prefix) {
		t.Fatalf("password not shown: %+v", flash)
	}
	password := strings.TrimPrefix(flash.Message, prefix)
	if _, err := app.svc.AuthenticateAPIToken(ctx, token); err == nil {
		t.Fatal("reset did not revoke token")
	}
	if got := app.do(&app.member, "GET", "/settings", nil); got.Code != 303 {
		t.Fatal("reset did not revoke session")
	}
	_, _, err = app.svc.Login(ctx, "member", "memberpassword123", "127.0.0.1", "test")
	if err == nil {
		t.Fatal("old password still works")
	}
	session, _, err := app.svc.Login(ctx, "member", password, "127.0.0.1", "test")
	if err != nil || !session.User.MustChangePassword {
		t.Fatal("temporary password failed")
	}
	body = app.do(&app.admin, "GET", "/settings", nil).Body.String()
	if !strings.Contains(body, `class="admin-menu`) || !strings.Contains(body, "Налаштування системи") || !strings.Contains(body, "Мій профіль") {
		t.Fatal("admin navigation missing")
	}
}
