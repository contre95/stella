package config

// defaultConfig returns a fully-populated Config. Every option must have a
// default here so a fresh install is valid without hand-editing the file.
func defaultConfig() Config {
	return Config{
		DbPath:    "./data/stella.db",
		ImagePath: "./data/images",
		Server: Server{
			Port:      8080,
			AppName:   "Stella",
			BodyLimit: 10 * 1024 * 1024,
		},
		MCP: MCP{
			Port: 8858,
		},
		Logger: Logger{
			Level: "info",
			Debug: false,
		},
		Review: Review{
			DayStartsAt:              3,
			NonMemorisedCardsInHand:  10,
			MaxScheduledIntervalDays: 0,
			RandomiseScheduledCards:  false,
			RandomiseNewCards:        false,
		},
		Cards: Cards{
			AutoplayAudio: true,
		},
	}
}
