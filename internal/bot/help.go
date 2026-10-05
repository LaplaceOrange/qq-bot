package bot

import (
	"context"
	"strings"

	"github.com/fsykk/qq-bot/internal/model"
	"github.com/fsykk/qq-bot/internal/qq"
)

type helpEntry struct {
	path, args, description string
	adminOnly, write        bool
}

func commandHelpEntries() []helpEntry {
	return []helpEntry{
		{"/help", "", "查看命令帮助", false, false},
		{"/whoami", "", "查看当前用户及群聊 OpenID", false, false},
		{"/rss list", "", "列出当前群订阅", false, false},
		{"/rss status", "", "查看检查结果和待发送数量", false, false},
		{"/rss add", "<URL>", "添加订阅，只推送后续新文章", true, true},
		{"/rss remove", "<编号>", "删除订阅", true, true},
		{"/rss pause", "<编号>", "暂停订阅", true, true},
		{"/rss resume", "<编号>", "恢复订阅，不补发暂停期间的文章", true, true},
		{"/rss interval", "<时长>", "设置本群检查间隔，1m 至 24h", true, true},
		{"/rss check", "<编号>", "检查订阅源，不改变推送进度", true, false},
		{"/llm", "<内容>", "与模型对话，群内也可 @机器人提问", false, false},
		{"/llm status", "", "查看配置状态和本人剩余次数", false, false},
		{"/llm reset", "", "清空当前历史，群聊需可写管理员", false, false},
		{"/llm on", "", "开启当前群对话", true, true},
		{"/llm off", "", "关闭当前群对话", true, true},
		{"/llm config show", "[key]", "查看配置，密钥不回显", true, false},
		{"/llm config set", "<key> <value>", "修改配置，密钥仅可在单聊设置", true, true},
		{"/llm config reset", "<key|all>", "恢复部署默认配置", true, true},
	}
}

func (s *Service) helpTextFor(identity model.QQIdentity, parent string) string {
	parent = strings.ToLower(strings.Join(strings.Fields(parent), " "))
	if parent == "" {
		return "可用命令：\n/help - 查看命令帮助\n/whoami - 查看当前 OpenID\n/rss - 管理群 RSS/Atom 订阅\n/llm - 与模型对话，群内也可 @机器人提问\n详细用法：/rss help、/llm help"
	}
	lines := []string{parent + " 帮助："}
	for _, entry := range commandHelpEntries() {
		if entry.adminOnly && !s.isAdmin(identity) || entry.write && s.isReadOnlyAdmin(identity) {
			continue
		}
		if entry.path != parent && !strings.HasPrefix(entry.path, parent+" ") {
			continue
		}
		usage := entry.path
		if entry.args != "" {
			usage += " " + entry.args
		}
		lines = append(lines, usage+" - "+entry.description)
	}
	if len(lines) == 1 {
		return "没有可用的命令帮助，请使用 /help。"
	}
	return strings.Join(lines, "\n")
}

func (s *Service) replyHelp(ctx context.Context, event qq.MessageEvent, text string) error {
	return s.replyChunked(ctx, event, text, 1500)
}
