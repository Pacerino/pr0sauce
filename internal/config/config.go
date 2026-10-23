// Package config loads process configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Config holds all settings for the bot, API and import commands.
type Config struct {
	DatabaseURL         string
	LegacyDatabaseURL   string
	Pr0User             string
	Pr0Password         string
	BannedUsers         []string
	ShazamURL           string
	PollInterval        time.Duration
	CommentSyncInterval time.Duration
	MaxAttempts         int
	BotHealthAddr       string
	APIAddr             string
	PublicBaseURL       string
	// Pr0BaseURL overrides the pr0gramm API root; empty means production.
	Pr0BaseURL string
	// InternalAPIToken guards /api/internal/*; required by the api command.
	InternalAPIToken string
	// AdminName is the pr0gramm name that becomes admin on first login.
	AdminName string
	// BotHealthURL is the bot's health endpoint, surfaced to admins.
	BotHealthURL string
}

// Load reads configuration via getenv (never os.Getenv directly).
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		DatabaseURL:       getenv("DATABASE_URL"),
		LegacyDatabaseURL: getenv("LEGACY_DATABASE_URL"),
		Pr0User:           getenv("PR0_USER"),
		Pr0Password:       getenv("PR0_PASSWORD"),
		ShazamURL:         def(getenv("SHAZAM_URL"), "http://shazam:8000"),
		BotHealthAddr:     def(getenv("BOT_HEALTH_ADDR"), ":8081"),
		APIAddr:           def(getenv("API_ADDR"), ":8080"),
		PublicBaseURL:     def(getenv("PUBLIC_BASE_URL"), "https://pr0sauce.info"),
		Pr0BaseURL:        getenv("PR0_BASE_URL"),
		InternalAPIToken:  getenv("INTERNAL_API_TOKEN"),
		AdminName:         strings.TrimSpace(getenv("ADMIN_PR0_NAME")),
		BotHealthURL:      getenv("BOT_HEALTH_URL"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("config: DATABASE_URL is required")
	}
	for _, p := range strings.Split(getenv("BANNED_USERS"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			cfg.BannedUsers = append(cfg.BannedUsers, p)
		}
	}
	var err error
	if cfg.PollInterval, err = duration(getenv, "POLL_INTERVAL", 5*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.CommentSyncInterval, err = duration(getenv, "COMMENT_SYNC_INTERVAL", 10*time.Minute); err != nil {
		return Config{}, err
	}
	cfg.MaxAttempts = 5
	if v := getenv("MAX_ATTEMPTS"); v != "" {
		if cfg.MaxAttempts, err = strconv.Atoi(v); err != nil {
			return Config{}, fmt.Errorf("config: invalid MAX_ATTEMPTS %q: %w", v, err)
		}
		if cfg.MaxAttempts <= 0 {
			return Config{}, fmt.Errorf("config: MAX_ATTEMPTS must be > 0, got %q", v)
		}
	}
	return cfg, nil
}

// IsBanned reports whether name is in BannedUsers, ignoring case.
func (c Config) IsBanned(name string) bool {
	for _, b := range c.BannedUsers {
		if strings.EqualFold(b, name) {
			return true
		}
	}
	return false
}

// RequireBot checks the variables the bot command needs.
func (c Config) RequireBot() error {
	return missing(map[string]string{"PR0_USER": c.Pr0User, "PR0_PASSWORD": c.Pr0Password},
		"PR0_USER", "PR0_PASSWORD")
}

// RequireAPI checks the variables the api command needs.
func (c Config) RequireAPI() error {
	return missing(map[string]string{"INTERNAL_API_TOKEN": c.InternalAPIToken}, "INTERNAL_API_TOKEN")
}

// RequireLegacy checks the variables import-legacy needs.
func (c Config) RequireLegacy() error {
	return missing(map[string]string{"LEGACY_DATABASE_URL": c.LegacyDatabaseURL}, "LEGACY_DATABASE_URL")
}

func missing(vals map[string]string, order ...string) error {
	var m []string
	for _, k := range order {
		if vals[k] == "" {
			m = append(m, k)
		}
	}
	if len(m) == 0 {
		return nil
	}
	return fmt.Errorf("config: missing required environment variables: %s", strings.Join(m, ", "))
}

func def(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func duration(getenv func(string) string, key string, d time.Duration) (time.Duration, error) {
	v := getenv(key)
	if v == "" {
		return d, nil
	}
	p, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config: invalid %s %q: %w", key, v, err)
	}
	if p <= 0 {
		return 0, fmt.Errorf("config: %s must be > 0, got %q", key, v)
	}
	return p, nil
}
