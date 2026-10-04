package bot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fsykk/qq-bot/internal/llm"
	"github.com/fsykk/qq-bot/internal/qq"
	"github.com/fsykk/qq-bot/internal/store"
)

type scriptedLLM struct {
	responses []llm.Completion
	messages  [][]llm.Message
	tools     [][]llm.Tool
}

func (f *scriptedLLM) Complete(_ context.Context, _ llm.Config, messages []llm.Message, tools []llm.Tool) (llm.Completion, error) {
	f.messages = append(f.messages, append([]llm.Message(nil), messages...))
	f.tools = append(f.tools, append([]llm.Tool(nil), tools...))
	if len(f.responses) == 0 {
		return llm.Completion{Message: llm.Message{Role: "assistant", Content: "answer"}}, nil
	}
	response := f.responses[0]
	f.responses = f.responses[1:]
	return response, nil
}

type fakeSearch struct{ calls int }

func (f *fakeSearch) Search(context.Context, llm.Config, string) (llm.SearchResult, error) {
	f.calls++
	return llm.SearchResult{Sources: []llm.Source{{Title: "source", URL: "https://example.com/source"}}}, nil
}

func setupLLM(t *testing.T) (*Service, *scriptedLLM, *fakeQQ) {
	s, _, api := testService(t)
	s.cfg.LLM.Enabled, s.cfg.LLM.BaseURL, s.cfg.LLM.APIKey, s.cfg.LLM.Model = true, "https://example.test/v1", "test-secret", "test-model"
	client := &scriptedLLM{}
	s.llmCompleter = client
	return s, client, api
}

func runLLMNow(t *testing.T, s *Service, event qq.MessageEvent) store.LLMJob {
	t.Helper()
	s.process(context.Background(), event)
	jobs, err := s.store.PendingLLMJobs()
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	s.runLLMJob(context.Background(), jobs[0])
	job, err := s.store.LLMJob(jobs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestLLMNoBindingGroupSwitchAndHistory(t *testing.T) {
	s, client, api := setupLLM(t)
	s.process(context.Background(), groupEvent("g", "ordinary", "/llm hello"))
	if !strings.Contains(lastReply(t, api), "/llm on") {
		t.Fatal(lastReply(t, api))
	}
	s.process(context.Background(), groupEvent("g", "ordinary", "/llm on"))
	if enabled, _ := s.store.LLMGroupEnabled("g"); enabled {
		t.Fatal("non-admin enabled group")
	}
	s.process(context.Background(), groupEvent("g", "admin", "/llm on"))
	event := groupEvent("g", "ordinary", "/llm <code>\nplease explain help")
	job := runLLMNow(t, s, event)
	if job.Status != "sent" {
		t.Fatal(job.Status)
	}
	messages := client.messages[0]
	if !strings.HasSuffix(messages[len(messages)-1].Content, "<code>\nplease explain help") {
		t.Fatal(messages)
	}
	if len(client.tools[0]) != 0 {
		t.Fatal("obsolete tools exposed", client.tools)
	}
	runLLMNow(t, s, groupEvent("g", "other", "/llm follow up"))
	if len(client.messages[1]) != len(client.messages[0])+2 {
		t.Fatal("group history not shared", client.messages)
	}
	s.process(context.Background(), groupEvent("g", "ordinary", "/llm reset"))
	if !strings.Contains(lastReply(t, api), "管理员") {
		t.Fatal(lastReply(t, api))
	}
	s.process(context.Background(), groupEvent("g", "admin", "/llm reset"))
	runLLMNow(t, s, groupEvent("g", "ordinary", "/llm after reset"))
	if len(client.messages[2]) != len(client.messages[0]) {
		t.Fatal("reset retained history")
	}
	s.process(context.Background(), groupEvent("g", "admin", "/llm off"))
	s.process(context.Background(), groupEvent("g", "ordinary", "/llm blocked"))
	if jobs, _ := s.store.PendingLLMJobs(); len(jobs) != 0 {
		t.Fatal(jobs)
	}
}

func TestLLMPrivateHistoryAndRateIsolation(t *testing.T) {
	s, client, api := setupLLM(t)
	runLLMNow(t, s, c2cEvent("one", "/llm first"))
	runLLMNow(t, s, c2cEvent("two", "/llm separate"))
	if len(client.messages[1]) != len(client.messages[0]) {
		t.Fatal("private history leaked")
	}
	runLLMNow(t, s, c2cEvent("one", "/llm follow up"))
	if len(client.messages[2]) != len(client.messages[0])+2 {
		t.Fatal("private history lost")
	}
	s.cfg.LLM.MinuteLimit = 2
	s.process(context.Background(), c2cEvent("one", "/llm over limit"))
	if !strings.Contains(lastReply(t, api), "限额") {
		t.Fatal(lastReply(t, api))
	}
}

func TestLLMConfigUnderSingleRootPermissionsAndEncryption(t *testing.T) {
	s, _, api := setupLLM(t)
	s.cfg.QQReadOnlyAdminOpenIDs = map[string]struct{}{"user:readonly": {}}
	s.process(context.Background(), c2cEvent("ordinary", "/llm config set model denied"))
	if cfg, _ := s.llmConfigSnapshot(); cfg.Model != "test-model" {
		t.Fatal(cfg)
	}
	s.process(context.Background(), c2cEvent("readonly", "/llm config set model denied"))
	if cfg, _ := s.llmConfigSnapshot(); cfg.Model != "test-model" {
		t.Fatal(cfg)
	}
	s.process(context.Background(), groupEvent("g", "admin", "/llm config set api_key private-secret"))
	if cfg, _ := s.llmConfigSnapshot(); cfg.APIKey != "test-secret" {
		t.Fatal(cfg)
	}
	s.process(context.Background(), c2cEvent("admin", `/llm config set system_prompt "a <b> persona"`))
	if cfg, _ := s.llmConfigSnapshot(); cfg.SystemPrompt != "a <b> persona" {
		t.Fatal(cfg)
	}
	s.process(context.Background(), c2cEvent("admin", "/llm config set api_key private-secret"))
	s.process(context.Background(), c2cEvent("admin", "/llm config show api_key"))
	if strings.Contains(lastReply(t, api), "private-secret") {
		t.Fatal("secret echoed")
	}
	saved, _ := s.store.LLMConfig()
	if strings.Contains(saved, "private-secret") || strings.Contains(saved, "persona") {
		t.Fatal("plaintext config")
	}
	s.process(context.Background(), c2cEvent("admin", "/llm config reset all"))
	if cfg, _ := s.llmConfigSnapshot(); cfg.APIKey != "test-secret" || cfg.SystemPrompt != "" {
		t.Fatal(cfg)
	}
}

func TestLLMSearchOnlyAndRejectRemovedTools(t *testing.T) {
	s, client, api := setupLLM(t)
	s.cfg.LLM.SearchBackend = "tavily"
	search := &fakeSearch{}
	s.llmSearcher = search
	call := llm.ToolCall{ID: "search-1", Type: "function"}
	call.Function.Name, call.Function.Arguments = "web_search", `{"query":"news"}`
	client.responses = []llm.Completion{{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}}}}
	runLLMNow(t, s, c2cEvent("one", "/llm search"))
	if search.calls != 1 || !strings.Contains(lastReply(t, api), "https://example.com/source") {
		t.Fatal(search.calls, lastReply(t, api))
	}
	if len(client.tools[0]) != 1 || client.tools[0][0].Function.Name != "web_search" {
		t.Fatal(client.tools)
	}
	call.Function.Name, call.Function.Arguments = "account", `{}`
	client.responses = []llm.Completion{{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}}}}
	runLLMNow(t, s, c2cEvent("one", "/llm account tool"))
	if search.calls != 1 || !strings.Contains(lastReply(t, api), "未知工具") {
		t.Fatal(lastReply(t, api))
	}
}

func TestLLMResetAndGlobalDisableCancelQueuedJobs(t *testing.T) {
	for _, action := range []string{"reset", "disable"} {
		t.Run(action, func(t *testing.T) {
			s, client, _ := setupLLM(t)
			event := c2cEvent("one", "/llm hello")
			s.process(context.Background(), event)
			jobs, _ := s.store.PendingLLMJobs()
			if len(jobs) != 1 {
				t.Fatal(jobs)
			}
			if action == "reset" {
				s.process(context.Background(), c2cEvent("one", "/llm reset"))
			} else {
				s.process(context.Background(), c2cEvent("admin", "/llm config set enabled false"))
			}
			s.runLLMJob(context.Background(), jobs[0])
			if len(client.messages) != 0 {
				t.Fatal("canceled job called model")
			}
		})
	}
}

func TestLLMJobPayloadEncryptedAndQQMarkupEscaped(t *testing.T) {
	s, client, api := setupLLM(t)
	client.responses = []llm.Completion{{Message: llm.Message{Role: "assistant", Content: "test-secret <qqbot-at-everyone />"}}}
	event := c2cEvent("one", "/llm private prompt")
	s.process(context.Background(), event)
	jobs, _ := s.store.PendingLLMJobs()
	if len(jobs) != 1 || strings.Contains(jobs[0].Payload, "private prompt") {
		t.Fatal(jobs)
	}
	plain, err := s.secure.Decrypt(jobs[0].Payload)
	var payload llmJobPayload
	if err != nil || json.Unmarshal([]byte(plain), &payload) != nil || payload.Prompt != "private prompt" {
		t.Fatal(err)
	}
	s.runLLMJob(context.Background(), jobs[0])
	text := lastReply(t, api)
	if strings.Contains(text, "test-secret") || strings.Contains(text, "<qqbot-") {
		t.Fatal(text)
	}
	session, _ := s.store.LLMSession(jobs[0].Session, time.Now())
	if strings.Contains(session.Ciphertext, "private prompt") {
		t.Fatal("plaintext history")
	}
}
