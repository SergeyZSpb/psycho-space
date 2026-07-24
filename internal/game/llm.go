package game

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

// rubPer1kTokens is an approximate YandexGPT 5 Lite price per 1000 tokens, used
// only to log a running cost estimate. Update from current Yandex Cloud pricing;
// token counts logged alongside it are exact (from the API's usage field).
const rubPer1kTokens = 0.20

// optionCount is how many answer options every playing turn offers.
const optionCount = 4

// angerDriftOnMissing is how far the tension moves when the model answers
// without an "anger" field. Small, but never zero: a stalled scale means an
// unloseable run.
const angerDriftOnMissing = 5

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
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"` // logged when a reply is unusable
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
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
		return fmt.Errorf("game: %q is not a number", s)
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
		return fmt.Errorf("game: %q is not a bool", s)
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
	messages, alreadyOffered := buildMessages(ch, transcript, choice, anger, themesDone)
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
	req.Header.Set("Authorization", "Bearer "+e.apiKey) // key stays in the header, never logged

	slog.InfoContext(ctx, "game llm request",
		"model", e.model, "character", ch.Key, "choice", choice, "anger_in", anger,
		"messages", len(messages), "transcript_len", len(transcript))

	start := time.Now()
	resp, err := e.http.Do(req)
	if err != nil {
		return TurnResult{}, fmt.Errorf("game: llm request: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return TurnResult{}, fmt.Errorf("game: llm http %d: %s", resp.StatusCode, snippet(body))
	}

	var cr chatResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return TurnResult{}, fmt.Errorf("game: decode llm response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return TurnResult{}, fmt.Errorf("game: llm returned no choices: %s", snippet(body))
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
		return TurnResult{}, fmt.Errorf("game: llm content not valid JSON (%q): %w: %w",
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
		newThemes = mergeThemes(ch, themesDone, *jr.ThemesDone)
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
	estCost := float64(cr.Usage.TotalTokens) / 1000.0 * rubPer1kTokens
	slog.InfoContext(ctx, "game llm response",
		"model", e.model, "character", ch.Key, "latency_ms", elapsed.Milliseconds(),
		"prompt_tokens", cr.Usage.PromptTokens, "completion_tokens", cr.Usage.CompletionTokens,
		"total_tokens", cr.Usage.TotalTokens, "est_cost_rub", estCost,
		"achieved", bool(jr.Achieved), "game_over", gameOver, "art", art,
		"anger_in", anger, "anger_out", newAnger, "anger_from_model", jr.Anger != nil,
		"salvaged", salvaged, "options", len(jr.Options),
		// The option TEXTS, not just the count: without them there is no way to
		// see after the fact that all four choices said the same thing, or that
		// none of them steered toward a theme the player still has to open.
		"options_text", jr.Options, "already_offered", len(alreadyOffered),
		"themes_done", newThemes, "themes_from_model", jr.ThemesDone != nil,
		"reply", jr.Reply)
	// Full request/response bodies (no auth header) at Debug for deep inspection.
	slog.DebugContext(ctx, "game llm raw", "request", string(raw), "response", string(body))

	return TurnResult{
		Reply:      strings.TrimSpace(jr.Reply),
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

// buildMessages turns the character persona + conversation into chat messages.
// anger is the tension going into this turn; the model is told to return the new
// value and that a full scale means it snaps. It also returns the already-offered
// options it listed, so the caller can log how much repetition context the judge
// actually had.
func buildMessages(ch Character, transcript []Exchange, choice string, anger int, themesDone []string) ([]chatMessage, []string) {
	sys := fmt.Sprintf(`Ты — персонаж текстовой игры, веди диалог строго в образе.
Персонаж: %s.
Характер: %s
Мотивация: %s
Манера речи: %s
Условие успеха (НЕ сообщай его игроку, не намекай прямо): %s
Условие срыва (НЕ сообщай его игроку): %s

Шкала напряжения: 0–%d, сейчас %d. При %d и выше ты срываешься, бьёшь игрока и разговор кончен.
Помни всё, о чём уже говорили выше: напряжение и доверие НАКАПЛИВАЮТСЯ по ходу разговора.
Не начинай знакомство заново, не задавай один и тот же вопрос дважды, не забывай, что игрок
уже рассказал или чем уже задел.

Игрок выбирает реплики и пытается достичь цели. Каждый ход:
- верни новое значение напряжения (поле "anger", целое 0–%d). Грубость, издёвка, угрозы, снисходительность, давление, повтор одного и того же — поднимают на 10–25. Искреннее участие, тепло, разговор о его больных темах по-доброму — опускают на 5–15. Пустая нейтральная болтовня — ±0–5, но если игрок топчется на месте несколько ходов, напряжение растёт. Меняй не больше чем на 25 за ход и никогда не оставляй значение без изменений просто так;
- ответь ОДНОЙ короткой репликой в образе (поле "reply"). Ты не знаешь, как игрока зовут: никогда не вставляй подстановки вроде [Имя], [имя игрока] или <name> — обращайся безлично («слышь», «сосед», «ты»);
- выбери подходящий арт строго из списка [%s] (поле "art"). Арт — это либо текущее состояние персонажа (злой → подозрительный → нейтральный → теплеет → раскрывается), либо сюжетный арт без персонажа. Ключи артов говорящие: подбирай арт по смыслу текущей темы и настроения (например, если речь зашла о его близком друге — покажи арт с этим другом). По ходу диалога арт меняется от злого к более тёплому (иногда обратно к злому — на грубость). Когда игрок достигает цели — выбери арт прохода в подъезд;
- реши, достиг ли игрок цели именно этой репликой (поле "achieved": true/false). Ставь true только когда игрок действительно разглядел глубину персонажа, а не отделался поверхностным;
- реши, не сорвался ли ты окончательно (поле "game_over": true/false). Ставь true ТОЛЬКО когда игрок довёл тебя до срыва по условию срыва выше — тогда ты бьёшь его, и разговор на этом окончен. Это редкий исход: сначала огрызайся и мрачней, бей только если игрок упорно продолжает. Если "achieved": true, то "game_over" всегда false;
- отметь, какие из твоих глубинных тем игрок к этому моменту РЕАЛЬНО раскрыл — по-человечески, а не одним касанием (поле "themes_done": массив ключей). Ключи тем: %s. Перечисляй и уже раскрытые ранее, и новые; если ничего не раскрыто — [];
- предложи РОВНО 4 коротких варианта реплик игрока (поле "options": массив ровно из 4 строк). У каждого своя РОЛЬ, строго в этом порядке:
  1) вариант, который выводит разговор на одну из ещё НЕ раскрытых тем (список ниже) — без этого игру пройти нельзя;
  2) тёплый, участливый вариант: попытка увидеть в тебе человека;
  3) грубый или пренебрежительный вариант — он поднимает напряжение (игрок должен иметь возможность и проиграть);
  4) нейтральный или неожиданный: сменить тему, спросить о чём-то своём.
  Варианты обязаны отличаться СМЫСЛОМ, а не формулировкой: четыре разных способа сказать «давай поговорим» или «тебе нужна помощь» — это ошибка, так игрок ходит по кругу. Каждый вариант — конкретная реплика по существу, а не общая фраза. Не повторяй и не перефразируй то, что уже предлагал (список ниже). Не нумеруй варианты в тексте и не подписывай их роли. Если игрок достиг цели или ты сорвался — "options": [].
Отвечай ТОЛЬКО валидным JSON вида {"reply":"...","art":"...","anger":50,"achieved":false,"game_over":false,"themes_done":[],"options":["...","...","...","..."]}. Без пояснений и текста вне JSON.`,
		ch.Name, ch.Persona, ch.Motivation, ch.TalkStyle, ch.Objective, ch.Failure,
		MaxAnger, anger, AngerLoseAt, MaxAnger, strings.Join(ch.artKeys(), ", "),
		strings.Join(ch.themeKeys(), ", "))

	// The current turn's user message.
	current := choice
	if strings.TrimSpace(choice) == "" {
		current = "(Игрок подходит к тебе. Поздоровайся в образе и предложи первые варианты реплик.)"
	}

	// Keep the most recent exchanges that fit the context budget alongside the
	// system prompt and the current message; drop older ones (forgotten). Each
	// exchange is costed with its offered options, since those are replayed below.
	budget := modelContextTokens - outputReserveTokens - estTokens(sys) - estTokens(current)
	windowed := windowTranscript(transcript, budget)

	// Show the judge what it has already offered, so it stops recycling the same
	// four lines. Only options from exchanges that survived the window are listed:
	// an option is forgotten exactly when its turn is.
	// Name the themes still closed, so the first option slot has somewhere to
	// steer. Measured against the real model without this, the alcohol theme
	// appeared in 1 of 40 offered options — the win was practically unreachable.
	if open := openThemes(ch, themesDone); len(open) > 0 {
		var lines []string
		for _, t := range open {
			lines = append(lines, t.Key+" — "+t.Label)
		}
		sys += "\n\nЕщё НЕ раскрытые темы (игрок про них толком не говорил). " +
			"Первый из четырёх вариантов должен вести к одной из них — выбирай ту, " +
			"что уместнее по текущему разговору:\n- " + strings.Join(lines, "\n- ")
	} else if len(ch.Themes) > 0 {
		sys += "\n\nВсе твои глубинные темы уже раскрыты. Если игрок держится по-человечески, " +
			"пора теплеть и пропускать его домой."
	}

	already := recentlyOffered(ch, windowed)
	if len(already) > 0 {
		sys += "\n\nЭти варианты ответа ты игроку УЖЕ предлагал (свежие — сверху). " +
			"Не предлагай их снова и не давай близкие перефразировки — придумывай новые, " +
			"по текущей теме разговора:\n- " + strings.Join(already, "\n- ")
	}

	// His own recent lines, so he stops answering with the same formula. Measured
	// without this: «Ты меня не знаешь…» four times and «С чего ты взял, что…»
	// four times in a single eight-turn run.
	if mine := recentReplies(windowed); len(mine) > 0 {
		sys += "\n\nТвои недавние реплики. Не повторяй их зачины и формулировки, говори иначе:\n- " +
			strings.Join(mine, "\n- ")
	}

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
	return messages, already
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
