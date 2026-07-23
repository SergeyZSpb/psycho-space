package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/SergeyZSpb/psycho-space/internal/wishlist"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleWishlistList(w http.ResponseWriter, r *http.Request) {
	viewer, _ := accountFromContext(r.Context())
	items, err := s.d.Wishlist.List(r.Context(), viewer.ID, wishlist.Sort(r.URL.Query().Get("sort")))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.itemsResponse(r, viewer, items)})
}

func (s *Server) handleWishlistCreate(w http.ResponseWriter, r *http.Request) {
	viewer, _ := accountFromContext(r.Context())
	var req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	it, err := s.d.Wishlist.Create(r.Context(), viewer.ID, req.Title, req.Body)
	if err != nil {
		switch {
		case errors.Is(err, wishlist.ErrEmptyTitle):
			writeError(w, r, http.StatusUnprocessableEntity, "title_required")
		case errors.Is(err, wishlist.ErrTooLong):
			writeError(w, r, http.StatusUnprocessableEntity, "too_long")
		default:
			writeError(w, r, http.StatusInternalServerError, "internal")
		}
		return
	}
	writeJSON(w, http.StatusCreated, s.oneItemResponse(viewer, it,
		map[string]*account.Account{viewer.ID: viewer}))
}

func (s *Server) handleVote(w http.ResponseWriter, r *http.Request) {
	viewer, _ := accountFromContext(r.Context())
	id := chi.URLParam(r, "id")
	if !validUUID(id) {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	if err := s.d.Wishlist.Vote(r.Context(), id, viewer.ID); err != nil {
		if errors.Is(err, wishlist.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnvote(w http.ResponseWriter, r *http.Request) {
	viewer, _ := accountFromContext(r.Context())
	id := chi.URLParam(r, "id")
	if !validUUID(id) {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	if err := s.d.Wishlist.Unvote(r.Context(), id, viewer.ID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// itemsResponse enriches items with author display info (decrypted once per author).
func (s *Server) itemsResponse(r *http.Request, viewer *account.Account, items []wishlist.Item) []map[string]any {
	authors := map[string]*account.Account{}
	for _, it := range items {
		if _, ok := authors[it.AuthorID]; !ok {
			if a, err := s.d.Accounts.GetByID(r.Context(), it.AuthorID); err == nil {
				authors[it.AuthorID] = a
			}
		}
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, s.oneItemResponse(viewer, it, authors))
	}
	return out
}

func (s *Server) oneItemResponse(viewer *account.Account, it wishlist.Item, authors map[string]*account.Account) map[string]any {
	author := map[string]any{"display_name": "", "avatar_url": "", "vk_url": ""}
	if a := authors[it.AuthorID]; a != nil {
		author = map[string]any{"display_name": a.DisplayName(), "avatar_url": a.AvatarURL, "vk_url": a.VKURL()}
	}
	return map[string]any{
		"id":            it.ID,
		"title":         it.Title,
		"body":          it.Body,
		"votes":         it.Votes,
		"voted_by_me":   it.VotedByMe,
		"comment_count": it.CommentCount,
		"created_at":    it.CreatedAt,
		"author":        author,
		"mine":          it.AuthorID == viewer.ID,
	}
}

func (s *Server) handleCommentList(w http.ResponseWriter, r *http.Request) {
	viewer, _ := accountFromContext(r.Context())
	itemID := chi.URLParam(r, "id")
	if !validUUID(itemID) {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	comments, err := s.d.Wishlist.ListComments(r.Context(), itemID, viewer.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comments": s.commentsResponse(r, viewer, comments)})
}

func (s *Server) handleCommentCreate(w http.ResponseWriter, r *http.Request) {
	viewer, _ := accountFromContext(r.Context())
	itemID := chi.URLParam(r, "id")
	if !validUUID(itemID) {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	c, err := s.d.Wishlist.AddComment(r.Context(), itemID, viewer.ID, req.Body)
	if err != nil {
		switch {
		case errors.Is(err, wishlist.ErrEmptyComment):
			writeError(w, r, http.StatusUnprocessableEntity, "comment_required")
		case errors.Is(err, wishlist.ErrTooLong):
			writeError(w, r, http.StatusUnprocessableEntity, "too_long")
		case errors.Is(err, wishlist.ErrNotFound):
			writeError(w, r, http.StatusNotFound, "not_found")
		default:
			writeError(w, r, http.StatusInternalServerError, "internal")
		}
		return
	}
	writeJSON(w, http.StatusCreated, s.oneCommentResponse(viewer, c,
		map[string]*account.Account{viewer.ID: viewer}))
}

func (s *Server) handleCommentVote(w http.ResponseWriter, r *http.Request) {
	viewer, _ := accountFromContext(r.Context())
	id := chi.URLParam(r, "id")
	if !validUUID(id) {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	if err := s.d.Wishlist.VoteComment(r.Context(), id, viewer.ID); err != nil {
		if errors.Is(err, wishlist.ErrCommentNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCommentUnvote(w http.ResponseWriter, r *http.Request) {
	viewer, _ := accountFromContext(r.Context())
	id := chi.URLParam(r, "id")
	if !validUUID(id) {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	if err := s.d.Wishlist.UnvoteComment(r.Context(), id, viewer.ID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) commentsResponse(r *http.Request, viewer *account.Account, comments []wishlist.Comment) []map[string]any {
	authors := map[string]*account.Account{}
	for _, c := range comments {
		if _, ok := authors[c.AuthorID]; !ok {
			if a, err := s.d.Accounts.GetByID(r.Context(), c.AuthorID); err == nil {
				authors[c.AuthorID] = a
			}
		}
	}
	out := make([]map[string]any, 0, len(comments))
	for _, c := range comments {
		out = append(out, s.oneCommentResponse(viewer, c, authors))
	}
	return out
}

func (s *Server) oneCommentResponse(viewer *account.Account, c wishlist.Comment, authors map[string]*account.Account) map[string]any {
	author := map[string]any{"display_name": "", "avatar_url": "", "vk_url": ""}
	if a := authors[c.AuthorID]; a != nil {
		author = map[string]any{"display_name": a.DisplayName(), "avatar_url": a.AvatarURL, "vk_url": a.VKURL()}
	}
	return map[string]any{
		"id":          c.ID,
		"item_id":     c.ItemID,
		"body":        c.Body,
		"votes":       c.Votes,
		"voted_by_me": c.VotedByMe,
		"created_at":  c.CreatedAt,
		"author":      author,
		"mine":        c.AuthorID == viewer.ID,
	}
}
