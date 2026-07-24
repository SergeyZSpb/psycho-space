package game

import "context"

// TurnResult is the outcome of one dialogue turn, returned by an Evaluator and
// passed straight to the client. The wire shape is stable across evaluation
// modes, so swapping MockEvaluator for the LLM one changes nothing downstream.
type TurnResult struct {
	Reply    string `json:"reply"`    // the character's line back
	Emotion  string `json:"emotion"`  // asset/emotion key the judge chose
	Achieved bool   `json:"achieved"` // has the player convinced them (goal reached)?
}

// Evaluator judges one dialogue turn: given the character, the ids of options
// chosen so far, and the option chosen this turn, it decides the character's
// reply, emotion, and whether the goal is now reached.
//
// MockEvaluator scores authored per-option effects; a future OpenAIEvaluator
// will send the character persona (Motivation/Persona/TalkStyle) + the goal +
// the conversation to an OpenAI-compatible model (Yandex Cloud, DeepSeek) and
// have it decide achieved + reply + emotion. The service depends on this
// interface, so the LLM version is a drop-in (see NewEvaluator).
type Evaluator interface {
	Judge(ctx context.Context, ch Character, priorOptionIDs []string, optionID string) (TurnResult, error)
}

// NewEvaluator picks the evaluator for the configured mode. Today it always
// returns the mock; when LLM credentials are provisioned, return an
// OpenAI-compatible evaluator here when enabled — nothing else changes.
//
//	llmEnabled, model → future switch point
func NewEvaluator(llmEnabled bool, _ string) Evaluator {
	// TODO(llm): if llmEnabled { return newOpenAIEvaluator(baseURL, key, model) }
	_ = llmEnabled
	return MockEvaluator{}
}

// MockEvaluator is a deterministic stand-in for the AI judge: it sums the hidden
// per-option Effect across the conversation and declares success once the
// character's Threshold is met, choosing an emotion from the running score. It
// does no I/O and ignores ctx.
type MockEvaluator struct{}

// Judge implements Evaluator.
func (MockEvaluator) Judge(_ context.Context, ch Character, priorOptionIDs []string, optionID string) (TurnResult, error) {
	opt, ok := ch.findOption(optionID)
	if !ok {
		return TurnResult{}, ErrOptionNotFound
	}
	conviction := opt.Effect
	for _, id := range priorOptionIDs {
		if prev, ok := ch.findOption(id); ok {
			conviction += prev.Effect
		}
	}
	achieved := conviction >= ch.Threshold
	return TurnResult{
		Reply:    opt.Reply,
		Emotion:  mockEmotion(conviction, achieved),
		Achieved: achieved,
	}, nil
}

// mockEmotion maps a running conviction score to an emotion key. Keep the keys
// in sync with Character.Emotions / the frontend asset map.
func mockEmotion(conviction int, achieved bool) string {
	switch {
	case achieved:
		return "pleased"
	case conviction <= -1:
		return "annoyed"
	case conviction == 0:
		return "suspicious"
	case conviction == 1:
		return "neutral"
	default: // >= 2
		return "warming"
	}
}
