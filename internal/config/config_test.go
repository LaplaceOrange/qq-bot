package config

import (
	"strings"
	"testing"
)

func TestMinimalConfigWithoutRemovedDependencies(t *testing.T) {
	t.Setenv("QQ_APP_ID", "app")
	t.Setenv("QQ_APP_SECRET", "secret")
	t.Setenv("QQ_ADMIN_OPENIDS", "member:g:admin")
	t.Setenv("BOT_DATA_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	for _, key := range []string{"NEWAPI_BASE_URL", "NEWAPI_ADMIN_TOKEN", "NEWAPI_ADMIN_USER_ID", "SMTP_HOST", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM"} {
		t.Setenv(key, "")
	}
	cfg, err := Load()
	if err != nil || cfg.QQAppID != "app" || len(cfg.BotDataKey) != 32 {
		t.Fatal(cfg, err)
	}
}

func TestInvalidKeyAndAdmins(t *testing.T) {
	t.Setenv("QQ_ADMIN_OPENIDS", "")
	t.Setenv("BOT_DATA_KEY", "invalid")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "BOT_DATA_KEY") || !strings.Contains(err.Error(), "QQ_ADMIN_OPENIDS") {
		t.Fatal(err)
	}
}
