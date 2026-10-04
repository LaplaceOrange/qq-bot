package bot

import (
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
