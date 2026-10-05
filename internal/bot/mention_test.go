package bot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fsykk/qq-bot/internal/qq"
)

func TestMentionEntersLLMThroughDurableInbox(t *testing.T) {
	for _, kind := range []string{"literal", "mentions", "at-event"} {
		t.Run(kind, func(t *testing.T) {
			s, client, api := setupLLM(t)
			if err := s.store.SetLLMGroup("g", true, time.Now()); err != nil {
				t.Fatal(err)
			}
			prompt := "<code>\nexplain help"
			event := groupEvent("g", "ordinary", prompt)
			switch kind {
			case "literal":
				event.Message.Content = "<@!app> " + prompt
			case "mentions":
				event.Message.Mentions = []qq.MessageAuthor{{ID: "app", Bot: true}}
			case "at-event":
				event.EventType = "GROUP_AT_MESSAGE_CREATE"
			}
			for i := 0; i < 2; i++ {
				if !s.HandleGateway(context.Background(), event) {
					t.Fatal("gateway rejected mention")
				}
			}
			pending, err := s.store.ListPendingGatewayEvents(10)
			if err != nil || len(pending) != 1 {
				t.Fatal(pending, err)
			}
			if strings.Contains(string(pending[0].Payload), prompt) {
				t.Fatal("plaintext prompt in inbox")
			}
			plain, err := s.secure.Decrypt(string(pending[0].Payload))
			var replay qq.MessageEvent
			if err != nil || json.Unmarshal([]byte(plain), &replay) != nil {
				t.Fatal("invalid inbox payload", err)
			}
			if replay.Message.Content != event.Message.Content {
				t.Fatal("addressing evidence lost")
			}
			s.processQueuedGatewayEvent(context.Background(), queuedGatewayEvent{key: pending[0].Key, event: replay})
			jobs, err := s.store.PendingLLMJobs()
			if err != nil || len(jobs) != 1 {
				t.Fatal(jobs, err)
			}
			s.runLLMJob(context.Background(), jobs[0])
			if lastReply(t, api) != "answer" || len(client.messages) != 1 {
				t.Fatal(api.messages, client.messages)
			}
			messages := client.messages[0]
			if !strings.HasSuffix(messages[len(messages)-1].Content, prompt) {
				t.Fatal("prompt modified", messages)
			}
		})
	}
}

func TestMentionTextNeverExecutesLLMSubcommands(t *testing.T) {
	for _, prompt := range []string{"on", "off", "status", "reset", "help", "config set enabled false"} {
		t.Run(prompt, func(t *testing.T) {
			s, client, _ := setupLLM(t)
			if err := s.store.SetLLMGroup("g", true, time.Now()); err != nil {
				t.Fatal(err)
			}
			event := groupEvent("g", "admin", "<@app> "+prompt)
			runLLMNow(t, s, event)
			messages := client.messages[0]
			if !strings.HasSuffix(messages[len(messages)-1].Content, prompt) {
				t.Fatal(messages)
			}
			if enabled, _ := s.store.LLMGroupEnabled("g"); !enabled {
				t.Fatal("mention changed group settings")
			}
			cfg, err := s.llmConfigSnapshot()
			if err != nil || !cfg.Enabled {
				t.Fatal("mention changed global config", err)
			}
		})
	}
}

func TestMentionIgnoresUnaddressedAndBotMessages(t *testing.T) {
	for _, kind := range []string{"ordinary", "other-member", "other-bot", "bot-author", "private", "unknown-command"} {
		t.Run(kind, func(t *testing.T) {
			s, _, api := setupLLM(t)
			event := groupEvent("g", "ordinary", "hello")
			switch kind {
			case "other-member":
				event.Message.Content = "<@someone> hello"
			case "other-bot":
				event.Message.Mentions = []qq.MessageAuthor{{ID: "someone", Bot: true}}
			case "bot-author":
				event.EventType = "GROUP_AT_MESSAGE_CREATE"
				event.Message.Author.Bot = true
			case "private":
				event = c2cEvent("ordinary", "hello")
			case "unknown-command":
				event.Message.Content = "<@app> /bind 1"
			}
			if !s.HandleGateway(context.Background(), event) {
				t.Fatal("ignored event requested redelivery")
			}
			s.process(context.Background(), event)
			pending, _ := s.store.ListPendingGatewayEvents(10)
			jobs, _ := s.store.PendingLLMJobs()
			if len(pending) != 0 || len(jobs) != 0 || len(api.messages) != 0 {
				t.Fatal("ignored event caused work", pending, jobs, api.messages)
			}
		})
	}
}

func TestMentionRespectsGroupGlobalSwitchAndLimits(t *testing.T) {
	s, _, api := setupLLM(t)
	event := groupEvent("g", "ordinary", "<@app> hello")
	s.process(context.Background(), event)
	if !strings.Contains(lastReply(t, api), "/llm on") {
		t.Fatal(lastReply(t, api))
	}
	if err := s.store.SetLLMGroup("g", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.cfg.LLM.Enabled = false
	s.process(context.Background(), event)
	if !strings.Contains(lastReply(t, api), "尚未开启") {
		t.Fatal(lastReply(t, api))
	}
	s.cfg.LLM.Enabled = true
	s.cfg.LLM.MinuteLimit = 1
	runLLMNow(t, s, event)
	s.process(context.Background(), groupEvent("g", "ordinary", "<@app> second"))
	if !strings.Contains(lastReply(t, api), "限额") {
		t.Fatal(lastReply(t, api))
	}
	if jobs, _ := s.store.PendingLLMJobs(); len(jobs) != 0 {
		t.Fatal(jobs)
	}
}

func TestMentionWithoutTextAndMentionCommands(t *testing.T) {
	s, _, api := setupLLM(t)
	if err := s.store.SetLLMGroup("g", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.process(context.Background(), groupEvent("g", "ordinary", "<@app>"))
	if lastReply(t, api) != "我在，请告诉我你的问题。" {
		t.Fatal(lastReply(t, api))
	}
	s.process(context.Background(), groupEvent("g", "ordinary", "<@app> /help"))
	if !strings.Contains(lastReply(t, api), "/whoami") {
		t.Fatal(lastReply(t, api))
	}
	if jobs, _ := s.store.PendingLLMJobs(); len(jobs) != 0 {
		t.Fatal("empty mention or command consumed model quota", jobs)
	}
	s.process(context.Background(), groupEvent("g", "ordinary", "<@app> "+strings.Repeat("x", maxCommandBytes+1)))
	if !strings.Contains(lastReply(t, api), "4096") {
		t.Fatal(lastReply(t, api))
	}
}
