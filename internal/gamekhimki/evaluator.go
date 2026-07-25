package gamekhimki

import "context"

// Exchange is one completed dialogue turn: what the player chose to say, how the
// character replied, and which answer options the judge offered afterwards. The
// client accumulates these and sends them back each turn as the conversation so
// far (the backend is stateless per turn).
//
// Options are carried so the judge can see what it has already put in front of
// the player and stop offering the same four lines every turn. They are dropped
// from the context by the same window as the rest of the exchange.
type Exchange struct {
	Choice  string   `json:"choice"`
	Reply   string   `json:"reply"`
	Options []string `json:"options,omitempty"`
	// Art, Anger and ThemesDone are the rest of what the judge returned that turn.
	// The client sends them all back so the history can be replayed to the model as
	// the JSON it actually produced — see judgeReplayJSON. Anything missing here
	// would teach the model to omit that field too, which is why art and the theme
	// set are carried and not just the tension.
	Art        string   `json:"art,omitempty"`
	Anger      *int     `json:"anger,omitempty"`
	ThemesDone []string `json:"themes_done,omitempty"`
}

// TurnResult is the outcome of one dialogue turn, judged by the LLM and passed
// straight to the client.
//
// Options are the answer choices for the NEXT turn — generated fresh by the LLM
// each turn (always 4 while playing); empty ends the dialogue, either because
// the goal was reached (Achieved) or because the character snapped (GameOver).
type TurnResult struct {
	Reply    string   `json:"reply"`     // the character's line back
	Art      string   `json:"art"`       // one of Character.Arts (mood or story/location art)
	Achieved bool     `json:"achieved"`  // has the player convinced them (goal reached)?
	GameOver bool     `json:"game_over"` // has the player pushed them too far (run lost)?
	Anger    int      `json:"anger"`     // tension after this turn; at AngerLoseAt the run is lost
	Options  []string `json:"options"`   // answer options for the next turn (4 while playing)

	// ThemesDone are the character's deep subjects the judge considers genuinely
	// opened so far. Carried by the client between turns like Anger, and used only
	// to steer the next options toward what is still closed — never rendered, and
	// never trusted as the win condition (that stays the judge's call on the
	// dialogue itself, so a tampering client cannot award itself the ending).
	ThemesDone []string `json:"themes_done"`
}

// Evaluator judges one dialogue turn: given the character, the conversation so
// far, what the player just said (choice; empty on the opening turn), the tension
// going in, and which themes are already open, it decides the character's reply,
// emotion, the new tension, the new theme progress, whether the goal is reached,
// and the next answer options. The only implementation is the OpenAI-compatible
// LLM judge (see llm.go); the game requires an LLM endpoint to be configured.
type Evaluator interface {
	Judge(ctx context.Context, ch Character, transcript []Exchange, choice string, anger int, themesDone []string) (TurnResult, error)
}
