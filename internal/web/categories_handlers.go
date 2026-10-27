package web

import (
	"net/http"
	"strings"

	"skarb/internal/domain"
	"skarb/internal/store"
)

func (s *Server) categoriesPage(w http.ResponseWriter, r *http.Request) {
	actor := s.actor(r)
	categories, err := s.svc.ListCategories(r.Context(), actor, true)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	s.render(w, r, "categories.html", map[string]any{
		"Title":      "Категорії",
		"Categories": categories,
	})
}

func (s *Server) categoryCreate(w http.ResponseWriter, r *http.Request) {
	in := store.CreateCategoryInput{
		Name:      r.FormValue("name"),
		Kind:      domain.Kind(r.FormValue("kind")),
		Color:     r.FormValue("color"),
		Emoji:     r.FormValue("emoji"),
		SortOrder: atoiDefault(r.FormValue("sort"), 0),
		Aliases:   splitAliases(r.FormValue("aliases")),
	}
	if _, err := s.svc.CreateCategory(r.Context(), s.actor(r), in); err != nil {
		s.setFlash(w, "error", "Не вдалося створити категорію: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Категорію створено.")
	}
	s.redirect(w, r, "/admin/categories")
}

func (s *Server) categoryEditPage(w http.ResponseWriter, r *http.Request) {
	cat, err := s.svc.CategoryByUID(r.Context(), s.actor(r), r.PathValue("uid"))
	if err != nil {
		s.renderError(w, r, http.StatusNotFound, "Категорію не знайдено.")
		return
	}
	s.render(w, r, "category_form.html", map[string]any{
		"Title":    "Редагування категорії",
		"Category": cat,
		"Action":   "/admin/categories/" + cat.UID + "/edit",
		"Aliases":  strings.Join(cat.Aliases, "\n"),
	})
}

func (s *Server) categoryUpdate(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	name := r.FormValue("name")
	color := r.FormValue("color")
	emoji := r.FormValue("emoji")
	sortOrder := atoiDefault(r.FormValue("sort"), 0)
	aliases := splitAliases(r.FormValue("aliases"))
	in := store.UpdateCategoryInput{
		Name:      &name,
		Color:     &color,
		Emoji:     &emoji,
		SortOrder: &sortOrder,
		Aliases:   &aliases,
	}
	if _, err := s.svc.UpdateCategory(r.Context(), s.actor(r), uid, in); err != nil {
		s.setFlash(w, "error", "Не вдалося оновити категорію: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Категорію оновлено.")
	}
	s.redirect(w, r, "/admin/categories")
}

func (s *Server) categoryArchive(w http.ResponseWriter, r *http.Request) {
	archived := r.FormValue("archived") == "1"
	if err := s.svc.ArchiveCategory(r.Context(), s.actor(r), r.PathValue("uid"), archived); err != nil {
		s.setFlash(w, "error", "Не вдалося змінити архів: "+s.errText(r, err))
	} else if archived {
		s.setFlash(w, "success", "Категорію архівовано.")
	} else {
		s.setFlash(w, "success", "Категорію відновлено.")
	}
	s.redirect(w, r, "/admin/categories")
}

func (s *Server) categoryDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteCategory(r.Context(), s.actor(r), r.PathValue("uid"), r.FormValue("reassign_to")); err != nil {
		s.setFlash(w, "error", "Не вдалося видалити категорію: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Категорію видалено.")
	}
	s.redirect(w, r, "/admin/categories")
}

func splitAliases(raw string) []string {
	replacer := strings.NewReplacer("\r", "\n", ",", "\n", ";", "\n")
	var out []string
	for _, line := range strings.Split(replacer.Replace(raw), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
