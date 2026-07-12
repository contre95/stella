package config

// defaultConfig returns a fully-populated Config. Every option must have a
// default here so a fresh install is valid without hand-editing the file.
func defaultConfig() Config {
	return Config{
		DbPath:    "./data/stella.db",
		ImagePath: "./data/images",
		Timezone:  "", // host's local timezone
		Server: Server{
			Port:      8858,
			AppName:   "Stella",
			BodyLimit: 10 * 1024 * 1024,
		},
		Auth: Auth{
			Enabled: false,
		},
		MCP: MCP{
			Enabled: false,
		},
		Logger: Logger{
			Level: "info",
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
