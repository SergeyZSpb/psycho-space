package game

// Game + Character content, served to the SPA as config. Defined here in Go
// today (one source of truth, editable without touching the frontend); a later
// authoring UI / DB can replace this without changing the wire shape.
//
// The persona fields (Motivation/Persona/TalkStyle) are the AI judge's prompt
// material and are NOT sent to the client (json:"-"). Answer options are NOT
// authored here — the LLM generates them each turn (see llm.go / evaluator.go).
//
// Arts is the asset catalog: every visual the judge may show, each carrying its
// own render descriptor (placeholder emoji + gradient now, Image URL later). The
// judge returns an art KEY each turn; the frontend resolves it against this
// catalog, so adding/altering arts is a backend-only change — the client needs
// no update. Arts cover the character's changing mood (angry → warming → open,
// sometimes angrier) AND story/location arts with no character (a memory beat,
// the passage into the entrance).

// Game is a mini-game: a set of characters and which one is played by default.
type Game struct {
	GameKey          string      `json:"game_key"`
	Title            string      `json:"title"`
	Intro            string      `json:"intro"`
	DefaultCharacter string      `json:"default_character"`
	Characters       []Character `json:"characters"`
}

// Art is one showable asset with its render descriptor. Image (a URL) wins when
// set; otherwise the frontend renders Emoji over Gradient.
type Art struct {
	Key      string `json:"key"`
	Emoji    string `json:"emoji"`
	Gradient string `json:"gradient"`
	Image    string `json:"image,omitempty"`
}

// Character is one person to convince. Public fields are sent to the SPA;
// Motivation/Persona/TalkStyle stay server-side (the AI judge's persona prompt).
type Character struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Goal     string `json:"goal"`      // what the player must convince them of
	Greeting string `json:"greeting"`  // opening line shown before the first turn
	Arts     []Art  `json:"arts"`      // asset catalog the judge chooses from
	MaxSteps int    `json:"max_steps"` // dialogue-step budget before failure

	Motivation string `json:"-"` // AI persona: what drives them
	Persona    string `json:"-"` // AI persona: character
	TalkStyle  string `json:"-"` // AI persona: how they speak
}

// artKeys is the list of allowed art keys (for the judge prompt + clamping).
func (c Character) artKeys() []string {
	keys := make([]string, len(c.Arts))
	for i, a := range c.Arts {
		keys[i] = a.Key
	}
	return keys
}

// ContentFor returns the game config for a key, or ErrUnknownGame.
func ContentFor(key string) (Game, error) {
	if key == GameSmalltalkKhimki {
		return smalltalkKhimki(), nil
	}
	return Game{}, ErrUnknownGame
}

func (g Game) findCharacter(key string) (Character, bool) {
	for _, c := range g.Characters {
		if c.Key == key {
			return c, true
		}
	}
	return Character{}, false
}

// smalltalkKhimki is the default script. One character for now (the default).
// The dialogue is inherently multi-step: the LLM only marks success once the
// player has actually seen past the addict-idiot mask to дядя Ваня's depth
// (love of children, humanist values); it returns fewer options each turn and
// picks the art (his changing mood, or a story/location art) as the arc moves.
// The profile is replaceable config — richer prompts + real art land later.
func smalltalkKhimki() Game {
	dyadyaVanya := Character{
		Key:  "dyadya_vanya",
		Name: "Дядя Ваня",
		Goal: "Пройти мимо дяди Вани в свой подъезд: разговорить его, разглядеть за " +
			"маской поверхностного торчка глубокую личность (любовь к детям, гуманизм) — " +
			"тогда он пропустит тебя домой.",
		Greeting: "Куда прёшь?! Не пущу. Есть чё? И баба есть — потомство надо оставить...",
		Arts: []Art{
			{Key: "entrance_far_angry", Emoji: "🏢", Gradient: "linear-gradient(160deg, #2a2f3a, #14171d)"},
			{Key: "vanya_angry", Emoji: "😡", Gradient: "linear-gradient(160deg, #5a2f2f, #2a1717)"},
			{Key: "vanya_suspicious", Emoji: "🤨", Gradient: "linear-gradient(160deg, #4a3b2f, #241c16)"},
			{Key: "vanya_neutral", Emoji: "😐", Gradient: "linear-gradient(160deg, #3a3f4b, #20242c)"},
			{Key: "vanya_warming", Emoji: "🙂", Gradient: "linear-gradient(160deg, #2f4738, #1c2a22)"},
			{Key: "vanya_deep", Emoji: "🥹", Gradient: "linear-gradient(160deg, #2d5a53, #173b36)"},
			{Key: "memory_children", Emoji: "🧒", Gradient: "linear-gradient(160deg, #4a4368, #241f3a)"},
			{Key: "hallway_pass", Emoji: "🚪", Gradient: "linear-gradient(160deg, #2d5a53, #0f2b27)"},
		},
		MaxSteps: 10,
		Motivation: "Постоянно хочет ширнуться и найти женщину, чтобы оставить потомство. " +
			"Изначально не хочет никого пропускать. В глубине — тоскует по смыслу, любви и детях.",
		Persona: "Странный сосед у подъезда, на грани шизофрении. За маской поверхностного " +
			"торчка-нарика прячется глубокая, ранимая личность: любит детей, исповедует " +
			"гуманистические ценности.",
		TalkStyle: "Сбивчиво, резко скачет между бредом и внезапно глубокими мыслями; сперва " +
			"агрессивно и подозрительно, теплеет, когда в нём видят человека, а не идиота; " +
			"на грубость и снисходительность заводится обратно.",
	}
	return Game{
		GameKey: GameSmalltalkKhimki,
		Title:   "Смолтолк в Химках",
		Intro: "Ты почти дома, но у подъезда — странный сосед, дядя Ваня. Сначала он видится " +
			"поверхностным торчком, который никого не пускает. Разговори его, загляни глубже — " +
			"и, может, он откроется и пропустит тебя домой (а кот уже наблевал на шторы). " +
			"Реплик немного, и с каждой попыткой их меньше.",
		DefaultCharacter: dyadyaVanya.Key,
		Characters:       []Character{dyadyaVanya},
	}
}
