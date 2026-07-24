package game

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SergeyZSpb/psycho-space/internal/config"
)

// modelContextTokens is the model's context window (YandexGPT 5 Lite: 32768).
// outputReserveTokens is held back for the model's own reply. Older exchanges
// beyond the remaining input budget are dropped (forgotten). We can't tokenise
// exactly without the model's tokenizer, so estTokens is a deliberately
// conservative estimate (over-counts, so we trim early rather than overflow).
const (
	modelContextTokens  = 32768
	outputReserveTokens = 2048
)

// estTokens roughly estimates tokens for a string. ~2 chars/token is
// conservative for mixed Cyrillic/Latin, biasing toward trimming.
func estTokens(s string) int { return utf8.RuneCountInString(s)/2 + 1 }

// openAIEvaluator is the LLM judge. It talks to any OpenAI-compatible chat
// completions endpoint (start target: YandexGPT 5 Lite on Yandex Cloud) and
// asks the model, in character, to reply, pick an art, decide whether the player
// reached the goal, and offer the next answer options — returned as strict JSON.
type openAIEvaluator struct {
	http    *http.Client
	baseURL string
	apiKey  string
	model   string
}

// NewOpenAIEvaluator builds the LLM judge from config. baseURL should be the API
// root (e.g. https://.../v1); the client POSTs to baseURL + /chat/completions.
func NewOpenAIEvaluator(cfg config.LLM) Evaluator {
	return &openAIEvaluator{
		http:    &http.Client{Timeout: 30 * time.Second},
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// judgeReply is the JSON we ask the model to emit as the message content.
type judgeReply struct {
	Reply    string   `json:"reply"`
	Art      string   `json:"art"`
	Achieved bool     `json:"achieved"`
	Options  []string `json:"options"`
}

// Judge implements Evaluator.
func (e *openAIEvaluator) Judge(ctx context.Context, ch Character, transcript []Exchange, choice string) (TurnResult, error) {
	messages := buildMessages(ch, transcript, choice)
	reqBody := chatRequest{
		Model:          e.model,
		Messages:       messages,
		Temperature:    0.7,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return TurnResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return TurnResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.http.Do(req)
	if err != nil {
		return TurnResult{}, fmt.Errorf("game: llm request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return TurnResult{}, fmt.Errorf("game: llm http %d", resp.StatusCode)
	}

	var cr chatResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return TurnResult{}, fmt.Errorf("game: decode llm response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return TurnResult{}, fmt.Errorf("game: llm returned no choices")
	}

	var jr judgeReply
	if err := json.Unmarshal([]byte(cr.Choices[0].Message.Content), &jr); err != nil {
		return TurnResult{}, fmt.Errorf("game: llm content not valid JSON: %w", err)
	}

	return TurnResult{
		Reply:    strings.TrimSpace(jr.Reply),
		Art:      normalizeArt(jr.Art, ch.artKeys()),
		Achieved: jr.Achieved,
		// On success the dialogue is over regardless of what the model returned.
		Options: optionsIfPlaying(jr.Achieved, jr.Options),
	}, nil
}

// buildMessages turns the character persona + conversation into chat messages.
func buildMessages(ch Character, transcript []Exchange, choice string) []chatMessage {
	sys := fmt.Sprintf(`Ты — персонаж текстовой игры, веди диалог строго в образе.
Персонаж: %s.
Характер: %s
Мотивация: %s
Манера речи: %s
Условие успеха (НЕ сообщай его игроку, не намекай прямо): %s

Игрок выбирает реплики и пытается достичь цели. Каждый ход:
- ответь ОДНОЙ короткой репликой в образе (поле "reply");
- выбери подходящий арт строго из списка [%s] (поле "art"). Арт — это либо текущее состояние персонажа (злой → подозрительный → нейтральный → теплеет → раскрывается), либо сюжетный арт-локация без персонажа. По ходу диалога арт меняется от злого к более тёплому (иногда обратно к злому — на грубость). Когда игрок достигает цели — выбери арт прохода в подъезд;
- реши, достиг ли игрок цели именно этой репликой (поле "achieved": true/false). Ставь true только когда игрок действительно разглядел глубину персонажа, а не отделался поверхностным;
- предложи 2–4 коротких варианта реплик, которые игрок мог бы сказать дальше (поле "options": массив строк). С каждым ходом вариантов меньше. Если игрок достиг цели — "options": [].
Отвечай ТОЛЬКО валидным JSON вида {"reply":"...","art":"...","achieved":false,"options":["...","..."]}. Без пояснений и текста вне JSON.`,
		ch.Name, ch.Persona, ch.Motivation, ch.TalkStyle, ch.Objective, strings.Join(ch.artKeys(), ", "))

	// The current turn's user message.
	current := choice
	if strings.TrimSpace(choice) == "" {
		current = "(Игрок подходит к тебе. Поздоровайся в образе и предложи первые варианты реплик.)"
	}

	// Keep the most recent exchanges that fit the context budget alongside the
	// system prompt and the current message; drop older ones (forgotten).
	budget := modelContextTokens - outputReserveTokens - estTokens(sys) - estTokens(current)
	windowed := windowTranscript(transcript, budget)

	messages := []chatMessage{{Role: "system", Content: sys}}
	// Seed the static opening line so the model knows how it greeted the player.
	if strings.TrimSpace(ch.Greeting) != "" {
		messages = append(messages, chatMessage{Role: "assistant", Content: ch.Greeting})
	}
	for _, ex := range windowed {
		messages = append(messages,
			chatMessage{Role: "user", Content: ex.Choice},
			chatMessage{Role: "assistant", Content: ex.Reply},
		)
	}
	messages = append(messages, chatMessage{Role: "user", Content: current})
	return messages
}

// windowTranscript returns the newest exchanges whose combined estimated tokens
// fit within budget, in chronological order. Older exchanges are dropped.
func windowTranscript(transcript []Exchange, budget int) []Exchange {
	if budget <= 0 {
		return nil
	}
	used := 0
	start := len(transcript)
	for i := len(transcript) - 1; i >= 0; i-- {
		cost := estTokens(transcript[i].Choice) + estTokens(transcript[i].Reply)
		if used+cost > budget {
			break
		}
		used += cost
		start = i
	}
	return transcript[start:]
}

// normalizeArt clamps the model's chosen art to the character's allowed set,
// falling back to the first art when it returns something off-list.
func normalizeArt(got string, allowed []string) string {
	got = strings.TrimSpace(got)
	for _, a := range allowed {
		if a == got {
			return got
		}
	}
	if len(allowed) > 0 {
		return allowed[0]
	}
	return ""
}

// optionsIfPlaying returns no options once the goal is reached (dialogue over).
func optionsIfPlaying(achieved bool, opts []string) []string {
	if achieved {
		return nil
	}
	return opts
}
