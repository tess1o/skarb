package web

import "net/http"

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	data, err := s.svc.Dashboard(r.Context(), s.actor(r))
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Не вдалося завантажити огляд: "+err.Error())
		return
	}
	s.render(w, r, "dashboard.html", map[string]any{
		"Title":     "Огляд",
		"Dashboard": data,
	})
}
