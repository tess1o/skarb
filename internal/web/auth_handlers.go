package web

import (
	"net/http"
	"strings"

	"skarb/internal/service"
)

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.sessionToken(r) != "" {
		if _, err := s.svc.AuthenticateSession(r.Context(), s.sessionToken(r)); err == nil {
			s.redirect(w, r, "/")
			return
		}
	}
	csrf := s.loginCSRF(w, r)
	next := r.URL.Query().Get("next")
	s.render(w, r, "login.html", map[string]any{
		"Title": "Вхід",
		"CSRF":  csrf,
		"Next":  next,
	})
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Некоректна форма.")
		return
	}
	if !s.validLoginCSRFRequest(r) {
		s.renderError(w, r, http.StatusForbidden, "Сесія форми застаріла. Спробуйте ще раз.")
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	key := s.clientIP(r) + "|" + strings.ToLower(username)
	if !s.limiter.allow(key) {
		s.setFlash(w, "error", "Забагато спроб. Спробуйте пізніше.")
		s.redirect(w, r, "/login")
		return
	}
	sess, token, err := s.svc.Login(r.Context(), username, password, s.clientIP(r), r.UserAgent())
	if err != nil {
		s.limiter.fail(key)
		s.setFlash(w, "error", "Невірний логін або пароль.")
		s.redirect(w, r, "/login")
		return
	}
	s.limiter.reset(key)
	s.setSessionCookie(w, token, 30*24*60*60)
	next := safeNext(r.FormValue("next"))
	if sess.User.MustChangePassword {
		next = "/settings"
	}
	if next == "" {
		next = "/"
	}
	s.redirect(w, r, next)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	_ = s.svc.Logout(r.Context(), s.sessionToken(r))
	s.clearSessionCookie(w)
	s.redirect(w, r, "/login")
}

func safeNext(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(raw, `\`) {
		return ""
	}
	return raw
}

func (s *Server) ownSettingsPage(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	tokens, err := s.svc.ListAPITokens(r.Context(), s.actor(r), u.UID)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Не вдалося завантажити токени.")
		return
	}
	s.render(w, r, "own_settings.html", map[string]any{
		"Title":  "Мої налаштування",
		"Tokens": tokens,
	})
}

func (s *Server) ownPasswordChange(w http.ResponseWriter, r *http.Request) {
	old := r.FormValue("old_password")
	newPassword := r.FormValue("new_password")
	if err := s.svc.ChangePassword(r.Context(), s.actor(r), old, newPassword); err != nil {
		s.setFlash(w, "error", "Не вдалося змінити пароль: "+err.Error())
		s.redirect(w, r, "/settings")
		return
	}
	s.clearSessionCookie(w)
	s.setFlash(w, "success", "Пароль змінено. Увійдіть знову.")
	s.redirect(w, r, "/login")
}

func (s *Server) ownTokenCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	plain, _, err := s.svc.CreateAPIToken(r.Context(), s.actor(r), s.apiTokenInputFromForm(r, name))
	if err != nil {
		s.setFlash(w, "error", "Не вдалося створити токен: "+err.Error())
		s.redirect(w, r, "/settings")
		return
	}
	s.setFlash(w, "success", "Токен створено. Скопіюйте його зараз: "+plain)
	s.redirect(w, r, "/settings")
}

func (s *Server) ownTokenRevoke(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	if err := s.svc.RevokeAPIToken(r.Context(), s.actor(r), uid); err != nil {
		s.setFlash(w, "error", "Не вдалося відкликати токен: "+err.Error())
	} else {
		s.setFlash(w, "success", "Токен відкликано.")
	}
	s.redirect(w, r, "/settings")
}

func (s *Server) apiTokenInputFromForm(r *http.Request, name string) service.APITokenCreateInput {
	expires := r.FormValue("expires_days")
	input := service.APITokenCreateInput{Name: name}
	if expires == "365" {
		t := s.svc.Now().AddDate(1, 0, 0)
		input.ExpiresAt = &t
	}
	return input
}
