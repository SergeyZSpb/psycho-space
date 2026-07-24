package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/SergeyZSpb/psycho-space/internal/game"
)

// handleGameConfig serves a game's config (characters, options, assets). Persona
// prompts and answer keys are hidden by the game package's json tags.
func (s *Server) handleGameConfig(w http.ResponseWriter, r *http.Request) {
	g, err := s.d.Game.Content(r.URL.Query().Get("game"))
	if err != nil {
		if errors.Is(err, game.ErrUnknownGame) {
			writeError(w, r, http.StatusNotFound, "unknown_game")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, g)
}

// handleGameAttempt judges one dialogue turn. The result shape is stable whether
// judging is mock (today) or LLM-driven (later).
func (s *Server) handleGameAttempt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GameKey      string   `json:"game_key"`
		CharacterKey string   `json:"character_key"`
		History      []string `json:"history"` // ids of options chosen so far
		OptionID     string   `json:"option_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	res, err := s.d.Game.Judge(r.Context(), req.GameKey, req.CharacterKey, req.History, req.OptionID)
	if err != nil {
		switch {
		case errors.Is(err, game.ErrUnknownGame):
			writeError(w, r, http.StatusNotFound, "unknown_game")
		case errors.Is(err, game.ErrUnknownCharacter):
			writeError(w, r, http.StatusNotFound, "unknown_character")
		case errors.Is(err, game.ErrOptionNotFound):
			writeError(w, r, http.StatusBadRequest, "unknown_option")
		default:
			writeError(w, r, http.StatusInternalServerError, "internal")
		}
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleGameSubmitRun records a finished play-through (goal reached or budget spent).
func (s *Server) handleGameSubmitRun(w http.ResponseWriter, r *http.Request) {
	viewer, _ := accountFromContext(r.Context())
	var req struct {
		GameKey      string `json:"game_key"`
		CharacterKey string `json:"character_key"`
		Success      bool   `json:"success"`
		Steps        int    `json:"steps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	run, err := s.d.Game.SubmitRun(r.Context(), viewer.ID, req.GameKey, req.CharacterKey, req.Success, req.Steps)
	if err != nil {
		switch {
		case errors.Is(err, game.ErrUnknownGame):
			writeError(w, r, http.StatusNotFound, "unknown_game")
		case errors.Is(err, game.ErrUnknownCharacter):
			writeError(w, r, http.StatusNotFound, "unknown_character")
		case errors.Is(err, game.ErrStepsRange):
			writeError(w, r, http.StatusUnprocessableEntity, "steps_range")
		default:
			writeError(w, r, http.StatusInternalServerError, "internal")
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":            run.ID,
		"game_key":      run.GameKey,
		"character_key": run.CharacterKey,
		"success":       run.Success,
		"steps":         run.Steps,
		"created_at":    run.CreatedAt,
	})
}

// handleGameLeaderboard returns each account's aggregate for a game, enriched
// with display info (decrypted once per account).
func (s *Server) handleGameLeaderboard(w http.ResponseWriter, r *http.Request) {
	viewer, _ := accountFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	entries, err := s.d.Game.Leaderboard(r.Context(), r.URL.Query().Get("game"), limit)
	if err != nil {
		if errors.Is(err, game.ErrUnknownGame) {
			writeError(w, r, http.StatusNotFound, "unknown_game")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}

	players := map[string]*account.Account{}
	for _, e := range entries {
		if _, ok := players[e.AccountID]; !ok {
			if a, err := s.d.Accounts.GetByID(r.Context(), e.AccountID); err == nil {
				players[e.AccountID] = a
			}
		}
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		player := map[string]any{"display_name": "", "avatar_url": "", "vk_url": ""}
		if a := players[e.AccountID]; a != nil {
			player = map[string]any{"display_name": a.DisplayName(), "avatar_url": a.AvatarURL, "vk_url": a.VKURL()}
		}
		out = append(out, map[string]any{
			"player":    player,
			"successes": e.Successes,
			"plays":     e.Plays,
			"steps":     e.Steps,
			"mine":      e.AccountID == viewer.ID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

// handleGameStats returns the current player's summary for a game.
func (s *Server) handleGameStats(w http.ResponseWriter, r *http.Request) {
	viewer, _ := accountFromContext(r.Context())
	st, err := s.d.Game.Stats(r.Context(), r.URL.Query().Get("game"), viewer.ID)
	if err != nil {
		if errors.Is(err, game.ErrUnknownGame) {
			writeError(w, r, http.StatusNotFound, "unknown_game")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"successes":  st.Successes,
		"plays":      st.Plays,
		"best_steps": st.BestSteps,
	})
}
