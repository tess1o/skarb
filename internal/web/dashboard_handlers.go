package web

import "net/http"

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	data, err := s.svc.DashboardMonth(r.Context(), s.actor(r), r.URL.Query().Get("month"))
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Не вдалося завантажити огляд: "+s.errText(r, err))
		return
	}
	s.render(w, r, "dashboard.html", map[string]any{
		"Title":     "Огляд",
		"Dashboard": data,
	})
}

// menuPage lists the sections that don't fit in the mobile bottom bar.
func (s *Server) menuPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "menu.html", map[string]any{"Title": "Більше"})
}
