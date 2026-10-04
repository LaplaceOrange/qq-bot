package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/fsykk/qq-bot/internal/bot"
	"github.com/fsykk/qq-bot/internal/config"
	"github.com/fsykk/qq-bot/internal/health"
	"github.com/fsykk/qq-bot/internal/qq"
	"github.com/fsykk/qq-bot/internal/secure"
	"github.com/fsykk/qq-bot/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("机器人停止", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)
	storage, err := store.Open(cfg.DataPath)
	if err != nil {
		return err
	}
	defer storage.Close()
	box, err := secure.New(cfg.BotDataKey)
	if err != nil {
		return err
	}
	client := qq.NewClient(cfg.QQAppID, cfg.QQAppSecret, cfg.QQAPITimeout)
	gateway := qq.NewGateway(client, storage, logger)
	service := bot.New(cfg, storage, box, client, logger)
	service.SetGatewayConnectedFunc(gateway.Connected)
	appCtx, stopApp := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopApp()
	serviceCtx, cancelService := context.WithCancel(context.Background())
	defer cancelService()
	service.Start(serviceCtx)
	gatewayDone := make(chan struct{})
	go func() {
		defer close(gatewayDone)
		if err := gateway.Run(appCtx, service.HandleGateway); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("QQ Gateway 已停止", "error", err)
			stopApp()
		}
	}()
	server := &http.Server{
		Addr: cfg.ListenAddr, Handler: (&health.Server{Store: storage, QQ: client, Gateway: gateway}).Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
	}
	httpDone := make(chan struct{})
	go func() {
		defer close(httpDone)
		logger.Info("健康检查服务已启动", "listen", cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("健康检查服务异常退出", "error", err)
			stopApp()
		}
	}()
	<-appCtx.Done()
	<-gatewayDone
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err = service.StopContext(ctx)
	cancel()
	if err != nil {
		cancelService()
		// Wait for all database users before closing the store.
		service.Stop()
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		_ = server.Close()
	}
	<-httpDone
	logger.Info("服务已停止")
	return err
}
