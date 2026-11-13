package handler

import (
	"strings"
	"testing"

	"raven/internal/socketmap/cache"
	"raven/internal/socketmap/config"
)

func TestUserExistsDefersWhenThunderIsUnreachable(t *testing.T) {
	cfg := &config.Config{ThunderHost: "127.0.0.1", ThunderPort: "1", TokenRefreshSeconds: 300, CacheTTLSeconds: 60}

	if got := HandleUserExistsMap("alice@one.lk", cfg, cache.New(cfg.CacheTTLSeconds)); !strings.HasPrefix(got, "TEMP ") {
		t.Errorf("HandleUserExistsMap() with Thunder unreachable = %q, want TEMP so Postfix retries", got)
	}
}
