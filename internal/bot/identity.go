package bot

import (
	"context"
	"strings"

	"unicode"

	"github.com/fsykk/qq-bot/internal/model"
	"github.com/fsykk/qq-bot/internal/qq"
)

func commandRuleTail(content string) string {
	content = strings.TrimSpace(content)
	if index := strings.IndexFunc(content, unicode.IsSpace); index >= 0 {
		return strings.TrimSpace(content[index:])
	}
	return ""
}

func commandRuleActor(identity model.QQIdentity) string {
	if candidates := identity.AdminCandidates(); len(candidates) > 0 {
		return candidates[0]
	}
	return "unknown"
}

func (s *Service) handleWhoAmI(ctx context.Context, event qq.MessageEvent, identity model.QQIdentity) error {
	userOpenID := firstNonEmpty(identity.UserOpenID, event.Message.Author.ID)
	if event.EventType == "C2C_MESSAGE_CREATE" {
		if userOpenID == "" {
			return s.reply(ctx, event, "当前消息未包含用户 OpenID。")
		}
		lines := []string{"用户 OpenID：" + userOpenID}
		if identity.UnionOpenID != "" {
			lines = append(lines, "Union OpenID："+identity.UnionOpenID)
		}
		return s.reply(ctx, event, strings.Join(lines, "\n"))
	}
	groupOpenID := firstNonEmpty(identity.GroupOpenID, event.Member.GroupOpenID)
	memberOpenID := firstNonEmpty(identity.MemberOpenID, event.Member.MemberOpenID, event.Message.Author.ID)
	if groupOpenID == "" && memberOpenID == "" && userOpenID == "" {
		return s.reply(ctx, event, "当前消息未包含用户或群聊 OpenID。")
	}
	lines := []string{}
	if userOpenID != "" {
		lines = append(lines, "用户 OpenID："+userOpenID)
	}
	if memberOpenID != "" {
		lines = append(lines, "当前群成员 OpenID："+memberOpenID)
	}
	if groupOpenID != "" {
		lines = append(lines, "群 OpenID："+groupOpenID)
	}
	if identity.UnionOpenID != "" {
		lines = append(lines, "Union OpenID："+identity.UnionOpenID)
	}
	return s.reply(ctx, event, strings.Join(lines, "\n"))
}

func llmIdentity(event qq.MessageEvent) string {
	identity := identityFromEvent(event)
	if identity.GroupOpenID != "" {
		if alias := identity.GroupAlias(); alias != "" {
			return alias
		}
		if canonical := identity.Canonical(); canonical != "" {
			return canonical
		}
		if event.Message.Author.ID != "" {
			return "member:" + identity.GroupOpenID + ":" + event.Message.Author.ID
		}
		return ""
	}
	if canonical := identity.Canonical(); canonical != "" {
		return canonical
	}
	if event.Message.Author.ID != "" {
		return "user:" + event.Message.Author.ID
	}
	return ""
}
