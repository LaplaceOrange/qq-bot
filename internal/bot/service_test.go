package bot

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsykk/qq-bot/internal/config"
	"github.com/fsykk/qq-bot/internal/llm"
	"github.com/fsykk/qq-bot/internal/qq"
	"github.com/fsykk/qq-bot/internal/secure"
	"github.com/fsykk/qq-bot/internal/store"
)

type fakeQQ struct {
	mu              sync.Mutex
	messages        []string
	groupReplyErr   error
	groupReplyErrAt int
	groupReplies    int
	groupReplyHook  func(int)
}

func (f *fakeQQ) ReplyC2C(_ context.Context, _, _, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, content)
	return nil
}

func (f *fakeQQ) ReplyGroup(_ context.Context, _, _, content string) error {
	f.mu.Lock()
	f.groupReplies++
	call := f.groupReplies
	hook := f.groupReplyHook
	if f.groupReplyErr != nil && (f.groupReplyErrAt == 0 || f.groupReplyErrAt == call) {
		f.mu.Unlock()
		return f.groupReplyErr
	}
	f.messages = append(f.messages, content)
	f.mu.Unlock()
	if hook != nil {
		hook(call)
	}
	return nil
}

func testService(t *testing.T) (*Service, *store.Store, *fakeQQ) {
	t.Helper()
	storage, err := store.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secure.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeQQ{}
	service := New(config.Config{
		LLM: llm.DefaultConfig(), QQAppID: "app",
		QQAdminOpenIDs: map[string]struct{}{"user:admin": {}, "member:g:admin": {}},
		QQAPITimeout:   time.Second, RSSHTTPTimeout: time.Second,
		MessageDedupTTL: time.Hour,
	}, storage, box, api, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { service.Stop(); _ = storage.Close() })
	return service, storage, api
}

func c2cEvent(user, content string) qq.MessageEvent {
	return qq.MessageEvent{EventType: "C2C_MESSAGE_CREATE", Message: qq.Message{
		ID: "m-" + content, Content: content, Author: qq.MessageAuthor{UserOpenID: user},
	}}
}

func groupEvent(group, member, content string) qq.MessageEvent {
	return qq.MessageEvent{EventType: "GROUP_MESSAGE_CREATE", Message: qq.Message{
		ID: "m-" + content, Content: content, GroupOpenID: group, Author: qq.MessageAuthor{MemberOpenID: member},
	}}
}

func lastReply(t *testing.T, api *fakeQQ) string {
	t.Helper()
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.messages) == 0 {
		t.Fatal("expected a bot reply")
	}
	return api.messages[len(api.messages)-1]
}

func TestOnlySupportedRootCommands(t *testing.T) {
	s, storage, api := testService(t)
	for _, command := range []string{
		"/bind 1", "/unbind", "/chat hello", "/llm_config show",
		"/checkin", "/me", "/usage", "/logs", "/models", "/credit", "/plan",
		"/hongbao", "/reset", "/admin", "/welcome", "/join", "/mute", "/recall",
		"/bot status", "/vendor_status", "/vendor_config", "/vendor_subscribe",
		"/enable", "/disable", "/benefit", "/notify", "/link", "/confirm",
	} {
		event := groupEvent("g", "admin", command)
		if !s.HandleGateway(context.Background(), event) {
			t.Fatal(command)
		}
		s.process(context.Background(), event)
	}
	event := groupEvent("g", "admin", "plain text")
	s.HandleGateway(context.Background(), event)
	s.process(context.Background(), event)
	if len(api.messages) != 0 {
		t.Fatal("removed commands or plain text replied", api.messages)
	}
	pending, _ := storage.ListPendingGatewayEvents(512)
	if len(pending) != 0 {
		t.Fatal("removed commands admitted", pending)
	}
	s.process(context.Background(), groupEvent("g", "ordinary", "/help"))
	text := lastReply(t, api)
	for _, command := range []string{"/help", "/whoami", "/rss", "/llm"} {
		if !strings.Contains(text, command) {
			t.Fatal(text)
		}
	}
	for _, entry := range commandHelpEntries() {
		if !supportedCommand(entry.path) {
			t.Fatal("obsolete command in help", entry)
		}
	}
}

func TestWhoAmIShowsContextSpecificOpenIDs(t *testing.T) {
	s, _, api := testService(t)
	private := c2cEvent("user-openid-1", "/whoami")
	private.Message.Author.UnionOpenID = "union-openid-1"
	s.process(context.Background(), private)
	privateReply := lastReply(t, api)
	if !strings.Contains(privateReply, "用户 OpenID：user-openid-1") ||
		!strings.Contains(privateReply, "Union OpenID：union-openid-1") ||
		strings.Contains(privateReply, "群 OpenID：") {
		t.Fatal(privateReply)
	}

	group := groupEvent("group-openid-1", "member-openid-1", "/whoami")
	group.Message.Author.UserOpenID = "user-openid-2"
	group.Message.Author.UnionOpenID = "union-openid-2"
	s.process(context.Background(), group)
	groupReply := lastReply(t, api)
	for _, expected := range []string{
		"用户 OpenID：user-openid-2",
		"当前群成员 OpenID：member-openid-1",
		"群 OpenID：group-openid-1",
		"Union OpenID：union-openid-2",
	} {
		if !strings.Contains(groupReply, expected) {
			t.Fatal(groupReply)
		}
	}
}

func TestGatewayInboxEncryptedDedupAndDrain(t *testing.T) {
	s, storage, api := testService(t)
	event := groupEvent("g", "ordinary", "/help")
	if !s.HandleGateway(context.Background(), event) || !s.HandleGateway(context.Background(), event) {
		t.Fatal("admission")
	}
	pending, _ := storage.ListPendingGatewayEvents(10)
	if len(pending) != 1 || strings.Contains(string(pending[0].Payload), "/help") {
		t.Fatal(pending)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pending, _ = storage.ListPendingGatewayEvents(10)
		if len(pending) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pending) != 0 {
		t.Fatal("inbox not drained")
	}
	s.Stop()
	if len(api.messages) != 1 {
		t.Fatal("duplicate reply", api.messages)
	}
}
