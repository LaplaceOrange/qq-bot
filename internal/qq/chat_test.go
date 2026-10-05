package qq

import (
	"encoding/json"
	"testing"
	"time"
)

func TestChatContent(t *testing.T) {
	for _, tc := range []struct {
		name, kind, content string
		mentions            []MessageAuthor
		bot, want           bool
		normalized          string
	}{
		{"private command", "C2C_MESSAGE_CREATE", " /llm <a>\nhelp ", nil, false, true, "/llm <a>\nhelp"},
		{"private text", "C2C_MESSAGE_CREATE", "hello", nil, false, false, "hello"},
		{"group text", "GROUP_MESSAGE_CREATE", "hello", nil, false, false, "hello"},
		{"other mention", "GROUP_MESSAGE_CREATE", "hello", []MessageAuthor{{ID: "other"}}, false, false, "hello"},
		{"other bot", "GROUP_MESSAGE_CREATE", "hello", []MessageAuthor{{ID: "other", Bot: true}}, false, false, "hello"},
		{"our mention", "GROUP_MESSAGE_CREATE", "hello", []MessageAuthor{{ID: "bot", Bot: true}}, false, true, "hello"},
		{"self marker with openid", "GROUP_MESSAGE_CREATE", "hello", []MessageAuthor{{MemberOpenID: "bot-member-openid", IsYou: true}}, false, true, "hello"},
		{"self marker without bot flag", "GROUP_MESSAGE_CREATE", "hello", []MessageAuthor{{ID: "not-appid", IsYou: true}}, false, true, "hello"},
		{"self marker strips openid prefix", "GROUP_MESSAGE_CREATE", "<@!bot-openid> hello", []MessageAuthor{{MemberOpenID: "bot-openid", IsYou: true}}, false, true, "hello"},
		{"other marker with bot flag", "GROUP_MESSAGE_CREATE", "hello", []MessageAuthor{{MemberOpenID: "other-bot-openid", Bot: true}}, false, false, "hello"},
		{"literal mention", "GROUP_MESSAGE_CREATE", "<@!bot> hello", nil, false, true, "hello"},
		{"legacy at", "GROUP_AT_MESSAGE_CREATE", "<@!bot> hello", nil, false, true, "hello"},
		{"mention command", "GROUP_MESSAGE_CREATE", "<@bot> /me", nil, false, true, "/me"},
		{"bot author", "GROUP_AT_MESSAGE_CREATE", "/llm recursive", nil, true, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := MessageEvent{EventType: tc.kind, Message: Message{Content: tc.content, Mentions: tc.mentions, Author: MessageAuthor{Bot: tc.bot}}}
			if tc.kind != "C2C_MESSAGE_CREATE" {
				e.Message.GroupOpenID = "g"
			}
			got, accepted := ChatContent(e, "bot")
			if got != tc.normalized || accepted != tc.want {
				t.Fatal(got, accepted)
			}
		})
	}
}

func TestChatContentDecodesQQSelfMention(t *testing.T) {
	var message Message
	if err := json.Unmarshal([]byte(`{
		"id":"message",
		"group_openid":"group",
		"author":{"member_openid":"user"},
		"content":"你好",
		"mentions":[{"scope":"single","member_openid":"bot-openid","is_you":true}]
	}`), &message); err != nil {
		t.Fatal(err)
	}
	event := MessageEvent{EventType: "GROUP_MESSAGE_CREATE", Message: message}
	content, accepted := ChatContent(event, "")
	if content != "你好" || !accepted || !message.Mentions[0].IsYou {
		t.Fatal(content, accepted, message.Mentions)
	}
	data, err := json.Marshal(event)
	var replay MessageEvent
	if err != nil || json.Unmarshal(data, &replay) != nil {
		t.Fatal("message cannot be persisted", err)
	}
	content, accepted = ChatContent(replay, "appid-not-openid")
	if content != "你好" || !accepted {
		t.Fatal("self marker lost during replay", content, accepted)
	}
}

func TestReplyOriginCannotExtendPassiveWindow(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	e := MessageEvent{ReceivedAt: now.Add(-time.Hour)}
	if origin := ReplyOrigin(e, now); !origin.Equal(e.ReceivedAt) {
		t.Fatal(origin)
	}
	e.ReceivedAt = now.Add(time.Hour)
	e.Message.Timestamp = json.RawMessage(`"2026-10-03T11:00:00Z"`)
	if origin := ReplyOrigin(e, now); !origin.Equal(now.Add(-time.Hour)) {
		t.Fatal(origin)
	}
	e.Message.Timestamp = json.RawMessage(`"2099-01-01T00:00:00Z"`)
	if origin := ReplyOrigin(e, now); !origin.Equal(now) {
		t.Fatal(origin)
	}
	e.Message.Timestamp = json.RawMessage(`"invalid"`)
	if origin := ReplyOrigin(e, now); !origin.Equal(now) {
		t.Fatal(origin)
	}
}
