// Package game owns the mini-games section: character dialogues driven by an AI
// judge, plus recorded results (game_runs) for a leaderboard.
//
// The first game is "smalltalk_khimki": you must convince a character (default:
// сосед дядя Ваня at the подъезд) to let you pass, choosing answer options turn
// by turn. Each turn the Evaluator (an OpenAI-compatible LLM — Yandex Cloud /
// DeepSeek; see llm.go) replies in character, judges whether the goal is reached
// yet, picks the character's emotion, and generates the next answer options
// (fewer each turn). The game requires an LLM endpoint to be configured
// (config.LLM); when it is not, the /attempt endpoint returns 503.
//
// Character profiles (goal, motivation, persona, talk style, emotions) are
// config (see content.go), editable without touching the frontend; a default
// character is selected per game. Answer options are NOT authored — the LLM
// generates them.
package game

import (
	"errors"
	"time"
)

// Known game keys.
const (
	// GameSmalltalkKhimki is the first game: «смолтолк в химках».
	GameSmalltalkKhimki = "smalltalk_khimki"
)

// maxSteps bounds a submitted step count (defence against garbage input).
const maxSteps = 1000

// Run is a recorded, finished play-through of one character dialogue.
type Run struct {
	ID           string
	AccountID    string
	GameKey      string
	CharacterKey string
	Success      bool
	Steps        int
	CreatedAt    time.Time
}

// LeaderboardEntry is one account's aggregate for a game: how many successes,
// how many plays, and total dialogue steps.
type LeaderboardEntry struct {
	AccountID string
	Successes int
	Plays     int
	Steps     int
}

// PlayerStats is a single player's summary for a game.
type PlayerStats struct {
	Successes int
	Plays     int
	BestSteps int // fewest steps in a successful run (0 if none yet)
}

// Errors.
var (
	ErrUnknownGame      = errors.New("game: unknown game key")
	ErrUnknownCharacter = errors.New("game: unknown character")
	ErrStepsRange       = errors.New("game: steps out of range")
)

// KnownGame reports whether key names a game we serve.
func KnownGame(key string) bool {
	return key == GameSmalltalkKhimki
}
