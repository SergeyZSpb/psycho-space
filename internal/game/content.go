package game

// Game + Character content, served to the SPA as config. Defined here in Go
// today (one source of truth, editable without touching the frontend); a later
// authoring UI / DB can replace this without changing the wire shape.
//
// What is NOT sent to the client (json:"-"): the character's persona/motivation/
// talk-style prompt (fed to the AI judge later), and per-option mock tuning
// (Effect / Reply). Answers must never be shipped, and the LLM will generate
// replies at runtime anyway.
//
// Assets are named by key only. The frontend resolves a background key and an
// emotion key (the AI picks the emotion each turn) to concrete art — static
// placeholders now, real images / backend-served URLs later.

// Game is a mini-game: a set of characters and which one is played by default.
type Game struct {
	GameKey          string      `json:"game_key"`
	Title            string      `json:"title"`
	Intro            string      `json:"intro"`
	DefaultCharacter string      `json:"default_character"`
	Characters       []Character `json:"characters"`
}

// Character is one person to convince. Public fields are sent to the SPA;
// Motivation/Persona/TalkStyle/Threshold stay server-side (persona prompt for
// the AI judge; mock tuning).
type Character struct {
	Key        string   `json:"key"`
	Name       string   `json:"name"`
	Goal       string   `json:"goal"`       // what the player must convince them of
	Background string   `json:"background"` // background-screen asset key
	Greeting   string   `json:"greeting"`   // opening line
	Emotions   []string `json:"emotions"`   // emotion asset keys the judge may choose from
	MaxSteps   int      `json:"max_steps"`  // dialogue-step budget before failure
	Options    []Option `json:"options"`    // answer options (the dialogue wheel)

	Motivation string `json:"-"` // AI persona: what drives them
	Persona    string `json:"-"` // AI persona: character
	TalkStyle  string `json:"-"` // AI persona: how they speak
	Threshold  int    `json:"-"` // mock: conviction needed to reach the goal
}

// Option is one answer the player can pick. Effect/Reply are mock-only and never
// serialised (they would leak the answer; the LLM generates replies itself).
type Option struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Effect int    `json:"-"` // mock: conviction delta
	Reply  string `json:"-"` // mock: canned character line
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

func (c Character) findOption(id string) (Option, bool) {
	for _, o := range c.Options {
		if o.ID == id {
			return o, true
		}
	}
	return Option{}, false
}

// smalltalkKhimki is the default script. It ships one character for now (the
// default), a single background, and the standard emotion set. The whole profile
// is replaceable config — a richer profile + prompts land later.
func smalltalkKhimki() Game {
	dyadyaVitya := Character{
		Key:        "dyadya_vitya",
		Name:       "Сосед дядя Витя",
		Goal:       "Убедить дядю Витю, что ты свой, и он пропустит тебя к подъезду.",
		Background: "entrance",
		Greeting:   "Ты кто такой? Я тебя тут не видел.",
		Emotions:   []string{"suspicious", "annoyed", "neutral", "warming", "pleased"},
		MaxSteps:   5,
		Motivation: "Охраняет свой двор, не любит чужаков и хамство, уважает своих.",
		Persona:    "Пожилой сосед-ворчун, подозрительный, но отходчивый к своим.",
		TalkStyle:  "Коротко, ворчливо, с прищуром; на грубость заводится, на уважение теплеет.",
		Threshold:  3,
		Options: []Option{
			{ID: "domofon", Label: "Напомнить, как в 2019 вместе чинили домофон", Effect: 2, Reply: "Хм… а точно, было дело."},
			{ID: "lusy", Label: "Сказать, что живёшь в 42-й, у Люси", Effect: 1, Reply: "У Люси, говоришь… ну допустим."},
			{ID: "zhkh", Label: "Поворчать вместе про тарифы ЖКХ", Effect: 1, Reply: "Во-о, тарифы совсем оборзели, да."},
			{ID: "hundred", Label: "Предложить сотку на водяру", Effect: -1, Reply: "Ты меня купить решил?!"},
			{ID: "diver", Label: "Обозвать водолазом", Effect: -2, Reply: "Совсем берега потерял?!"},
			{ID: "push", Label: "Молча попытаться протиснуться", Effect: -1, Reply: "Куда?! А ну стой."},
		},
	}
	return Game{
		GameKey: GameSmalltalkKhimki,
		Title:   "Смолтолк в Химках",
		Intro: "Ты почти дома, но у подъезда — сосед дядя Витя. Разговори его и убеди, " +
			"что ты свой. Выбирай реплики: нахамишь — не пройдёшь, найдёшь подход — ты дома " +
			"(а кот уже наблевал на шторы).",
		DefaultCharacter: dyadyaVitya.Key,
		Characters:       []Character{dyadyaVitya},
	}
}
