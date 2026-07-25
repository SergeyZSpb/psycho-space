// Package gamekhimki owns one game — «Смолтолк в Химках» — end to end: the
// character dialogues driven by an AI judge, plus recorded results
// (game_khimki_runs) for a leaderboard.
//
// One package per game is deliberate. Every game is a self-contained module
// named gamekhimki / game_khimki_* / /api/game-khimki across code, routes and
// tables, so removing a game is `git grep -il gamekhimki` and nothing else.
// Shared infrastructure (realtime, session, account, crypto, db, httpapi) is
// pointedly NOT prefixed — the absence of the prefix is what marks a package
// game-agnostic.
//
// The game key is "smalltalk_khimki": you must convince a character (default:
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
package gamekhimki

import (
	"errors"
	"strings"
	"time"
)

// Known game keys.
const (
	// GameSmalltalkKhimki is the first game: «смолтолк в химках».
	GameSmalltalkKhimki = "smalltalk_khimki"
)

// maxSteps bounds a submitted step count (defence against garbage input).
const maxSteps = 1000

// The tension scale. It is cross-turn state the client carries: it sends the
// current value with each turn, the judge returns the new one, and at
// AngerLoseAt the character snaps and the run is lost automatically — so a
// player who keeps pushing always eventually loses, which a judge left to its
// own devices was reluctant to enforce.
//
// StartAnger is deliberately well above zero: дядя Ваня opens hostile.
//
// AngerLoseAt is below MaxAnger on purpose. Measured against the real model, the
// scale crawled 85 → 90 → 95 → 95 and stalled: it would not cross its own
// ceiling, so the run never ended. Ending it at 90 makes losing actually
// reachable instead of theoretical.
const (
	MaxAnger    = 100
	AngerLoseAt = 90
	StartAnger  = 40
)

// Theme is one of the deep subjects the player has to genuinely open before the
// character warms up. Server-only: the key is the judge's vocabulary for
// reporting progress, the label tells it what the key means. Which themes are
// still closed steers the option the judge offers next — the player is never
// shown the list, or the game would play itself.
//
// Keywords are a deliberately crude substring vocabulary used to measure how much
// a conversation has actually engaged with the theme. It exists because the judge
// cannot be trusted with this on its own: measured in prod it marked `sahur` open
// on turn one, then talked about drinking for twenty turns without ever marking
// `alcohol`. Engagement is a check on both mistakes — see themeEngagement.
type Theme struct {
	Key      string
	Label    string
	Keywords []string
}

// matches reports whether text engages the theme. Case-insensitive substrings, so
// stems catch inflections: Russian declines everything.
func (t Theme) matches(text string) bool {
	low := strings.ToLower(text)
	for _, k := range t.Keywords {
		if strings.Contains(low, k) {
			return true
		}
	}
	return false
}

// How much engagement a theme needs before the judge's claim that it is open is
// believed, and after how much the server stops waiting and marks it open itself.
//
// Both exist to break a loop measured in prod: the first option slot always
// steered at a still-closed theme, so once two of three were marked the slot
// pushed the last one every single turn, the whole conversation became that one
// subject, the judge never marked it — and 15 of 20 option sets ended up with all
// four choices on the same topic, with the run unwinnable.
const (
	themeConfirmTurns  = 2
	themeAutoDoneTurns = 4
)

// ClampAnger bounds a tension value to the scale.
func ClampAnger(v int) int {
	if v < 0 {
		return 0
	}
	if v > MaxAnger {
		return MaxAnger
	}
	return v
}

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
	ErrUnknownGame      = errors.New("gamekhimki: unknown game key")
	ErrUnknownCharacter = errors.New("gamekhimki: unknown character")
	ErrStepsRange       = errors.New("gamekhimki: steps out of range")
	ErrAssetNotFound    = errors.New("gamekhimki: asset not found")

	// ErrLLMUnparsable means the model answered HTTP 200 but with something we
	// cannot use as a turn — in practice its content filter replying in plain
	// prose instead of the JSON we ask for. It is a property of the dialogue,
	// not an outage: the same input fails again, so the player must say
	// something else. Callers map it to 4xx, not 5xx.
	ErrLLMUnparsable = errors.New("gamekhimki: llm reply was not usable JSON")
)

// KnownGame reports whether key names a game we serve.
func KnownGame(key string) bool {
	return key == GameSmalltalkKhimki
}
