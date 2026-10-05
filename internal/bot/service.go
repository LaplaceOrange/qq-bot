package bot

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsykk/qq-bot/internal/config"
	"github.com/fsykk/qq-bot/internal/llm"
	"github.com/fsykk/qq-bot/internal/qq"
	"github.com/fsykk/qq-bot/internal/rss"
	"github.com/fsykk/qq-bot/internal/secure"
	"github.com/fsykk/qq-bot/internal/store"
)

const (
	maxCommandBytes         = 4 << 10
	maxPendingGatewayEvents = 512
)

type QQAPI interface {
	ReplyC2C(context.Context, string, string, string) error
	ReplyGroup(context.Context, string, string, string) error
}

type c2cTextSequenceAPI interface {
	SendC2CTextWithSequence(context.Context, string, string, string, int) (qq.SentMessage, error)
}

type Service struct {
	cfg              config.Config
	store            *store.Store
	secure           *secure.Box
	qq               QQAPI
	logger           *slog.Logger
	queue            chan queuedGatewayEvent
	workers          sync.WaitGroup
	workersDone      chan struct{}
	notifyStop       chan struct{}
	dispatchStop     chan struct{}
	dispatchDone     chan struct{}
	inboxWake        chan struct{}
	started          atomic.Bool
	stopOnce         sync.Once
	queueCloseOnce   sync.Once
	lifecycleCtx     context.Context
	gatewayConnected func() bool
	inflightMu       sync.Mutex
	inflight         map[string]struct{}
	now              func() time.Time
	llmConfigMu      sync.Mutex
	llmMu            sync.Mutex
	llmActive        map[string]context.CancelFunc
	llmWake          chan struct{}
	llmClient        *llm.Client
	llmCompleter     llm.Completer
	llmSearcher      llm.Searcher
	rssClient        rssFeedClient
	rssGroups        keyedLocker[string]
	rssWake          chan struct{}
}

func New(cfg config.Config, storage *store.Store, box *secure.Box, qqAPI QQAPI, logger *slog.Logger) *Service {
	if cfg.GatewayQueueSize <= 0 {
		cfg.GatewayQueueSize = 64
	}
	if cfg.GatewayWorkers <= 0 {
		cfg.GatewayWorkers = 2
	}
	if cfg.QQAPITimeout <= 0 {
		cfg.QQAPITimeout = 10 * time.Second
	}
	if cfg.RSSPollInterval <= 0 {
		cfg.RSSPollInterval = 5 * time.Minute
	}
	if cfg.RSSHTTPTimeout <= 0 {
		cfg.RSSHTTPTimeout = 20 * time.Second
	}
	s := &Service{
		cfg: cfg, store: storage, secure: box, qq: qqAPI, logger: logger,
		queue:       make(chan queuedGatewayEvent, cfg.GatewayQueueSize),
		workersDone: make(chan struct{}), notifyStop: make(chan struct{}),
		dispatchStop: make(chan struct{}), dispatchDone: make(chan struct{}),
		inboxWake: make(chan struct{}, 1), rssWake: make(chan struct{}, 1),
		inflight: map[string]struct{}{}, lifecycleCtx: context.Background(), now: time.Now,
	}
	s.initLLM()
	client, err := rss.NewClient(cfg.RSSHTTPTimeout, cfg.RSSProxyURL)
	if err == nil {
		s.rssClient = client
	} else {
		logger.Error("RSS 客户端配置无效")
	}
	return s
}

func (s *Service) SetGatewayConnectedFunc(fn func() bool) { s.gatewayConnected = fn }

func (s *Service) Start(ctx context.Context) {
	if !s.started.CompareAndSwap(false, true) {
		return
	}
	s.lifecycleCtx = ctx
	s.workers.Add(1)
	go s.runLLMDispatcher(ctx)
	if s.cfg.RSSEnabled && s.rssClient != nil {
		s.workers.Add(1)
		go func() { defer s.workers.Done(); defer s.rssClient.Close(); s.runRSSWorker(ctx) }()
	}
	for i := 0; i < s.cfg.GatewayWorkers; i++ {
		s.workers.Add(1)
		go func() {
			defer s.workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case item, ok := <-s.queue:
					if !ok {
						return
					}
					s.processQueuedGatewayEvent(ctx, item)
				}
			}
		}()
	}
	go s.runInboxDispatcher(ctx)
	go func() { s.workers.Wait(); close(s.workersDone) }()
}

func (s *Service) Stop() { _ = s.StopContext(context.Background()) }

func (s *Service) StopContext(ctx context.Context) error {
	s.stopOnce.Do(func() { close(s.notifyStop); close(s.dispatchStop) })
	if !s.started.Load() {
		s.llmClient.Close()
		s.queueCloseOnce.Do(func() { close(s.queue) })
		if s.rssClient != nil {
			s.rssClient.Close()
		}
		return nil
	}
	select {
	case <-s.dispatchDone:
		s.queueCloseOnce.Do(func() { close(s.queue) })
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-s.workersDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func supportedCommand(content string) bool {
	fields := strings.Fields(content)
	if len(fields) == 0 {
		return false
	}
	switch strings.ToLower(fields[0]) {
	case "/help", "/whoami", "/rss", "/llm":
		return true
	default:
		return false
	}
}

func (s *Service) HandleGateway(ctx context.Context, event qq.MessageEvent) bool {
	if event.Message.ID == "" {
		return true
	}
	content, accepted := qq.ChatContent(event, s.cfg.QQAppID)
	if !accepted || strings.HasPrefix(content, "/") && !supportedCommand(content) {
		return true
	}
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = s.now()
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return false
	}
	encrypted, err := s.secure.Encrypt(string(payload))
	if err != nil {
		return false
	}
	key := event.EventType + "|" + event.Message.ID + "|" + sceneValue(event.Message.Scene.Ext, "msg_idx")
	pending, err := s.store.EnqueueGatewayEvent(key, []byte(encrypted), s.now(), s.cfg.MessageDedupTTL, maxPendingGatewayEvents)
	if err != nil {
		s.logger.Error("保存 QQ 命令失败", "error", err)
		return false
	}
	if pending && !s.tryDispatchGatewayEvent(queuedGatewayEvent{key: key, event: event}) {
		s.wakeInboxDispatcher()
	}
	return true
}

func (s *Service) process(parent context.Context, event qq.MessageEvent) {
	content, accepted := qq.ChatContent(event, s.cfg.QQAppID)
	if !accepted || strings.HasPrefix(content, "/") && !supportedCommand(content) {
		return
	}
	ctx, cancel := context.WithTimeout(parent, s.cfg.RSSHTTPTimeout+2*s.cfg.QQAPITimeout)
	defer cancel()
	if !strings.HasPrefix(content, "/") {
		if len(content) > maxCommandBytes {
			_ = s.reply(ctx, event, "对话内容不能超过 4096 字节。")
			return
		}
		if err := s.handleLLMPrompt(ctx, event, content); err != nil {
			s.logger.Error("处理 QQ @对话失败")
		}
		return
	}
	fields := strings.Fields(content)
	command := strings.ToLower(fields[0])
	limit := maxCommandBytes
	if command == "/llm" && len(fields) > 1 && strings.EqualFold(fields[1], "config") {
		limit = 20 << 10
	}
	if len(content) > limit {
		_ = s.reply(ctx, event, "指令内容过长，请缩短后重试。")
		return
	}
	identity := identityFromEvent(event)
	var err error
	switch command {
	case "/help":
		if len(fields) != 1 {
			err = s.reply(ctx, event, "正确用法：/help")
		} else {
			err = s.replyHelp(ctx, event, s.helpTextFor(identity, ""))
		}
	case "/whoami":
		if len(fields) != 1 {
			err = s.reply(ctx, event, "正确用法：/whoami")
		} else {
			err = s.handleWhoAmI(ctx, event, identity)
		}
	case "/rss":
		if len(fields) > 1 && strings.EqualFold(fields[len(fields)-1], "help") {
			err = s.replyHelp(ctx, event, s.helpTextFor(identity, strings.Join(fields[:len(fields)-1], " ")))
		} else {
			err = s.handleRSS(ctx, event, identity, fields)
		}
	case "/llm":
		if len(fields) > 1 && strings.EqualFold(fields[1], "config") {
			err = s.handleLLMConfig(ctx, event, "/llm "+commandRuleTail(commandRuleTail(content)))
		} else {
			err = s.handleChat(ctx, event, content)
		}
	}
	if err != nil {
		s.logger.Error("处理 QQ 命令失败", "command", command)
	}
}

func (s *Service) sendGroupReplyWithSequence(ctx context.Context, group, replyTo, content string, sequence int) error {
	if sender, ok := s.qq.(sequencedGroupMessageSender); ok {
		_, err := sender.SendGroupTextWithSequence(ctx, group, replyTo, content, sequence)
		return err
	}
	if sequence > 1 {
		replyTo = ""
	}
	if sender, ok := s.qq.(groupMessageSender); ok {
		_, err := sender.SendGroupText(ctx, group, replyTo, content)
		return err
	}
	return s.qq.ReplyGroup(ctx, group, replyTo, content)
}
