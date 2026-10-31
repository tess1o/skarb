package web

import (
	"net/http"
	"strconv"

	"skarb/internal/service"
)

func (s *Server) usersPage(w http.ResponseWriter, r *http.Request) {
	s.renderUsers(w, r, http.StatusOK, nil)
}
func (s *Server) renderUsers(w http.ResponseWriter, r *http.Request, status int, extra map[string]any) {
	users, err := s.svc.ListUsers(r.Context(), s.actor(r), true)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	data := map[string]any{"Title": "Користувачі", "Users": users, "Form": map[string]string{}}
	for k, v := range extra {
		data[k] = v
	}
	s.renderStatus(w, r, status, "users.html", data)
}

func (s *Server) userCreate(w http.ResponseWriter, r *http.Request) {
	telegram, err := parseTelegramID(r.FormValue("telegram_id"))
	if err != nil {
		s.userCreateError(w, r, err)
		return
	}
	in := service.CreateUserInput{
		Username:       r.FormValue("username"),
		DisplayName:    r.FormValue("display_name"),
		Password:       r.FormValue("password"),
		TelegramUserID: telegram,
	}
	user, password, err := s.svc.CreateUser(r.Context(), s.actor(r), in)
	if err != nil {
		s.userCreateError(w, r, err)
		return
	}
	s.setFlash(w, "success", "Користувача "+user.Username+" створено. Тимчасовий пароль: "+password)
	s.redirect(w, r, "/admin/users")
}

func (s *Server) userCreateError(w http.ResponseWriter, r *http.Request, err error) {
	s.renderUsers(w, r, http.StatusUnprocessableEntity, map[string]any{"CreateError": s.errText(r, err), "Form": formStrings(r, "username", "display_name", "telegram_id")})
}
func (s *Server) userEditPage(w http.ResponseWriter, r *http.Request) {
	s.renderUserCard(w, r, http.StatusOK, nil)
}
func (s *Server) renderUserCard(w http.ResponseWriter, r *http.Request, status int, extra map[string]any) {
	user, err := s.svc.UserByUID(r.Context(), r.PathValue("uid"))
	if err != nil {
		s.renderError(w, r, http.StatusNotFound, "Користувача не знайдено.")
		return
	}
	telegram := ""
	if user.TelegramUserID != nil {
		telegram = strconv.FormatInt(*user.TelegramUserID, 10)
	}
	data := map[string]any{"Title": "Картка користувача", "EditUser": user, "Action": "/admin/users/" + user.UID + "/edit", "IsSelf": user.ID == currentUser(r.Context()).ID, "Form": map[string]string{"display_name": user.DisplayName, "telegram_id": telegram}}
	for k, v := range extra {
		data[k] = v
	}
	s.renderStatus(w, r, status, "user_form.html", data)
}
func (s *Server) userUpdate(w http.ResponseWriter, r *http.Request) {
	telegram, err := parseTelegramID(r.FormValue("telegram_id"))
	if err == nil {
		err = s.svc.UpdateUser(r.Context(), s.actor(r), r.PathValue("uid"), r.FormValue("display_name"), telegram)
	}
	if err != nil {
		s.renderUserCard(w, r, http.StatusUnprocessableEntity, map[string]any{"ProfileError": s.errText(r, err), "Form": formStrings(r, "display_name", "telegram_id")})
		return
	}
	s.setFlash(w, "success", "Користувача оновлено.")
	s.redirect(w, r, "/admin/users/"+r.PathValue("uid")+"/edit#personal")
}

func (s *Server) userResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("uid") == currentUser(r.Context()).UID {
		s.redirect(w, r, "/settings#password")
		return
	}
	password, err := s.svc.ResetUserPassword(r.Context(), s.actor(r), r.PathValue("uid"))
	if err != nil {
		s.setFlash(w, "error", "Не вдалося скинути пароль: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Новий тимчасовий пароль: "+password)
	}
	s.redirect(w, r, "/admin/users/"+r.PathValue("uid")+"/edit#access")
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
	s.redirect(w, r, "/admin/users/"+r.PathValue("uid")+"/edit#access")
}

func (s *Server) userDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.SetUserActive(r.Context(), s.actor(r), r.PathValue("uid"), false); err != nil {
		s.setFlash(w, "error", "Не вдалося видалити користувача: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Користувача видалено (деактивовано). Історія транзакцій збережена.")
	}
	s.redirect(w, r, "/admin/users/"+r.PathValue("uid")+"/edit#access")
}
