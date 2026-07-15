package config

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"полный конфиг ок", func(c *Config) {
			c.BotToken = "123:ABC"
			c.DashboardPass = "pw"
		}, false},
		{"нет токена", func(c *Config) { c.DashboardPass = "pw" }, true},
		{"нет пароля дашборда", func(c *Config) { c.BotToken = "123:ABC" }, true},
		{"неверный target", func(c *Config) {
			c.BotToken = "123:ABC"
			c.DashboardPass = "pw"
			c.Target = "bogus"
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Defaults()
			tc.mutate(c)
			err := c.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err=%v, ожидали wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestRenderEnvContainsSecretsAndToken(t *testing.T) {
	c := Defaults()
	c.BotToken = "999:SECRETTOKEN"
	env := c.RenderEnv()

	must := []string{
		"TELEGRAM_BOT_TOKEN=999:SECRETTOKEN",
		"OPENCLAW_GATEWAY_TOKEN=" + c.OpenClawGatewayToken,
		"INITIAL_PASSWORD=" + c.OmniroutePassword,
		"JWT_SECRET=" + c.OmnirouteJWTSecret,
		"OMNIROUTE_ENABLE_FREE_MODELS=true",
	}
	for _, m := range must {
		if !strings.Contains(env, m) {
			t.Errorf(".env не содержит %q\n---\n%s", m, env)
		}
	}
}

func TestRenderEnvVPSAddsCookieAndPublicURL(t *testing.T) {
	c := Defaults()
	c.BotToken = "x"
	c.Target = TargetVPS
	c.PublicHost = "203.0.113.10"
	env := c.RenderEnv()
	if !strings.Contains(env, "AUTH_COOKIE_SECURE=false") {
		t.Error("для VPS ожидали AUTH_COOKIE_SECURE")
	}
	if !strings.Contains(env, "NEXT_PUBLIC_BASE_URL=http://203.0.113.10:20128") {
		t.Errorf("для VPS ожидали публичный URL, получили:\n%s", env)
	}
	// Для localhost этих полей быть не должно.
	l := Defaults()
	l.BotToken = "x"
	if strings.Contains(l.RenderEnv(), "AUTH_COOKIE_SECURE") {
		t.Error("для localhost AUTH_COOKIE_SECURE не нужен")
	}
}

func TestDashboardBindAndURL(t *testing.T) {
	c := Defaults()
	c.DashboardPort = 8088
	if got := c.DashboardBind(); got != "127.0.0.1:8088" {
		t.Errorf("localhost bind = %q", got)
	}
	if got := c.DashboardURL(); got != "http://localhost:8088" {
		t.Errorf("localhost url = %q", got)
	}

	c.Target = TargetVPS
	if got := c.DashboardBind(); got != "0.0.0.0:8088" {
		t.Errorf("vps bind = %q", got)
	}
	c.PublicHost = "example.com"
	if got := c.DashboardURL(); got != "http://example.com:8088" {
		t.Errorf("vps url = %q", got)
	}
}

func TestRandSecretUniqueAndLength(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		s := RandSecret(32)
		if len(s) != 32 {
			t.Fatalf("длина секрета = %d, ожидали 32", len(s))
		}
		if seen[s] {
			t.Fatalf("повтор секрета: %q", s)
		}
		seen[s] = true
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	c := Defaults()
	c.InstallDir = t.TempDir()
	c.BotToken = "roundtrip:token"
	c.DashboardPass = "pw"
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(c.InstallDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.BotToken != c.BotToken || got.OmniroutePassword != c.OmniroutePassword {
		t.Errorf("после roundtrip данные не совпали: %+v", got)
	}
}
