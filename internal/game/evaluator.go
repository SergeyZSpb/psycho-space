package game

import "context"

// Exchange is one completed dialogue turn: what the player chose to say and how
// the character replied. The client accumulates these and sends them back each
// turn as the conversation so far (the backend is stateless per turn).
type Exchange struct {
	Choice string `json:"choice"`
	Reply  string `json:"reply"`
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
	Options  []string `json:"options"`   // answer options for the next turn (4 while playing)
}

// Evaluator judges one dialogue turn: given the character, the conversation so
// far, and what the player just said (choice; empty on the opening turn), it
// decides the character's reply, emotion, whether the goal is reached, and the
// next answer options. The only implementation is the OpenAI-compatible LLM
// judge (see llm.go); the game requires an LLM endpoint to be configured.
type Evaluator interface {
	Judge(ctx context.Context, ch Character, transcript []Exchange, choice string) (TurnResult, error)
}
