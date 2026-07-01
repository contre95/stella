package config

// Config is the root application configuration, loaded from YAML.
type Config struct {
	DbPath     string `yaml:"dbPath" validate:"required"`
	ImagePath  string `yaml:"imagePath"`
	Server     Server `yaml:"server"`
	MCP        MCP    `yaml:"mcp"`
	Logger     Logger `yaml:"logger"`
	Review     Review `yaml:"review"`
	Cards      Cards  `yaml:"cards"`
}

// Cards holds card display settings.
type Cards struct {
	// AutoplayAudio automatically plays audio when a card is shown.
	AutoplayAudio bool `yaml:"autoplayAudio"`
}

// Server holds HTTP server settings.
type Server struct {
	Port      int    `yaml:"port" validate:"required"`
	AppName   string `yaml:"appName"`
	BodyLimit int    `yaml:"bodyLimit"`
}

// MCP holds the Model Context Protocol server settings.
type MCP struct {
	Port int `yaml:"port" validate:"required"`
}

// Logger holds logging settings.
type Logger struct {
	Level string `yaml:"level"`
	Debug bool   `yaml:"debug"`
}

// Review holds scheduler tunables ported from Mnemosyne's config keys.
type Review struct {
	// DayStartsAt is the hour (0-23) at which a new study day begins.
	DayStartsAt int `yaml:"dayStartsAt"`
	// NonMemorisedCardsInHand limits how many not-yet-retained cards are juggled at once.
	NonMemorisedCardsInHand int `yaml:"nonMemorisedCardsInHand"`
	// MaxScheduledIntervalDays caps the interval; 0 means no cap.
	MaxScheduledIntervalDays int `yaml:"maxScheduledIntervalDays"`
	// RandomiseScheduledCards shuffles due cards instead of ordering by interval.
	RandomiseScheduledCards bool `yaml:"randomiseScheduledCards"`
	// RandomiseNewCards shuffles the order in which unseen cards are introduced.
	RandomiseNewCards bool `yaml:"randomiseNewCards"`
}
