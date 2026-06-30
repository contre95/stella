package config

// Config is the root application configuration, loaded from YAML.
type Config struct {
	DataPath string   `yaml:"dataPath" validate:"required"`
	Server   Server   `yaml:"server"`
	Database Database `yaml:"database"`
	Logger   Logger   `yaml:"logger"`
	Review   Review   `yaml:"review"`
}

// Server holds HTTP server settings.
type Server struct {
	Port      int    `yaml:"port" validate:"required"`
	AppName   string `yaml:"appName"`
	BodyLimit int    `yaml:"bodyLimit"`
}

// Database holds storage settings.
type Database struct {
	Path string `yaml:"path" validate:"required"`
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
