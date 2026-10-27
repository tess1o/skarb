package web

import (
	"net/http"
	"strconv"
	"strings"

	"skarb/internal/service"
)

func (s *Server) usersPage(w http.ResponseWriter, r *http.Request) {
	users, err := s.svc.ListUsers(r.Context(), s.actor(r), true)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	s.render(w, r, "users.html", map[string]any{
		"Title": "Користувачі",
		"Users": users,
	})
}

func (s *Server) userCreate(w http.ResponseWriter, r *http.Request) {
	in := service.CreateUserInput{
		Username:       r.FormValue("username"),
		DisplayName:    r.FormValue("display_name"),
		Password:       r.FormValue("password"),
		TelegramUserID: parseOptionalInt64(r.FormValue("telegram_id")),
	}
	user, password, err := s.svc.CreateUser(r.Context(), s.actor(r), in)
	if err != nil {
		s.setFlash(w, "error", "Не вдалося створити користувача: "+s.errText(r, err))
		s.redirect(w, r, "/admin/users")
		return
	}
	s.setFlash(w, "success", "Користувача "+user.Username+" створено. Тимчасовий пароль: "+password)
	s.redirect(w, r, "/admin/users")
}

func (s *Server) userEditPage(w http.ResponseWriter, r *http.Request) {
	user, err := s.svc.UserByUID(r.Context(), r.PathValue("uid"))
	if err != nil {
		s.renderError(w, r, http.StatusNotFound, "Користувача не знайдено.")
		return
	}
	s.render(w, r, "user_form.html", map[string]any{
		"Title":    "Редагування користувача",
		"EditUser": user,
		"Action":   "/admin/users/" + user.UID + "/edit",
	})
}

func (s *Server) userUpdate(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	if err := s.svc.UpdateUser(r.Context(), s.actor(r), uid,
		r.FormValue("display_name"), parseOptionalInt64(r.FormValue("telegram_id"))); err != nil {
		s.setFlash(w, "error", "Не вдалося оновити користувача: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Користувача оновлено.")
	}
	s.redirect(w, r, "/admin/users")
}

func (s *Server) userResetPassword(w http.ResponseWriter, r *http.Request) {
	password, err := s.svc.ResetUserPassword(r.Context(), s.actor(r), r.PathValue("uid"))
	if err != nil {
		s.setFlash(w, "error", "Не вдалося скинути пароль: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Новий тимчасовий пароль: "+password)
	}
	s.redirect(w, r, "/admin/users")
}

func (s *Server) userSetActive(w http.ResponseWriter, r *http.Request) {
	active := r.FormValue("active") == "1"
	if err := s.svc.SetUserActive(r.Context(), s.actor(r), r.PathValue("uid"), active); err != nil {
		s.setFlash(w, "error", "Не вдалося змінити статус: "+s.errText(r, err))
	} else if active {
		s.setFlash(w, "success", "Користувача активовано.")
	} else {
		s.setFlash(w, "success", "Користувача деактивовано.")
	}
	s.redirect(w, r, "/admin/users")
}

func (s *Server) userDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.SetUserActive(r.Context(), s.actor(r), r.PathValue("uid"), false); err != nil {
		s.setFlash(w, "error", "Не вдалося видалити користувача: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Користувача видалено (деактивовано). Історія транзакцій збережена.")
	}
	s.redirect(w, r, "/admin/users")
}

func parseOptionalInt64(raw string) *int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil
	}
	return &v
}
