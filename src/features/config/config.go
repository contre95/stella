package config

import (
	"fmt"
	"strings"
	"time"
)

// Config is the root application configuration, loaded from YAML.
type Config struct {
	DbPath    string `yaml:"dbPath" validate:"required"`
	ImagePath string `yaml:"imagePath"`
	// Timezone is the IANA zone (e.g. "Europe/Madrid") used for review-day
	// boundaries and sent to sync devices so their clocks show local time,
	// DST included. Empty means the host's local timezone.
	Timezone string `yaml:"timezone"`
	Server   Server `yaml:"server"`
	Auth     Auth   `yaml:"auth"`
	MCP      MCP    `yaml:"mcp"`
	Logger   Logger `yaml:"logger"`
	Review   Review `yaml:"review"`
	Cards    Cards  `yaml:"cards"`
}

// Location resolves Timezone, falling back to the host's local timezone when
// unset. Errors only on a non-empty zone name that fails to load.
func (c *Config) Location() (*time.Location, error) {
	if strings.TrimSpace(c.Timezone) == "" {
		return time.Local, nil
	}
	return time.LoadLocation(c.Timezone)
}

// Auth holds authentication settings.
type Auth struct {
	// Enabled activates multi-user OIDC login. When false (the default) the app
	// runs in single-user mode: no login required and all data belongs to one
	// built-in local account.
	Enabled bool `yaml:"enabled"`
	// PublicURL is the external base URL of this Stella instance. The OIDC
	// redirect URL registered at the provider must be <publicURL>/auth/callback.
	// It also doubles as Stella's own OAuth issuer for MCP clients, since
	// Stella acts as its own Authorization Server for the MCP endpoint.
	PublicURL string `yaml:"publicURL"`
	// OIDC configures the OpenID Connect provider used when Enabled is true.
	OIDC OIDC `yaml:"oidc"`
}

// OIDC holds the OpenID Connect provider settings for multi-user mode.
type OIDC struct {
	// Issuer is the provider's issuer URL (its discovery document must live at
	// <issuer>/.well-known/openid-configuration).
	Issuer       string `yaml:"issuer"`
	ClientID     string `yaml:"clientID"`
	ClientSecret string `yaml:"clientSecret"`
}

// Validate enforces the conditional requirement struct tags cannot express:
// multi-user mode needs a fully configured OIDC provider.
func (a *Auth) Validate() error {
	if !a.Enabled {
		return nil
	}
	var missing []string
	for key, val := range map[string]string{
		"oidc.issuer": a.OIDC.Issuer, "oidc.clientID": a.OIDC.ClientID,
		"oidc.clientSecret": a.OIDC.ClientSecret, "publicURL": a.PublicURL,
	} {
		if strings.TrimSpace(val) == "" {
			missing = append(missing, "auth."+key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("auth.enabled is true but %s not set", strings.Join(missing, ", "))
	}
	return nil
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
	Enabled bool `yaml:"enabled"`
}

// Logger holds logging settings.
type Logger struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"` // json, text, logfmt (default)
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
