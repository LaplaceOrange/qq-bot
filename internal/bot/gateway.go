package bot

import (
	"context"

	"encoding/json"

	"strings"

	"time"

	"github.com/fsykk/qq-bot/internal/model"

	"github.com/fsykk/qq-bot/internal/qq"
)

type queuedGatewayEvent struct {
	key   string
	event qq.MessageEvent
}

func (s *Service) tryDispatchGatewayEvent(item queuedGatewayEvent) bool {
	s.inflightMu.Lock()
	if _, exists := s.inflight[item.key]; exists {
		s.inflightMu.Unlock()
		return true
	}
	s.inflight[item.key] = struct{}{}
	s.inflightMu.Unlock()
	select {
	case s.queue <- item:
		return true
	default:
		s.releaseGatewayEvent(item.key)
		return false
	}
}

func (s *Service) releaseGatewayEvent(key string) {
	s.inflightMu.Lock()
	delete(s.inflight, key)
	s.inflightMu.Unlock()
}

func (s *Service) wakeInboxDispatcher() {
	select {
	case s.inboxWake <- struct{}{}:
	default:
	}
}

func (s *Service) runInboxDispatcher(ctx context.Context) {
	defer close(s.dispatchDone)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		s.dispatchPendingGatewayEvents()
		select {
		case <-ctx.Done():
			return
		case <-s.dispatchStop:
			return
		case <-s.inboxWake:
		case <-ticker.C:
		}
	}
}

func (s *Service) dispatchPendingGatewayEvents() {
	limit := cap(s.queue) + s.cfg.GatewayWorkers + 1
	items, err := s.store.ListPendingGatewayEvents(limit)
	if err != nil {
		s.logger.Error("读取持久化命令收件箱失败", "error", err)
		return
	}
	for _, pending := range items {
		plaintext, err := s.secure.Decrypt(string(pending.Payload))
		if err != nil {
			s.logger.Error("解密持久化 QQ Gateway 事件失败，已移除损坏记录", "key", pending.Key, "error", err)
			_ = s.store.CompleteGatewayEvent(pending.Key)
			continue
		}
		var event qq.MessageEvent
		if err := json.Unmarshal([]byte(plaintext), &event); err != nil {
			s.logger.Error("解析持久化 QQ Gateway 事件失败，已移除损坏记录", "key", pending.Key, "error", err)
			_ = s.store.CompleteGatewayEvent(pending.Key)
			continue
		}
		if !s.tryDispatchGatewayEvent(queuedGatewayEvent{key: pending.Key, event: event}) {
			return
		}
	}
}

func (s *Service) processQueuedGatewayEvent(ctx context.Context, item queuedGatewayEvent) {
	if ctx.Err() != nil {
		s.releaseGatewayEvent(item.key)
		return
	}
	completed := false
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Error("处理 QQ Gateway 事件时发生 panic，事件保留至下次重启恢复", "key", item.key, "panic", recovered)
		}
		if completed {
			s.releaseGatewayEvent(item.key)
			s.wakeInboxDispatcher()
		}
	}()
	s.process(ctx, item.event)
	if err := s.store.CompleteGatewayEvent(item.key); err != nil {
		s.logger.Error("完成命令后移除持久化收件箱记录失败", "key", item.key, "error", err)
		return
	}
	completed = true
}

func identityFromEvent(event qq.MessageEvent) model.QQIdentity {
	return model.QQIdentity{
		UnionOpenID:  event.Message.Author.UnionOpenID,
		UserOpenID:   event.Message.Author.UserOpenID,
		MemberOpenID: event.Message.Author.MemberOpenID,
		GroupOpenID:  event.Message.GroupOpenID,
	}
}

func (s *Service) isAdmin(identity model.QQIdentity) bool {
	for _, candidate := range identity.AdminCandidates() {
		if _, ok := s.cfg.QQAdminOpenIDs[candidate]; ok {
			return true
		}
		if _, ok := s.cfg.QQReadOnlyAdminOpenIDs[candidate]; ok {
			return true
		}
	}
	return false
}

func (s *Service) isReadOnlyAdmin(identity model.QQIdentity) bool {
	for _, candidate := range identity.AdminCandidates() {
		if _, full := s.cfg.QQAdminOpenIDs[candidate]; full {
			return false
		}
	}
	for _, candidate := range identity.AdminCandidates() {
		if _, readOnly := s.cfg.QQReadOnlyAdminOpenIDs[candidate]; readOnly {
			return true
		}
	}
	return false
}

type groupMessageSender interface {
	SendGroupText(context.Context, string, string, string) (qq.SentMessage, error)
}

type sequencedGroupMessageSender interface {
	SendGroupTextWithSequence(context.Context, string, string, string, int) (qq.SentMessage, error)
}

func (s *Service) reply(ctx context.Context, event qq.MessageEvent, content string) error {
	if event.EventType == "C2C_MESSAGE_CREATE" {
		openID := event.Message.Author.UserOpenID
		if openID == "" {
			openID = event.Message.Author.ID
		}
		return s.qq.ReplyC2C(ctx, openID, event.Message.ID, content)
	}
	return s.sendGroupReply(ctx, event.Message.GroupOpenID, event.Message.ID, content)
}

func (s *Service) sendGroupReply(ctx context.Context, groupOpenID, replyTo, content string) error {
	return s.sendGroupReplyWithSequence(ctx, groupOpenID, replyTo, content, 1)
}

func (s *Service) replyChunked(ctx context.Context, event qq.MessageEvent, content string, maxRunes int) error {
	if maxRunes < 200 || len([]rune(content)) <= maxRunes {
		return s.reply(ctx, event, content)
	}
	chunks := splitMessage(content, maxRunes)
	for index, chunk := range chunks {
		if index == 0 {
			if err := s.reply(ctx, event, chunk); err != nil {
				return err
			}
			continue
		}
		if event.EventType == "C2C_MESSAGE_CREATE" {
			openID := firstNonEmpty(event.Message.Author.UserOpenID, event.Message.Author.ID)
			if err := s.qq.ReplyC2C(ctx, openID, "", chunk); err != nil {
				return err
			}
		} else if err := s.qq.ReplyGroup(ctx, event.Message.GroupOpenID, "", chunk); err != nil {
			return err
		}
	}
	return nil
}

func splitMessage(content string, maxRunes int) []string {
	lines := strings.Split(content, "\n")
	chunks := make([]string, 0, 2)
	current := make([]rune, 0, maxRunes)
	flush := func() {
		if len(current) == 0 {
			return
		}
		chunks = append(chunks, string(current))
		current = current[:0]
	}
	for _, line := range lines {
		runes := []rune(line)
		for len(runes) > 0 {
			separator := 0
			if len(current) > 0 {
				separator = 1
			}
			remaining := maxRunes - len(current) - separator
			if remaining <= 0 {
				flush()
				continue
			}
			if separator == 1 {
				current = append(current, '\n')
			}
			if len(runes) <= remaining {
				current = append(current, runes...)
				runes = nil
				continue
			}
			current = append(current, runes[:remaining]...)
			runes = runes[remaining:]
			flush()
		}
		if len(runes) == 0 && len(line) == 0 {
			if len(current) < maxRunes {
				current = append(current, '\n')
			} else {
				flush()
			}
		}
	}
	flush()
	return chunks
}

func sceneValue(ext []string, key string) string {
	prefix := key + "="
	for _, value := range ext {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return ""
}

func nonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "-"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
