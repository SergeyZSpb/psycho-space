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

// RecordBoard names one of the leaderboard's four record rankings. Each ranks
// players by their best (or worst) SINGLE run, not by an aggregate.
type RecordBoard string

// The four boards: longest/shortest dialogue that ended in a win, and the same
// two for a loss.
const (
	BoardLongestWin   RecordBoard = "longest_win"
	BoardShortestWin  RecordBoard = "shortest_win"
	BoardLongestLoss  RecordBoard = "longest_loss"
	BoardShortestLoss RecordBoard = "shortest_loss"
)

// RecordBoards lists every board, in display order.
var RecordBoards = []RecordBoard{BoardLongestWin, BoardShortestWin, BoardLongestLoss, BoardShortestLoss}

// PlayerRecords is one account's extreme single runs for a game, plus its
// win/loss tally. A nil record field means the player has no run of that kind
// yet (no wins, or no losses), and the account is left off that board.
type PlayerRecords struct {
	AccountID    string
	Plays        int
	Wins         int
	Losses       int
	LongestWin   *int
	ShortestWin  *int
	LongestLoss  *int
	ShortestLoss *int
}

// RecordEntry is one row of a record board: the step count that earned the place,
// plus the player's overall tally shown alongside it.
type RecordEntry struct {
	AccountID string
	Steps     int
	Plays     int
	Wins      int
	Losses    int
}

// Boards is the whole leaderboard — every board keyed by name, each already
// sorted and capped.
type Boards map[RecordBoard][]RecordEntry

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
	ErrAssetNotFound    = errors.New("game: asset not found")

	// ErrLLMUnparsable means the model answered HTTP 200 but with something we
	// cannot use as a turn — in practice its content filter replying in plain
	// prose instead of the JSON we ask for. It is a property of the dialogue,
	// not an outage: the same input fails again, so the player must say
	// something else. Callers map it to 4xx, not 5xx.
	ErrLLMUnparsable = errors.New("game: llm reply was not usable JSON")
)

// KnownGame reports whether key names a game we serve.
func KnownGame(key string) bool {
	return key == GameSmalltalkKhimki
}
