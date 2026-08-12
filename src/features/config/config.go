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
	// PublicURLs are the external base URL(s) this Stella instance is
	// reachable at (e.g. a LAN address and a public domain). Each entry needs
	// its own <publicURL>/auth/callback registered as a redirect URI at the
	// OIDC provider — and, in CIMD mode, its own
	// <publicURL>/auth/client-metadata.json allowlisted, since CIMD's
	// client_id is the metadata document's URL, making each entry a distinct
	// client identity to the provider. Incoming requests are matched against
	// this list by Host header, so the login flow and the OAuth AS surface for
	// MCP clients (issuer, discovery, etc.) both mirror whichever domain the
	// request actually came in on; an unrecognized Host falls back to the
	// first entry.
	PublicURLs []string `yaml:"publicURLs"`
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
	// CIMD registers Stella as an OIDC client via a self-hosted Client ID
	// Metadata Document (<publicURL>/auth/client-metadata.json) instead of a
	// manually created clientID/clientSecret pair. Only supported by
	// providers implementing the CIMD spec (e.g. Pocket ID); mutually
	// exclusive with clientID/clientSecret.
	CIMD bool `yaml:"cimd"`
}

// Validate enforces the conditional requirement struct tags cannot express:
// multi-user mode needs a fully configured OIDC provider, via either a
// manually registered client or CIMD, not both.
func (a *Auth) Validate() error {
	if !a.Enabled {
		return nil
	}
	if a.OIDC.CIMD && (a.OIDC.ClientID != "" || a.OIDC.ClientSecret != "") {
		return fmt.Errorf("auth.oidc.cimd is true but clientID/clientSecret are also set — use one or the other")
	}
	required := map[string]string{"oidc.issuer": a.OIDC.Issuer}
	if !a.OIDC.CIMD {
		required["oidc.clientID"] = a.OIDC.ClientID
		required["oidc.clientSecret"] = a.OIDC.ClientSecret
	}
	var missing []string
	for key, val := range required {
		if strings.TrimSpace(val) == "" {
			missing = append(missing, "auth."+key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("auth.enabled is true but %s not set", strings.Join(missing, ", "))
	}
	if len(a.PublicURLs) == 0 {
		return fmt.Errorf("auth.enabled is true but auth.publicURLs not set")
	}
	for _, u := range a.PublicURLs {
		if strings.TrimSpace(u) == "" {
			return fmt.Errorf("auth.publicURLs contains an empty entry")
		}
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
	// RefreshTokenDays is how long an OAuth refresh token issued to an MCP
	// client (multi-user mode only) stays valid before the client must send
	// the user through the consent screen again. 0 or unset falls back to a
	// 30-day default.
	RefreshTokenDays int `yaml:"refreshTokenDays"`
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
