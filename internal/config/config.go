package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fsykk/qq-bot/internal/llm"
	"github.com/fsykk/qq-bot/internal/rss"
)

type Config struct {
	LLM                    llm.Config
	QQAppID                string
	QQAppSecret            string
	QQAdminOpenIDs         map[string]struct{}
	QQReadOnlyAdminOpenIDs map[string]struct{}
	BotDataKey             []byte
	DataPath               string
	ListenAddr             string
	LogLevel               slog.Level
	QQAPITimeout           time.Duration
	GatewayQueueSize       int
	GatewayWorkers         int
	MessageDedupTTL        time.Duration
	RSSEnabled             bool
	RSSPollInterval        time.Duration
	RSSHTTPTimeout         time.Duration
	RSSProxyURL            string
}

func Load() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, fmt.Errorf("读取 .env 失败: %w", err)
	}
	c := Config{
		QQAppID:                strings.TrimSpace(os.Getenv("QQ_APP_ID")),
		QQAppSecret:            strings.TrimSpace(os.Getenv("QQ_APP_SECRET")),
		DataPath:               envString("DATA_PATH", "./data/bot.db"),
		ListenAddr:             envString("LISTEN_ADDR", ":8080"),
		RSSProxyURL:            strings.TrimSpace(os.Getenv("RSS_PROXY_URL")),
		QQAdminOpenIDs:         openIDs("QQ_ADMIN_OPENIDS"),
		QQReadOnlyAdminOpenIDs: openIDs("QQ_READONLY_ADMIN_OPENIDS"),
	}
	var errs []error
	var err error
	c.LLM, err = llm.LoadEnvironment()
	if err != nil {
		errs = append(errs, fmt.Errorf("LLM 配置无效: %w", err))
	}
	for _, item := range [][2]string{{"QQ_APP_ID", c.QQAppID}, {"QQ_APP_SECRET", c.QQAppSecret}} {
		if item[1] == "" {
			errs = append(errs, fmt.Errorf("%s 为必填配置", item[0]))
		}
	}
	if len(c.QQAdminOpenIDs) == 0 {
		errs = append(errs, errors.New("QQ_ADMIN_OPENIDS 至少需要配置一个 OpenID 标识"))
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("BOT_DATA_KEY")))
	if err != nil || len(key) != 32 {
		errs = append(errs, errors.New("BOT_DATA_KEY 必须是 Base64 编码的 32 字节随机值"))
	} else {
		c.BotDataKey = key
	}
	duration := func(name string, def time.Duration) time.Duration {
		value, err := time.ParseDuration(envString(name, def.String()))
		if err != nil || value <= 0 {
			errs = append(errs, fmt.Errorf("%s 必须是正数 Go duration", name))
			return def
		}
		return value
	}
	integer := func(name string, def, max int) int {
		value, err := strconv.Atoi(envString(name, strconv.Itoa(def)))
		if err != nil || value < 1 || value > max {
			errs = append(errs, fmt.Errorf("%s 必须在 1 至 %d 之间", name, max))
			return def
		}
		return value
	}
	c.QQAPITimeout = duration("QQ_API_TIMEOUT", 10*time.Second)
	c.MessageDedupTTL = duration("MESSAGE_DEDUP_TTL", 24*time.Hour)
	c.GatewayQueueSize = integer("GATEWAY_QUEUE_SIZE", 64, 512)
	c.GatewayWorkers = integer("GATEWAY_WORKERS", 2, 16)
	c.RSSEnabled, err = strconv.ParseBool(envString("RSS_ENABLED", "true"))
	if err != nil {
		errs = append(errs, errors.New("RSS_ENABLED 必须是 true 或 false"))
	}
	c.RSSPollInterval = duration("RSS_POLL_INTERVAL", 5*time.Minute)
	c.RSSHTTPTimeout = duration("RSS_HTTP_TIMEOUT", 20*time.Second)
	if c.RSSPollInterval < time.Minute || c.RSSPollInterval > 24*time.Hour {
		errs = append(errs, errors.New("RSS_POLL_INTERVAL 必须在 1m 至 24h 之间"))
	}
	if c.RSSHTTPTimeout > 2*time.Minute {
		errs = append(errs, errors.New("RSS_HTTP_TIMEOUT 不能超过 2m"))
	}
	if err := rss.ValidateProxyURL(c.RSSProxyURL); err != nil {
		errs = append(errs, err)
	}
	c.LogLevel, err = parseLogLevel(envString("LOG_LEVEL", "info"))
	if err != nil {
		errs = append(errs, err)
	}
	return c, errors.Join(errs...)
}

func openIDs(name string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, value := range strings.Split(os.Getenv(name), ",") {
		if value = strings.TrimSpace(value); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}
