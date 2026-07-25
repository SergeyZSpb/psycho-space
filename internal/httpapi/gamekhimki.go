package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/SergeyZSpb/psycho-space/internal/account"
	"github.com/SergeyZSpb/psycho-space/internal/gamekhimki"
)

// handleGameKhimkiConfig serves the game's config (characters, options, assets).
// Persona prompts and answer keys are hidden by the gamekhimki package's json tags.
func (s *Server) handleGameKhimkiConfig(w http.ResponseWriter, r *http.Request) {
	g, err := s.d.GameKhimki.Content(r.URL.Query().Get("game"))
	if err != nil {
		if errors.Is(err, gamekhimki.ErrUnknownGame) {
			writeError(w, r, http.StatusNotFound, "unknown_game")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}
	// Point arts at their uploaded image (if any); arts without an image keep an
	// empty Image and the client renders the emoji placeholder.
	present, err := s.d.GameKhimki.PresentArtKeys(r.Context(), g.GameKey)
	if err != nil {
		slog.WarnContext(r.Context(), "game-khimki asset keys lookup failed", "err", err)
		present = nil
	}
	for ci := range g.Characters {
		for ai := range g.Characters[ci].Arts {
			if k := g.Characters[ci].Arts[ai].Key; present[k] {
				g.Characters[ci].Arts[ai].Image = GameAssetPath + g.GameKey + "/" + k
			}
		}
	}
	writeJSON(w, http.StatusOK, g)
}

// handleGameKhimkiAttempt judges one dialogue turn via the LLM. Requires a configured
// LLM endpoint (config.LLM); otherwise 503, like VK.
func (s *Server) handleGameKhimkiAttempt(w http.ResponseWriter, r *http.Request) {
	if !s.d.Config.LLM.Enabled() {
		writeError(w, r, http.StatusServiceUnavailable, "llm_not_configured")
		return
	}
	var req struct {
		GameKey      string                `json:"game_key"`
		CharacterKey string                `json:"character_key"`
		Transcript   []gamekhimki.Exchange `json:"transcript"` // conversation so far
		Choice       string                `json:"choice"`     // player's latest line ("" = opening turn)
		// Anger is the tension carried over from the previous turn. A pointer so
		// an omitted field starts the scale where the character starts, rather
		// than at the calmest possible value.
		Anger *int `json:"anger"`
		// ThemesDone is the theme progress carried over. Clamped to the
		// character's own keys downstream; it only steers the next options and is
		// never trusted as the win condition.
		ThemesDone []string `json:"themes_done"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	anger := gamekhimki.StartAnger
	if req.Anger != nil {
		anger = *req.Anger
	}
	res, err := s.d.GameKhimki.Judge(r.Context(), req.GameKey, req.CharacterKey, req.Transcript, req.Choice, anger, req.ThemesDone)
	if err != nil {
		switch {
		case errors.Is(err, gamekhimki.ErrUnknownGame):
			writeError(w, r, http.StatusNotFound, "unknown_game")
		case errors.Is(err, gamekhimki.ErrUnknownCharacter):
			writeError(w, r, http.StatusNotFound, "unknown_character")
		case errors.Is(err, gamekhimki.ErrLLMUnparsable):
			// The model answered, but with something we can't turn into a move
			// (usually its content filter replying in prose). Nothing is broken
			// server-side and a retry of the same line fails the same way, so
			// this is 422: the SPA asks the player to try a different line and
			// shows the trace id. Already logged in full by the evaluator.
			writeError(w, r, http.StatusUnprocessableEntity, "llm_unparsable")
		default:
			// LLM/network failure — the judge is a hard dependency here.
			slog.ErrorContext(r.Context(), "game-khimki judge failed", "err", err)
			writeError(w, r, http.StatusBadGateway, "llm_error")
		}
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleGameKhimkiSubmitRun records a finished play-through (goal reached or budget spent).
func (s *Server) handleGameKhimkiSubmitRun(w http.ResponseWriter, r *http.Request) {
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
	run, err := s.d.GameKhimki.SubmitRun(r.Context(), viewer.ID, req.GameKey, req.CharacterKey, req.Success, req.Steps)
	if err != nil {
		switch {
		case errors.Is(err, gamekhimki.ErrUnknownGame):
			writeError(w, r, http.StatusNotFound, "unknown_game")
		case errors.Is(err, gamekhimki.ErrUnknownCharacter):
			writeError(w, r, http.StatusNotFound, "unknown_character")
		case errors.Is(err, gamekhimki.ErrStepsRange):
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

// handleGameKhimkiLeaderboard returns the four record boards for a game (longest and
// shortest winning dialogue, longest and shortest losing one), each row enriched
// with display info. Accounts are decrypted once and reused across boards.
func (s *Server) handleGameKhimkiLeaderboard(w http.ResponseWriter, r *http.Request) {
	viewer, _ := accountFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	boards, err := s.d.GameKhimki.Leaderboard(r.Context(), r.URL.Query().Get("game"), limit)
	if err != nil {
		if errors.Is(err, gamekhimki.ErrUnknownGame) {
			writeError(w, r, http.StatusNotFound, "unknown_game")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal")
		return
	}

	players := map[string]*account.Account{}
	for _, entries := range boards {
		for _, e := range entries {
			if _, ok := players[e.AccountID]; !ok {
				if a, err := s.d.Accounts.GetByID(r.Context(), e.AccountID); err == nil {
					players[e.AccountID] = a
				}
			}
		}
	}

	out := make(map[string]any, len(gamekhimki.RecordBoards))
	for _, board := range gamekhimki.RecordBoards {
		rows := make([]map[string]any, 0, len(boards[board]))
		for _, e := range boards[board] {
			player := map[string]any{"display_name": "", "avatar_url": "", "vk_url": ""}
			if a := players[e.AccountID]; a != nil {
				player = map[string]any{"display_name": a.DisplayName(), "avatar_url": a.AvatarURL, "vk_url": a.VKURL()}
			}
			rows = append(rows, map[string]any{
				"player": player,
				"steps":  e.Steps,
				"plays":  e.Plays,
				"wins":   e.Wins,
				"losses": e.Losses,
				"mine":   e.AccountID == viewer.ID,
			})
		}
		out[string(board)] = rows
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": out})
}

// handleGameKhimkiStats returns the current player's summary for a game.
func (s *Server) handleGameKhimkiStats(w http.ResponseWriter, r *http.Request) {
	viewer, _ := accountFromContext(r.Context())
	st, err := s.d.GameKhimki.Stats(r.Context(), r.URL.Query().Get("game"), viewer.ID)
	if err != nil {
		if errors.Is(err, gamekhimki.ErrUnknownGame) {
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
