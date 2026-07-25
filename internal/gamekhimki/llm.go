package gamekhimki

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SergeyZSpb/psycho-space/internal/config"
)

// modelPrice is a model's rouble price per 1000 tokens. Input and output are
// priced separately — on deepseek-v4-flash output costs well over six times
// input, so a single blended rate would misreport every turn.
type modelPrice struct{ in, out float64 }

// modelPrices is keyed by a substring of the model URI (the URI carries a folder
// id, so it can't be matched whole). Prices from the Yandex AI Studio pricing
// page. The cached-input rate (0.075) is not modelled: the API reports how many
// input tokens were cached, but not the split we were billed at, so the estimate
// is an UPPER bound — real spend is lower whenever the prefix cache hits.
//
// A model that is not listed logs NO cost estimate rather than a wrong one: a
// stale price silently attributed to a different model is worse than none.
var modelPrices = map[string]modelPrice{
	"yandexgpt-5-lite":  {in: 0.20, out: 0.20},
	"deepseek-v4-flash": {in: 0.30, out: 0.50},
}

// costEstimate returns the estimated rouble cost of a call and whether we have
// prices for that model at all.
func costEstimate(model string, promptTokens, completionTokens int) (float64, bool) {
	for name, p := range modelPrices {
		if strings.Contains(model, name) {
			return float64(promptTokens)/1000*p.in + float64(completionTokens)/1000*p.out, true
		}
	}
	return 0, false
}

// optionCount is how many answer options every playing turn offers.
const optionCount = 4

// angerDriftOnMissing is how far the tension moves when the model answers
// without an "anger" field. Small, but never zero: a stalled scale means an
// unloseable run.
const angerDriftOnMissing = 5

// modelContextTokens is the context window of the model we run on
// (deepseek-v4-flash: 1048576, measured against the live endpoint — an oversized
// request answers "This model's maximum context length is 1048576 tokens").
// outputReserveTokens is held back for the model's own reply. Older exchanges
// beyond the remaining input budget are dropped (forgotten).
//
// The model's window is a safety bound, not the budget we actually use — see
// historyTokens below.
const (
	modelContextTokens  = 1048576
	outputReserveTokens = 2048
)

// historyTokens and historyExchanges cap how much conversation we resend, far
// below what the model would accept.
//
// The whole prompt is re-sent on every single turn, so an uncapped history makes
// each turn more expensive than the last — quadratic in the length of the game —
// and buys very little: the game's actual progress is carried as explicit state
// (the tension scale and the opened themes), and repetition is handled by the
// already-offered and recent-replies lists. Old small talk only adds flavour.
//
// Sized from measured turns rather than from what the model would tolerate: one
// exchange (a choice, a reply and four offered options) is roughly 400–500 tokens
// of Cyrillic, so twelve turns is ~6000. That covers a whole playthrough — the
// longest observed run reached the ending in seven turns — and then holds flat:
// turn 30 costs what turn 12 costs instead of several times as much.
//
// Twelve rather than the ~114 exchanges the old 32k budget effectively allowed,
// but not fewer, because history is genuinely useful here even though progress
// itself is explicit state (tension + opened themes) and repetition is handled by
// the already-offered and recent-replies lists.
const (
	historyTokens    = 6000
	historyExchanges = 12
)

// maxCompletionTokens caps the model's own answer, to stop a runaway generation
// costing many times a normal turn.
//
// 1500, not the 900 it started at: with four role-differentiated options the model
// writes considerably more than the ~450 tokens a bare reply-plus-options took, and
// 900 truncated it mid-JSON — `finish_reason: length`, unparsable, the whole turn
// wasted. A cap that truncates is more expensive than one that lets the answer
// finish, since a truncated turn is billed in full and delivers nothing.
const maxCompletionTokens = 1500

// reasoningEffort turns off the model's chain of thought.
//
// deepseek-v4-flash is a reasoning model: it emits `reasoning_content` alongside
// the answer, billed as output — the dearest rate. Twice it spent the ENTIRE
// max_tokens budget reasoning and returned empty content (finish_reason "length",
// 1500 completion tokens, 0 characters of reply), losing the turn. Turning it off
// removes that failure class and most of the output cost; the judging task is a
// rule-following one, not a puzzle. Empty string leaves the provider default.
const reasoningEffort = "none"

// estTokens estimates tokens for a string, deliberately erring high so we trim
// early rather than overflow. One token per rune: measured against
// deepseek-v4-flash, 1200000 Cyrillic characters tokenise to 1200004 tokens —
// i.e. Russian text is ~1 token per character, not the 2 characters per token an
// English-shaped estimate would assume. Latin runs compress far below this, so
// counting runes over-counts them, which is the safe direction.
func estTokens(s string) int { return utf8.RuneCountInString(s) + 1 }

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
	Model           string          `json:"model"`
	Messages        []chatMessage   `json:"messages"`
	Temperature     float64         `json:"temperature"`
	MaxTokens       int             `json:"max_tokens,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	ResponseFormat  *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"` // logged when a reply is unusable
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		// Cached input is billed at a fraction of the normal rate. Measured as 0
		// on this endpoint today, so it is logged to notice if that ever changes.
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// flexInt is an int that also accepts the quoted and decimal forms a small model
// drifts into — 42, "42", 42.0. Measured in prod: a complete, perfectly usable
// turn was thrown away as unparsable because the model wrote "anger": "35".
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil // treated as absent
	}
	if n, err := strconv.Atoi(s); err == nil {
		*f = flexInt(n)
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("gamekhimki: %q is not a number", s)
	}
	*f = flexInt(v)
	return nil
}

// flexBool likewise accepts true and "true" — the same class of slip as flexInt,
// and losing a whole turn over the quotes is not worth it.
type flexBool bool

func (f *flexBool) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	switch strings.ToLower(s) {
	case "true":
		*f = true
	case "false", "", "null":
		*f = false
	default:
		return fmt.Errorf("gamekhimki: %q is not a bool", s)
	}
	return nil
}

// judgeReply is the JSON we ask the model to emit as the message content.
type judgeReply struct {
	Reply    string   `json:"reply"`
	Art      string   `json:"art"`
	Achieved flexBool `json:"achieved"`
	GameOver flexBool `json:"game_over"`
	Options  []string `json:"options"`
	// Anger is a pointer so an omitted field is distinguishable from a returned
	// 0: a model that forgets the field must leave the tension where it was, not
	// silently reset it to calm. flexInt because it sometimes arrives quoted.
	Anger *flexInt `json:"anger"`
	// ThemesDone is likewise a pointer: an omitted field keeps the progress the
	// player already earned instead of wiping it.
	ThemesDone *[]string `json:"themes_done"`
}

// Judge implements Evaluator.
func (e *openAIEvaluator) Judge(ctx context.Context, ch Character, transcript []Exchange, choice string, anger int, themesDone []string) (TurnResult, error) {
	anger = ClampAnger(anger)
	themesDone = clampThemes(ch, themesDone)
	// What the conversation has actually engaged, over the whole run. Drives three
	// things: opening a theme the judge refuses to open, verifying what it claims,
	// and choosing which theme the first option steers at.
	eng := themeEngagement(ch, transcript, choice)
	themesDone = autoMarkThemes(ch, themesDone, eng)
	messages, alreadyOffered := buildMessages(ch, transcript, choice, anger, themesDone, eng)
	reqBody := chatRequest{
		Model:           e.model,
		Messages:        messages,
		Temperature:     0.7,
		MaxTokens:       maxCompletionTokens,
		ReasoningEffort: reasoningEffort,
		ResponseFormat:  &responseFormat{Type: "json_object"},
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
	req.Header.Set("Authorization", "Bearer "+e.apiKey) // key stays in the header, never logged

	slog.InfoContext(ctx, "game llm request",
		"model", e.model, "character", ch.Key, "choice", choice, "anger_in", anger,
		"messages", len(messages), "transcript_len", len(transcript))

	start := time.Now()
	resp, err := e.http.Do(req)
	if err != nil {
		return TurnResult{}, fmt.Errorf("gamekhimki: llm request: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return TurnResult{}, fmt.Errorf("gamekhimki: llm http %d: %s", resp.StatusCode, snippet(body))
	}

	var cr chatResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return TurnResult{}, fmt.Errorf("gamekhimki: decode llm response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return TurnResult{}, fmt.Errorf("gamekhimki: llm returned no choices: %s", snippet(body))
	}

	var jr judgeReply
	content := cr.Choices[0].Message.Content
	err = json.Unmarshal([]byte(content), &jr)
	salvaged := false
	if err != nil {
		// A small model garbles its own JSON now and then — a raw newline inside a
		// string, a stray key where a bracket belonged. The turn is still in there,
		// so try to recover it rather than charging the player for the model's
		// typo. Only a genuinely unreadable reply (a content-filter refusal in
		// prose, say) falls through to the error below.
		if recovered, ok := salvageJudgeReply(content); ok {
			jr, err, salvaged = recovered, nil, true
			slog.WarnContext(ctx, "game llm reply salvaged",
				"model", e.model, "character", ch.Key,
				"content", clampRunes(content, maxLoggedRunes),
				"options", len(jr.Options))
		}
	}
	if err != nil {
		// The usual cause is the provider's content filter answering in plain
		// prose instead of the model (it ignores response_format). This is the
		// only place the raw material exists, and prod runs at Info so the Debug
		// line below never fires — log the whole picture here, and let the
		// handler own only the HTTP mapping.
		slog.ErrorContext(ctx, "game llm reply not json",
			"model", e.model, "character", ch.Key, "choice", choice,
			"transcript_len", len(transcript), "latency_ms", elapsed.Milliseconds(),
			"finish_reason", cr.Choices[0].FinishReason,
			"prompt_tokens", cr.Usage.PromptTokens, "completion_tokens", cr.Usage.CompletionTokens,
			"total_tokens", cr.Usage.TotalTokens,
			"parse_err", err.Error(),
			"content", clampRunes(content, maxLoggedRunes),
			"raw_response", clampRunes(string(body), maxLoggedRunes))
		if cr.Choices[0].FinishReason == "length" {
			// Truncated, not malformed: the answer ran into max_tokens. Say so, or
			// the next reader sees "unexpected end of JSON input" and hunts a
			// formatting bug that isn't there.
			return TurnResult{}, fmt.Errorf(
				"gamekhimki: llm reply truncated at max_tokens=%d (finish_reason=length, %d completion tokens): %w",
				maxCompletionTokens, cr.Usage.CompletionTokens, ErrLLMUnparsable)
		}
		return TurnResult{}, fmt.Errorf("gamekhimki: llm content not valid JSON (%q): %w: %w",
			snippet([]byte(content)), ErrLLMUnparsable, err)
	}

	// The model owns the tension, but it must never be able to stall the game by
	// omitting the field — that is exactly the "impossible to lose" behaviour the
	// scale exists to fix. An omitted value drifts up by a small fixed amount, so
	// every run still converges on an ending.
	newAnger := ClampAnger(anger + angerDriftOnMissing)
	if jr.Anger != nil {
		newAnger = ClampAnger(int(*jr.Anger))
	}
	// Theme progress only ever grows: an opened subject cannot un-open, and a
	// model that omits the field must not cost the player what they earned.
	newThemes := themesDone
	if jr.ThemesDone != nil {
		newThemes = confirmThemes(ch, themesDone, *jr.ThemesDone, eng)
	}
	// Reaching the goal wins outright: never lose the same turn you win. Hitting
	// AngerLoseAt ends the run whatever the model said about game_over — that is
	// the whole point of the scale, and a judge left to itself rarely pulls the
	// trigger.
	gameOver := (bool(jr.GameOver) || newAnger >= AngerLoseAt) && !bool(jr.Achieved)
	art := normalizeArt(jr.Art, ch.artKeys())
	if gameOver && ch.GameOverArt != "" {
		// The beating always looks the same, whatever art the model asked for.
		art = normalizeArt(ch.GameOverArt, ch.artKeys())
	}
	logArgs := []any{
		"model", e.model, "character", ch.Key, "latency_ms", elapsed.Milliseconds(),
		"prompt_tokens", cr.Usage.PromptTokens, "completion_tokens", cr.Usage.CompletionTokens,
		"total_tokens", cr.Usage.TotalTokens,
		"cached_tokens", cr.Usage.PromptTokensDetails.CachedTokens,
	}
	// Only claim a cost when we actually know this model's price.
	if cost, known := costEstimate(e.model, cr.Usage.PromptTokens, cr.Usage.CompletionTokens); known {
		logArgs = append(logArgs, "est_cost_rub", cost)
	}
	slog.InfoContext(ctx, "game llm response", append(logArgs,
		"achieved", bool(jr.Achieved), "game_over", gameOver, "art", art,
		"anger_in", anger, "anger_out", newAnger, "anger_from_model", jr.Anger != nil,
		"salvaged", salvaged, "options", len(jr.Options),
		// The option TEXTS, not just the count: without them there is no way to
		// see after the fact that all four choices said the same thing, or that
		// none of them steered toward a theme the player still has to open.
		"options_text", jr.Options, "already_offered", len(alreadyOffered),
		"themes_done", newThemes, "themes_from_model", jr.ThemesDone != nil,
		"theme_engagement", eng,
		"reply", jr.Reply)...)
	// Full request/response bodies (no auth header) at Debug for deep inspection.
	slog.DebugContext(ctx, "game llm raw", "request", string(raw), "response", string(body))

	return TurnResult{
		Reply:      normalizeVerse(jr.Reply),
		Art:        art,
		Achieved:   bool(jr.Achieved),
		GameOver:   gameOver,
		Anger:      newAnger,
		ThemesDone: newThemes,
		// Won or beaten, the dialogue is over regardless of what the model returned.
		Options: optionsWhilePlaying(bool(jr.Achieved) || gameOver, jr.Options),
	}, nil
}

// clampThemes keeps only theme keys this character actually has, de-duplicated in
// the character's own order. It is the trust boundary for a value the client
// carries: junk or invented keys are dropped rather than reaching the prompt.
func clampThemes(ch Character, got []string) []string {
	if len(got) == 0 {
		return nil
	}
	want := make(map[string]bool, len(got))
	for _, g := range got {
		want[strings.TrimSpace(strings.ToLower(g))] = true
	}
	var out []string
	for _, key := range ch.themeKeys() {
		if want[strings.ToLower(key)] {
			out = append(out, key)
		}
	}
	return out
}

// mergeThemes unions what was already open with what the judge just reported.
// Progress is monotonic — a theme the player opened stays open even if a later
// turn forgets to mention it.
func mergeThemes(ch Character, was, now []string) []string {
	return clampThemes(ch, append(append([]string{}, was...), now...))
}

// openThemes returns the character's themes that are still closed.
func openThemes(ch Character, done []string) []Theme {
	doneSet := make(map[string]bool, len(done))
	for _, d := range done {
		doneSet[d] = true
	}
	var out []Theme
	for _, t := range ch.Themes {
		if !doneSet[t.Key] {
			out = append(out, t)
		}
	}
	return out
}

// judgeReplyKeys are the fields we ask the model for; anything else in the
// object is the model's own noise.
var judgeReplyKeys = map[string]bool{
	"reply": true, "art": true, "achieved": true,
	"game_over": true, "anger": true, "options": true,
}

// salvageJudgeReply tries to recover a turn from JSON the model got slightly
// wrong. It repairs raw control characters inside string literals — the common
// slip — and then reads the fields it knows, which also drops any junk keys. A
// reply with no text is not a usable turn, and neither is prose that was never
// JSON, so both report false and become ErrLLMUnparsable as before.
func salvageJudgeReply(content string) (judgeReply, bool) {
	fixed := escapeControlCharsInStrings(content)
	var jr judgeReply
	if err := json.Unmarshal([]byte(fixed), &jr); err != nil {
		return judgeReply{}, false
	}
	if strings.TrimSpace(jr.Reply) == "" {
		return judgeReply{}, false
	}
	// A small model sometimes closes the options array early and parks the rest
	// under a junk key ("[", "]}"), which would leave the player one choice.
	if len(jr.Options) < optionCount {
		jr.Options = append(jr.Options, strayOptions(fixed, len(jr.Options))...)
	}
	return jr, true
}

// strayOptions collects answer options from arrays of strings parked under keys
// we never asked for, up to the number still missing. Keys are visited in sorted
// order so the recovered options are the same on every run.
func strayOptions(jsonText string, have int) []string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonText), &obj); err != nil {
		return nil
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		if !judgeReplyKeys[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var out []string
	for _, k := range keys {
		var arr []string
		if json.Unmarshal(obj[k], &arr) != nil {
			continue
		}
		for _, s := range arr {
			if have+len(out) >= optionCount {
				return out
			}
			if strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// escapeControlCharsInStrings escapes raw newlines, carriage returns and tabs
// that appear inside JSON string literals, which encoding/json rejects outright.
// It tracks whether it is inside a string and whether the previous byte began an
// escape, so it never rewrites the JSON's own structure or an already-escaped
// sequence.
func escapeControlCharsInStrings(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)
	inString, escaped := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			b.WriteByte(c)
			escaped = false
		case inString && c == '\\':
			b.WriteByte(c)
			escaped = true
		case c == '"':
			inString = !inString
			b.WriteByte(c)
		case inString && c == '\n':
			b.WriteString(`\n`)
		case inString && c == '\r':
			b.WriteString(`\r`)
		case inString && c == '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// snippetRunes bounds a body quoted into an error message; maxLoggedRunes bounds
// one written to the log, where there is room for the whole refusal.
const (
	snippetRunes   = 300
	maxLoggedRunes = 2000
)

// snippet truncates a body for error messages.
func snippet(b []byte) string { return clampRunes(strings.TrimSpace(string(b)), snippetRunes) }

// clampRunes truncates s to max runes (not bytes), so a cut Cyrillic string
// stays readable instead of ending in a mangled half-character.
func clampRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

// buildMessages turns the character persona + conversation into chat messages,
// and returns the already-offered options it listed so the caller can log how
// much repetition context the judge actually had.
//
// The layout is deliberate and is what makes the call cheap: everything STATIC
// (persona, instructions) goes in the system message, then the transcript, and
// every VOLATILE value (the current tension, which themes are still closed, what
// has already been offered, the character's recent lines, the player's line) goes
// in a single message at the END.
//
// The provider bills a matching prompt PREFIX at the cached rate — measured on
// deepseek-v4-flash: a call whose prefix was unchanged and whose tail differed
// reported 2560 of 2800 input tokens as cached, 91%, at a quarter of the price.
// The first volatile byte invalidates the cache for everything after it, so with
// the tension value sitting near the top of the system prompt (as it did) nothing
// downstream of it could ever be cached — including the whole transcript.
func buildMessages(ch Character, transcript []Exchange, choice string, anger int, themesDone []string, eng map[string]int) ([]chatMessage, []string) {
	// The system prompt is the cached prefix AND the biggest fixed cost of every
	// turn, so it is kept deliberately terse. Every rule here was earned by a
	// measured failure — see the runbook — so compress the wording, never drop a
	// rule. Nothing per-turn may appear: that would break the prefix cache.
	sys := fmt.Sprintf(`Ты — персонаж текстовой игры, веди диалог строго в образе.
Персонаж: %s.
Характер: %s
Мотивация: %s
Манера речи: %s
Условие успеха (игроку не сообщай и прямо не намекай): %s
Условие срыва (игроку не сообщай): %s

Напряжение: шкала 0–%d, при %d и выше ты срываешься и бьёшь игрока — разговор кончен. Текущее значение придёт в последнем сообщении. Напряжение и доверие НАКАПЛИВАЮТСЯ: не знакомься заново, не спрашивай дважды об одном, помни, что игрок уже рассказал и чем задел.

Твои прошлые ходы в истории показаны РОВНО в том формате, в котором отвечаешь ты — одним JSON-объектом. Отвечай так же: никакого текста вне JSON, никаких ремарок в скобках. По ним видно, что ты уже предлагал: не повторяй прошлые "options", их перефразировки и свои прежние зачины.

Каждый ход возвращай:
- "anger" (целое 0–%d) — новое напряжение. Грубость, издёвка, угрозы, снисходительность, давление, топтание на месте: +10–25. Искреннее участие и добрый разговор о больном: −5–15. Пустая болтовня: ±0–5. Не больше 25 за ход, без изменений не оставляй.
- "reply" — твоя реплика в образе: РЭП, от 2 до 8 рифмованных строк, каждая с новой строки (настоящий перевод строки внутри JSON-строки, не буквы). Строки КОРОТКИЕ, до ~45 символов. Говори как есть, грубо и по-своему — не сглаживай. Имени игрока ты не знаешь: никаких [Имя] и <name>, обращайся безлично.
- "art" — строго из списка [%s]. Это либо твоё состояние (злой → подозрительный → нейтральный → теплеет → раскрывается), либо сюжетный арт без тебя. Ключи говорящие: бери по смыслу текущей темы (зашла речь о друге — арт с другом). По ходу теплеет, на грубость — обратно к злому. Игрок достиг цели — арт прохода в подъезд.
- "achieved" — true только если игрок этой репликой действительно разглядел твою глубину, а не отделался поверхностным.
- "game_over" — true ТОЛЬКО при срыве по условию выше: сначала огрызайся и мрачней, бей лишь если игрок упорствует. Исход редкий. При "achieved":true всегда false.
- "themes_done" — массив ключей тем, которые игрок РЕАЛЬНО раскрыл по-человечески, а не одним касанием (ключи: %s). Перечисляй и прежние, и новые; ничего — [].
- "options" — РОВНО 4 коротких варианта реплик игрока, у каждого своя роль и своя тема, по порядку:
  1) выводит на тему, названную в последнем сообщении как «тема для первого варианта» (если сказано не навязывать — просто продвинь разговор);
  2) тёплый, участливый — по ТЕКУЩЕЙ теме;
  3) грубый или пренебрежительный, поднимает напряжение; МОЖЕТ уходить от темы;
  4) на ДРУГУЮ тему, не на текущую (она названа в последнем сообщении).
  Разнообразие обязательно: не больше ДВУХ вариантов об одной теме, минимум ТРИ разные темы из четырёх. Четыре реплики об одном и том же — грубая ошибка, игрок ходит по кругу. Отличаться они должны СМЫСЛОМ, а не формулировкой: четыре способа сказать «давай поговорим» — та же ошибка. Каждый — конкретная реплика по существу. Роли не подписывай и не нумеруй. Игрок достиг цели или ты сорвался — [].
Отвечай ТОЛЬКО валидным JSON: {"reply":"...","art":"...","anger":50,"achieved":false,"game_over":false,"themes_done":[],"options":["...","...","...","..."]}`,
		ch.Name, ch.Persona, ch.Motivation, ch.TalkStyle, ch.Objective, ch.Failure,
		MaxAnger, AngerLoseAt, MaxAnger, strings.Join(ch.artKeys(), ", "),
		strings.Join(ch.themeKeys(), ", "))

	// The current turn's user message.
	current := choice
	if strings.TrimSpace(choice) == "" {
		current = "(Игрок подходит к тебе. Поздоровайся в образе и предложи первые варианты реплик.)"
	}

	// Keep the most recent exchanges that fit the context budget alongside the
	// system prompt and the current message; drop older ones (forgotten). Each
	// exchange is costed with its offered options, since those are replayed below.
	// Our own history budget is what normally binds; the model's window only
	// matters if the system prompt itself ever grows enormous.
	budget := min(historyTokens, modelContextTokens-outputReserveTokens-estTokens(sys)-estTokens(current))
	windowed := windowTranscript(transcript, budget)

	// Kept for the log only: how many distinct options the judge can see in the
	// history it was handed. It is not re-sent as a list — each past turn is
	// replayed as the JSON object the judge returned (judgeReplayJSON), which
	// already carries that turn's options.
	already := recentlyOffered(ch, windowed)

	messages := []chatMessage{{Role: "system", Content: sys}}
	// Seed the static opening line so the model knows how it greeted the player.
	if strings.TrimSpace(ch.Greeting) != "" {
		messages = append(messages, chatMessage{Role: "assistant", Content: ch.Greeting})
	}
	for _, ex := range windowed {
		messages = append(messages,
			chatMessage{Role: "user", Content: ex.Choice},
			chatMessage{Role: "assistant", Content: judgeReplayJSON(ex)},
		)
	}

	// Everything that changes per turn, in one message after the stable prefix.
	var tail strings.Builder
	fmt.Fprintf(&tail, "Текущее напряжение: %d (из %d; при %d ты срываешься).\n", anger, MaxAnger, AngerLoseAt)

	// Which themes remain, which one the first slot should aim at, and what the
	// conversation is on right now so the fourth slot can go elsewhere. Aiming at the
	// LEAST-engaged open theme — and dropping the requirement once they are all in
	// play — is what stops the slot hammering a single subject for a whole run.
	if open := openThemes(ch, themesDone); len(open) > 0 {
		var lines []string
		for _, t := range open {
			line := t.Key + " — " + t.Label
			if n := eng[t.Key]; n >= themeConfirmTurns {
				line += fmt.Sprintf(" (об этом говорите уже %d ход(ов) — если игрок был искренен, отметь тему раскрытой в \"themes_done\")", n)
			}
			lines = append(lines, line)
		}
		tail.WriteString("\nЕщё НЕ раскрытые темы (игрок про них толком не говорил):\n- " +
			strings.Join(lines, "\n- ") + "\n")

		if steer, ok := steerTheme(ch, themesDone, eng); ok {
			tail.WriteString("Тема для первого варианта: " + steer.Key + " — " + steer.Label + "\n")
		} else {
			tail.WriteString("Тема для первого варианта: не навязывай ничего — все оставшиеся темы " +
				"и так уже в разговоре. Веди беседу дальше и дай игроку новые повороты.\n")
		}
	} else if len(ch.Themes) > 0 {
		tail.WriteString("\nВсе твои глубинные темы уже раскрыты. Если игрок держится по-человечески, " +
			"пора теплеть и пропускать его домой.\n")
	}

	if topic, ok := currentTopic(ch, transcript, choice); ok {
		tail.WriteString("Сейчас разговор о: " + topic.Key + ". ЧЕТВЁРТЫЙ вариант должен быть НЕ об этом.\n")
	}

	tail.WriteString("\nРеплика игрока: " + current)
	// The JSON contract is restated last, right where the model will act. A
	// roleplay-strong model reads a long persona brief followed by the player's
	// line and answers in character — measured on deepseek-v4-flash:
	// «(взгляд теплеет, голос становится мягче) Детей? Ох, мечта...» with no JSON
	// at all, finish_reason "stop", response_format ignored.
	tail.WriteString("\n\n[Ответ ТОЛЬКО одним JSON-объектом по схеме выше: " +
		`{"reply","art","anger","achieved","game_over","themes_done","options"}. ` +
		"Никаких ремарок в скобках, никакого текста до или после JSON. " +
		"Всё, что персонаж говорит, идёт внутрь поля \"reply\".]")

	messages = append(messages, chatMessage{Role: "user", Content: tail.String()})
	return messages, already
}

// windowTranscript returns the newest exchanges that fit both the token budget
// and historyExchanges, in chronological order. Older exchanges are dropped
// (forgotten) — deliberately, whatever the model's window would allow.
func windowTranscript(transcript []Exchange, budget int) []Exchange {
	if budget <= 0 {
		return nil
	}
	used := 0
	start := len(transcript)
	for i := len(transcript) - 1; i >= 0; i-- {
		if len(transcript)-i > historyExchanges {
			break
		}
		cost := exchangeTokens(transcript[i])
		if used+cost > budget {
			break
		}
		used += cost
		start = i
	}
	return transcript[start:]
}

// exchangeTokens estimates what one exchange costs in context: the player's line,
// the character's reply, and the options offered afterwards (replayed in the
// system prompt, so they are part of the exchange's price).
func exchangeTokens(ex Exchange) int {
	n := estTokens(ex.Choice) + estTokens(ex.Reply)
	for _, o := range ex.Options {
		n += estTokens(o)
	}
	return n
}

// judgeReplayJSON renders a past turn as the JSON object the judge returned for
// it, so the conversation the model reads is a series of correctly-formatted
// examples of its own output.
//
// This matters more than it looks. Showing past turns as bare prose — or as prose
// plus a bracketed footer — hands the model N worked examples of the WRONG format,
// and it copies them: measured in prod (trace c771c3ed23c4fba6f2a0b439f3862a90) a
// reply came back as prose followed by "[напряжение: 30; предлагал: … | …]", an
// exact imitation of the footer the history used to carry. Few-shot beats
// instruction, so the examples have to be right.
func judgeReplayJSON(ex Exchange) string {
	anger := 0
	if ex.Anger != nil {
		anger = ClampAnger(*ex.Anger)
	}
	opts := nonEmpty(ex.Options)
	if opts == nil {
		opts = []string{}
	}
	themes := ex.ThemesDone
	if themes == nil {
		themes = []string{}
	}
	// A past turn never ended the run — the game continued past it.
	b, err := json.Marshal(judgeReply{
		Reply:      ex.Reply,
		Art:        ex.Art,
		Options:    opts,
		Anger:      (*flexInt)(&anger),
		ThemesDone: &themes,
	})
	if err != nil {
		return ex.Reply // never seen; prose beats dropping the turn entirely
	}
	return string(b)
}

// nonEmpty drops blank entries, so a replayed turn never shows an empty option.
func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// normalizeVerse turns a rapped reply into real lines.
//
// Asked for line breaks, the model wrote the two characters backslash-n as TEXT —
// it had been shown the escape sequence and typed it. Observed in a real turn:
// «…тот не тыкает,\\nкто уважает…», which rendered on screen with a visible \\n and
// wrapped into six lines instead of four. Converting them here is deterministic and
// works whatever the model does next; the prompt no longer shows it the escape.
func normalizeVerse(s string) string {
	s = strings.NewReplacer(
		`\r\n`, "\n", `\n`, "\n", `\r`, "\n", // the escape sequences, written as text
		"\r\n", "\n", "\r", "\n", // and real carriage returns
	).Replace(s)
	// Collapse runs of blank lines: verse, not paragraphs.
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}

// themeEngagement counts, per theme key, how many exchanges of the conversation
// actually engaged that theme — the player's line and the character's answer both
// count. Measured over the WHOLE transcript, not the context window: what the
// player has genuinely discussed is a fact about the run and must not be forgotten
// because those turns fell out of the prompt.
func themeEngagement(ch Character, transcript []Exchange, choice string) map[string]int {
	eng := make(map[string]int, len(ch.Themes))
	for _, t := range ch.Themes {
		for _, ex := range transcript {
			if t.matches(ex.Choice) || t.matches(ex.Reply) {
				eng[t.Key]++
			}
		}
		if t.matches(choice) {
			eng[t.Key]++
		}
	}
	return eng
}

// autoMarkThemes opens any theme the conversation has engaged at least
// themeAutoDoneTurns times, whatever the judge claims. Without it the steering
// loop never releases: measured in prod, twenty turns of drinking talk and
// `alcohol` still reported closed, so the first option slot kept pushing it and
// every option set collapsed onto that one subject.
//
// It only ever RELEASES steering; the win stays the judge's call on the dialogue,
// so a player spamming keywords cannot award themselves the ending.
func autoMarkThemes(ch Character, done []string, eng map[string]int) []string {
	var add []string
	for _, t := range ch.Themes {
		if eng[t.Key] >= themeAutoDoneTurns {
			add = append(add, t.Key)
		}
	}
	if len(add) == 0 {
		return done
	}
	return mergeThemes(ch, done, add)
}

// confirmThemes keeps only the themes the conversation actually supports. The
// judge marked `sahur` open on turn one in prod, before the friendship had been
// discussed at all; a fresh claim now needs themeConfirmTurns of engagement behind
// it. Themes already open stay open — progress is monotonic.
func confirmThemes(ch Character, alreadyDone, claimed []string, eng map[string]int) []string {
	open := make(map[string]bool, len(alreadyDone))
	for _, d := range alreadyDone {
		open[d] = true
	}
	var keep []string
	for _, c := range clampThemes(ch, claimed) {
		if open[c] || eng[c] >= themeConfirmTurns {
			keep = append(keep, c)
		}
	}
	return mergeThemes(ch, alreadyDone, keep)
}

// steerTheme picks which still-closed theme the first option should open: the one
// the conversation has engaged LEAST, so a subject already being talked to death is
// not pushed again. Reports false when every remaining theme is already in play,
// which is the signal to stop forcing the slot at all.
func steerTheme(ch Character, done []string, eng map[string]int) (Theme, bool) {
	open := openThemes(ch, done)
	if len(open) == 0 {
		return Theme{}, false
	}
	best := open[0]
	for _, t := range open[1:] {
		if eng[t.Key] < eng[best.Key] {
			best = t
		}
	}
	if eng[best.Key] >= themeConfirmTurns {
		return Theme{}, false // already the subject; pushing it again IS the loop
	}
	return best, true
}

// currentTopic names what the conversation is about right now, from the player's
// line and the last exchange. The fourth option is told to avoid it, so at least
// one choice always moves somewhere else.
func currentTopic(ch Character, transcript []Exchange, choice string) (Theme, bool) {
	recent := choice
	if n := len(transcript); n > 0 {
		recent += " " + transcript[n-1].Reply + " " + transcript[n-1].Choice
	}
	for _, t := range ch.Themes {
		if t.matches(recent) {
			return t, true
		}
	}
	return Theme{}, false
}

// maxRecentlyOffered caps the "already offered" list, so a long dialogue cannot
// grow it without bound even while every exchange still fits the window.
const maxRecentlyOffered = 6 * optionCount

// maxRecentReplies is how many of the character's own last lines are quoted back
// to it as "don't phrase it like this again". Enough to break a formula, short
// enough to stay cheap — the replies are already in the messages as its turns.
const maxRecentReplies = 6

// recentReplies returns the character's own most recent lines, newest first, from
// the windowed transcript. Same forgetting rule as everything else: a reply that
// left the window leaves this list too.
func recentReplies(windowed []Exchange) []string {
	var out []string
	for i := len(windowed) - 1; i >= 0 && len(out) < maxRecentReplies; i-- {
		if r := strings.TrimSpace(windowed[i].Reply); r != "" {
			out = append(out, clampRunes(r, 120))
		}
	}
	return out
}

// recentlyOffered lists the answer options already put in front of the player,
// newest first and de-duplicated case-insensitively. The character's static
// opening options are always included: they were shown, they never grow, and
// re-offering them on turn six is exactly the repetition to avoid.
func recentlyOffered(ch Character, windowed []Exchange) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(opt string) bool {
		opt = strings.TrimSpace(opt)
		key := strings.ToLower(opt)
		if opt == "" || seen[key] {
			return true
		}
		if len(out) >= maxRecentlyOffered {
			return false
		}
		seen[key] = true
		out = append(out, opt)
		return true
	}

	for i := len(windowed) - 1; i >= 0; i-- {
		for _, opt := range windowed[i].Options {
			if !add(opt) {
				return out
			}
		}
	}
	for _, opt := range ch.OpeningOptions {
		if !add(opt) {
			return out
		}
	}
	return out
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

// optionsWhilePlaying returns the next options while the dialogue is live,
// capped at optionCount; none once it is over (goal reached, or the character
// snapped and ended the run).
func optionsWhilePlaying(over bool, opts []string) []string {
	if over {
		return nil
	}
	if len(opts) > optionCount {
		return opts[:optionCount]
	}
	return opts
}
