package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseURL != "postgres://x" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.ShazamURL != "http://shazam:8000" {
		t.Errorf("ShazamURL = %q", cfg.ShazamURL)
	}
	if cfg.PollInterval != 5*time.Second {
		t.Errorf("PollInterval = %v", cfg.PollInterval)
	}
	if cfg.CommentSyncInterval != 10*time.Minute {
		t.Errorf("CommentSyncInterval = %v", cfg.CommentSyncInterval)
	}
	if cfg.MaxAttempts != 5 {
		t.Errorf("MaxAttempts = %d", cfg.MaxAttempts)
	}
	if cfg.BotHealthAddr != ":8081" {
		t.Errorf("BotHealthAddr = %q", cfg.BotHealthAddr)
	}
	if cfg.APIAddr != ":8080" {
		t.Errorf("APIAddr = %q", cfg.APIAddr)
	}
	if cfg.PublicBaseURL != "https://pr0sauce.info" {
		t.Errorf("PublicBaseURL = %q", cfg.PublicBaseURL)
	}
}

func TestLoad_BannedUsers(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "x", "BANNED_USERS": " banneduser, ,Foo "}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"banneduser", "Foo"}; !reflect.DeepEqual(cfg.BannedUsers, want) {
		t.Errorf("BannedUsers = %#v, want %#v", cfg.BannedUsers, want)
	}
}

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	_, err := Load(env(nil))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoad_InvalidValues(t *testing.T) {
	for _, k := range []string{"POLL_INTERVAL", "COMMENT_SYNC_INTERVAL", "MAX_ATTEMPTS"} {
		_, err := Load(env(map[string]string{"DATABASE_URL": "x", k: "bogus"}))
		if err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s: err = %v", k, err)
		}
	}
}

func TestLoad_Overrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"DATABASE_URL": "x", "POLL_INTERVAL": "2s", "COMMENT_SYNC_INTERVAL": "1m",
		"MAX_ATTEMPTS": "9", "SHAZAM_URL": "http://s", "BOT_HEALTH_ADDR": ":1", "API_ADDR": ":2",
		"PUBLIC_BASE_URL": "https://e",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollInterval != 2*time.Second || cfg.CommentSyncInterval != time.Minute || cfg.MaxAttempts != 9 ||
		cfg.ShazamURL != "http://s" || cfg.BotHealthAddr != ":1" || cfg.APIAddr != ":2" || cfg.PublicBaseURL != "https://e" {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestRequireBot_ListsAllMissing(t *testing.T) {
	cfg, _ := Load(env(map[string]string{"DATABASE_URL": "x"}))
	err := cfg.RequireBot()
	if err == nil || !strings.Contains(err.Error(), "PR0_USER") || !strings.Contains(err.Error(), "PR0_PASSWORD") {
		t.Fatalf("err = %v", err)
	}
	cfg, _ = Load(env(map[string]string{"DATABASE_URL": "x", "PR0_USER": "u", "PR0_PASSWORD": "p"}))
	if err := cfg.RequireBot(); err != nil {
		t.Fatal(err)
	}
}

func TestRequireLegacy(t *testing.T) {
	cfg, _ := Load(env(map[string]string{"DATABASE_URL": "x"}))
	if err := cfg.RequireLegacy(); err == nil || !strings.Contains(err.Error(), "LEGACY_DATABASE_URL") {
		t.Fatalf("err = %v", err)
	}
	cfg, _ = Load(env(map[string]string{"DATABASE_URL": "x", "LEGACY_DATABASE_URL": "y"}))
	if err := cfg.RequireLegacy(); err != nil {
		t.Fatal(err)
	}
}

func TestIsBanned_CaseInsensitive(t *testing.T) {
	cfg, _ := Load(env(map[string]string{"DATABASE_URL": "x", "BANNED_USERS": "banneduser"}))
	for name, want := range map[string]bool{"BANNEDUSER": true, "banneduser": true, "other": false} {
		if got := cfg.IsBanned(name); got != want {
			t.Errorf("IsBanned(%q) = %v, want %v", name, got, want)
		}
	}
	empty, _ := Load(env(map[string]string{"DATABASE_URL": "x"}))
	if empty.IsBanned("banneduser") {
		t.Error("empty list should ban nobody")
	}
}

func TestLoad_RejectsNonPositive(t *testing.T) {
	cases := map[string][]string{
		"POLL_INTERVAL":         {"0s", "-1s"},
		"COMMENT_SYNC_INTERVAL": {"0s", "-1s"},
		"MAX_ATTEMPTS":          {"0"},
	}
	for k, vals := range cases {
		for _, v := range vals {
			_, err := Load(env(map[string]string{"DATABASE_URL": "x", k: v}))
			if err == nil || !strings.Contains(err.Error(), k) {
				t.Errorf("%s=%s: err = %v", k, v, err)
			}
		}
	}
}

func TestLoad_Pr0BaseURL(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x"}
	cfg, err := Load(env(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Pr0BaseURL != "" {
		t.Errorf("default Pr0BaseURL = %q, want empty", cfg.Pr0BaseURL)
	}
	base["PR0_BASE_URL"] = "http://127.0.0.1:9"
	cfg, err = Load(env(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Pr0BaseURL != "http://127.0.0.1:9" {
		t.Errorf("Pr0BaseURL = %q", cfg.Pr0BaseURL)
	}
}

func TestRequireAPI_And_AccountVars(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "postgres://x", "ADMIN_PR0_NAME": "Helix", "BOT_HEALTH_URL": "http://bot:8081/healthz"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdminName != "Helix" || cfg.BotHealthURL != "http://bot:8081/healthz" {
		t.Errorf("cfg = %+v", cfg)
	}
	if err := cfg.RequireAPI(); err == nil || !strings.Contains(err.Error(), "INTERNAL_API_TOKEN") {
		t.Errorf("RequireAPI = %v", err)
	}
	cfg.InternalAPIToken = "x"
	if err := cfg.RequireAPI(); err == nil || !strings.Contains(err.Error(), "INTERNAL_API_TOKEN") {
		t.Errorf("short token RequireAPI = %v", err)
	}
	cfg.InternalAPIToken = strings.Repeat("a", 32)
	if err := cfg.RequireAPI(); err != nil {
		t.Errorf("RequireAPI = %v", err)
	}
}

func TestLoad_TrimsInternalToken(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "x", "INTERNAL_API_TOKEN": "  " + strings.Repeat("b", 31) + " \n"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InternalAPIToken != strings.Repeat("b", 31) || cfg.RequireAPI() == nil {
		t.Errorf("token = %q, RequireAPI = %v", cfg.InternalAPIToken, cfg.RequireAPI())
	}
}
