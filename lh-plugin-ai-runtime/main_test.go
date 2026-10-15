package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"lh-plugin-base/protocol"
)

type capabilityInvokeParams = protocol.CapabilityInvokeParams
type requestEnvelope = protocol.Request
type responseEnvelope = protocol.Response
type handshake = protocol.Handshake

const protocolVersion = protocol.Version

var requireEOF = protocol.RequireEOF

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type manifestDocument struct {
	ManifestVersion      int      `json:"manifestVersion"`
	PackageFormatVersion int      `json:"packageFormatVersion,omitempty"`
	OS                   []string `json:"os,omitempty"`
	Arch                 []string `json:"arch,omitempty"`
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Version              string   `json:"version"`
	ProtocolVersion      int      `json:"protocolVersion"`
	Executable           string   `json:"executable"`
	Capabilities         []struct {
		ID          string `json:"id"`
		Description string `json:"description,omitempty"`
	} `json:"capabilities"`
	Bindings []struct {
		ID         string   `json:"id"`
		Type       string   `json:"type"`
		Capability string   `json:"capability"`
		Actions    []string `json:"actions,omitempty"`
		Order      int      `json:"order,omitempty"`
	} `json:"bindings,omitempty"`
	Configuration struct {
		Version int               `json:"version"`
		Fields  []json.RawMessage `json:"fields,omitempty"`
	} `json:"configuration"`
}

func TestManifestDeclaresAIRuntimeArtifact(t *testing.T) {
	data, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest manifestDocument
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	if err := requireEOF(decoder); err != nil {
		t.Fatal(err)
	}
	if manifest.ManifestVersion != 1 || manifest.ProtocolVersion != protocolVersion || manifest.ID != extensionID || manifest.Version != extensionVersion {
		t.Fatalf("manifest identity/version mismatch: %+v", manifest)
	}
	if len(manifest.OS) != 1 || manifest.OS[0] != "darwin" {
		t.Fatalf("expected os [darwin], got %v", manifest.OS)
	}
	if len(manifest.Arch) != 1 || manifest.Arch[0] != "arm64" {
		t.Fatalf("expected arch [arm64], got %v", manifest.Arch)
	}
	if manifest.Name != "Sample AI Runtime Takeover" || manifest.Executable != "lh-plugin-ai-runtime" {
		t.Fatalf("invalid name/executable: %+v", manifest)
	}
	if len(manifest.Capabilities) != 1 || manifest.Capabilities[0].ID != aiRuntime {
		t.Fatalf("manifest is not exactly ai.runtime: %+v", manifest)
	}
	if len(manifest.Bindings) != 1 || manifest.Bindings[0].Type != "AI_RUNTIME" || manifest.Bindings[0].Capability != "ai.runtime" {
		t.Fatalf("manifest missing required AI_RUNTIME binding: %+v", manifest.Bindings)
	}
	if manifest.Configuration.Version != 1 || len(manifest.Configuration.Fields) < 3 {
		t.Fatalf("configuration schema missing fields: %+v", manifest.Configuration)
	}
}

func TestSubprocessProtocolHandshakeAndShutdown(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "lh-plugin-ai-runtime")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	build := exec.Command("go", "build", "-o", executable, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build executable: %v: %s", err, output)
	}

	command := exec.Command(executable)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(stdin)
	reader := bufio.NewReader(stdout)

	// 1. Handshake
	request(t, encoder, requestEnvelope{
		ID:     "hs-1",
		Method: "system.handshake",
		Params: mustJSON(t, handshake{
			ProtocolVersion:  1,
			ExtensionID:      extensionID,
			ExtensionVersion: extensionVersion,
			Capabilities:     []string{aiRuntime},
		}),
	})
	handshakeResponse := response(t, reader)
	if handshakeResponse.ID != "hs-1" || handshakeResponse.Error != nil {
		t.Fatalf("handshake response: %+v", handshakeResponse)
	}

	// 2. Start ai.runtime; the Sample requests a Core read.
	req := protocol.AIRuntimeInvocation{Phase: "START", ExecutionID: "req-sub-1", Request: &protocol.AIRuntimeRequest{
		RequestID: "req-sub-1", Purpose: "assistant.ask", Instructions: "Answer using current state.", Input: "Hello from test", OutputSchema: json.RawMessage(`{"type":"object"}`),
	}, Capabilities: []protocol.AICapability{{ID: "workitem.list", Kind: "READ", Description: "List work", InputSemantics: "object", ResultSemantics: "items", Origin: "CORE"}}}
	request(t, encoder, requestEnvelope{
		ID:     "invoke-1",
		Method: "capability.invoke",
		Params: mustJSON(t, capabilityInvokeParams{
			CapabilityID: aiRuntime,
			InstanceID:   "inst-sub",
			Config:       json.RawMessage(`{"mock_mode":true}`),
			Input:        mustJSON(t, req),
		}),
	})
	invokeResp := response(t, reader)
	if invokeResp.ID != "invoke-1" || invokeResp.Error != nil {
		t.Fatalf("invoke response: %+v", invokeResp)
	}
	var runtimeRes protocol.AIRuntimeResponse
	decodeResult(t, invokeResp, &runtimeRes)
	if runtimeRes.Status != "NEED_CAPABILITIES" || len(runtimeRes.Calls) != 1 || runtimeRes.Calls[0].ID != "workitem.list" {
		t.Fatalf("unexpected runtime response: %+v", runtimeRes)
	}

	// 3. Resume with the Core read result; the Sample finalizes.
	resume := protocol.AIRuntimeInvocation{Phase: "RESUME", ExecutionID: "req-sub-1", State: runtimeRes.State, Results: []protocol.AICapabilityResult{
		{Index: 0, Call: runtimeRes.Calls[0], Output: json.RawMessage(`[{"id":"wi-1","title":"Subprocess item"}]`)},
	}}
	request(t, encoder, requestEnvelope{
		ID:     "invoke-2",
		Method: "capability.invoke",
		Params: mustJSON(t, capabilityInvokeParams{
			CapabilityID: aiRuntime,
			InstanceID:   "inst-sub",
			Config:       json.RawMessage(`{"mock_mode":true}`),
			Input:        mustJSON(t, resume),
		}),
	})
	resumeResp := response(t, reader)
	if resumeResp.ID != "invoke-2" || resumeResp.Error != nil {
		t.Fatalf("resume response: %+v", resumeResp)
	}
	decodeResult(t, resumeResp, &runtimeRes)
	if runtimeRes.Status != "FINAL" || !strings.Contains(string(runtimeRes.Output), "Subprocess item") {
		t.Fatalf("unexpected resume response: %+v", runtimeRes)
	}

	// 4. Shutdown
	request(t, encoder, requestEnvelope{
		ID:     "sd-1",
		Method: "system.shutdown",
		Params: json.RawMessage(`{}`),
	})
	shutdownResponse := response(t, reader)
	if shutdownResponse.ID != "sd-1" || shutdownResponse.Error != nil {
		t.Fatalf("shutdown response: %+v", shutdownResponse)
	}
	_ = stdin.Close()
	_ = command.Wait()

	if !bytes.Contains(stderr.Bytes(), []byte("started")) || !bytes.Contains(stderr.Bytes(), []byte("shutdown accepted")) {
		t.Fatalf("expected diagnostics on stderr: %s", stderr.String())
	}
}

func request(t *testing.T, encoder *json.Encoder, value requestEnvelope) {
	t.Helper()
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
}

// response reads exactly one stdout line and requires it to be a protocol
// frame, so any diagnostic written to stdout fails the test.
func response(t *testing.T, reader *bufio.Reader) responseEnvelope {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	var value responseEnvelope
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("stdout line is not a protocol frame: %q: %v", line, err)
	}
	return value
}

func decodeResult(t *testing.T, response responseEnvelope, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Result, target); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

var (
	testLimits      = protocol.AIRuntimeLimits{MaxRounds: 4, MaxCalls: 8, MaxParallelCalls: 4, MaxContextBytes: 65536, MaxOutputBytes: 65536}
	testAnswer      = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["version","kind","text"],"properties":{"version":{"type":"integer"},"kind":{"type":"string"},"text":{"type":"string"}}}`)
	testReadCatalog = []protocol.AICapability{
		{ID: "workitem.list", Kind: "READ", Description: "List current work items", InputSemantics: `{"limit":number}`, ResultSemantics: "work items", Origin: "CORE"},
		{ID: "planner.item.create", Kind: "PROPOSE", Description: "Propose a todo", InputSemantics: `{"title":string}`, ResultSemantics: "proposal", Origin: "CORE"},
	}
)

// upstreamCall is one observed OpenAI-compatible request.
type upstreamCall struct {
	URL           string
	Authorization string
	Body          openAIChatRequest
}

// fakeUpstream answers each chat completion with the next scripted
// (status, body) pair and records what the plugin sent.
func fakeUpstream(t *testing.T, replies ...[2]string) (*http.Client, *[]upstreamCall) {
	t.Helper()
	var calls []upstreamCall
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body openAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		calls = append(calls, upstreamCall{URL: r.URL.String(), Authorization: r.Header.Get("Authorization"), Body: body})
		if len(calls) > len(replies) {
			t.Fatalf("unexpected upstream call %d", len(calls))
		}
		reply := replies[len(calls)-1]
		status := http.StatusOK
		if reply[0] != "" {
			fmt.Sscanf(reply[0], "%d", &status)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(reply[1]))}, nil
	})}
	return client, &calls
}

func chatReply(content string) [2]string {
	body, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
	})
	return [2]string{"", string(body)}
}

func runtimeParams(t *testing.T, config string, wire protocol.AIRuntimeInvocation) capabilityInvokeParams {
	t.Helper()
	return capabilityInvokeParams{CapabilityID: aiRuntime, InstanceID: "inst-1", Config: json.RawMessage(config), Input: mustJSON(t, wire)}
}

func startWire(input string, capabilities []protocol.AICapability) protocol.AIRuntimeInvocation {
	return protocol.AIRuntimeInvocation{Phase: "START", ExecutionID: "exec-1", Capabilities: capabilities, Request: &protocol.AIRuntimeRequest{
		RequestID: "exec-1", Purpose: "assistant.ask", Instructions: "Answer as the Lum Harbor assistant.", Input: input, OutputSchema: testAnswer, Limits: testLimits,
	}}
}

func decodeRuntime(t *testing.T, result protocol.CapabilityResult) protocol.AIRuntimeResponse {
	t.Helper()
	if result.Error != nil {
		t.Fatalf("unexpected runtime error: %+v", result.Error)
	}
	var response protocol.AIRuntimeResponse
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func messagesText(messages []aiMessage) string {
	var builder strings.Builder
	for _, message := range messages {
		builder.WriteString(message.Role + ": " + message.Content + "\n")
	}
	return builder.String()
}

const ollamaConfig = `{"base_url":"http://127.0.0.1:11434/v1","model":"qwen3:14b","timeout_seconds":120}`

func TestMockModeRunsStartResumeFinalOffline(t *testing.T) {
	offline := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("mock mode must not touch the network: %s", r.URL)
		return nil, nil
	})}
	server := NewServer(log.New(io.Discard, "", 0), offline)

	need := decodeRuntime(t, server.handleAIRuntime(runtimeParams(t, `{"mock_mode":true}`, startWire("What work items exist?", testReadCatalog))))
	if need.Status != "NEED_CAPABILITIES" || len(need.Calls) != 1 || need.Calls[0].ID != "workitem.list" || !json.Valid(need.State) {
		t.Fatalf("mock START must request a Core read: %+v", need)
	}
	final := decodeRuntime(t, server.handleAIRuntime(runtimeParams(t, `{"mock_mode":true}`, protocol.AIRuntimeInvocation{
		Phase: "RESUME", ExecutionID: "exec-1", State: need.State,
		Results: []protocol.AICapabilityResult{{Index: 0, Call: need.Calls[0], Output: json.RawMessage(`[{"title":"Mock evidence"}]`)}},
	})))
	if final.Status != "FINAL" || !strings.Contains(string(final.Output), "Mock evidence") {
		t.Fatalf("mock RESUME must finalize with evidence: %+v", final)
	}

	verify := startWire("verify", testReadCatalog)
	verify.Request.Purpose = "runtime.verify"
	if response := decodeRuntime(t, server.handleAIRuntime(runtimeParams(t, `{"mock_mode":true}`, verify))); response.Status != "FINAL" || string(response.Output) != `{"ok":true}` {
		t.Fatalf("mock verify: %+v", response)
	}
}

func TestMissingUpstreamConfigFailsExplicitly(t *testing.T) {
	cases := map[string]string{
		`{"mock_mode":false,"model":"qwen3:14b"}`:                    "base_url is required",
		`{"mock_mode":false,"base_url":"http://127.0.0.1:11434/v1"}`: "model is required",
		`{}`: "base_url and model are required",
	}
	for config, want := range cases {
		client, calls := fakeUpstream(t)
		result := NewServer(log.New(io.Discard, "", 0), client).handleAIRuntime(runtimeParams(t, config, startWire("hello", nil)))
		if result.Error == nil || result.Error.Code != "configuration_error" || !strings.Contains(result.Error.Message, want) {
			t.Fatalf("config %s: expected configuration_error %q, got %+v", config, want, result)
		}
		if len(*calls) != 0 {
			t.Fatalf("config %s: must not fall back to any upstream call", config)
		}
	}
}

func TestStartReturnsFinalDirectlyWithoutAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	client, calls := fakeUpstream(t, chatReply(`{"version":1,"kind":"ANSWER","text":"Hello there."}`))
	server := NewServer(log.New(io.Discard, "", 0), client)

	response := decodeRuntime(t, server.handleAIRuntime(runtimeParams(t, ollamaConfig, startWire("Hello", nil))))
	if response.Status != "FINAL" || string(response.Output) != `{"version":1,"kind":"ANSWER","text":"Hello there."}` {
		t.Fatalf("expected direct FINAL: %+v", response)
	}
	if len(*calls) != 1 {
		t.Fatalf("no READ capabilities means a single final model call, got %d", len(*calls))
	}
	call := (*calls)[0]
	if call.URL != "http://127.0.0.1:11434/v1/chat/completions" {
		t.Fatalf("unexpected upstream URL %q", call.URL)
	}
	if call.Authorization != "" {
		t.Fatalf("API key is optional; no Authorization header expected, got %q", call.Authorization)
	}
	if call.Body.Model != "qwen3:14b" || call.Body.ResponseFormat == nil || string(call.Body.ResponseFormat.JSONSchema.Schema) != string(testAnswer) {
		t.Fatalf("final call must use the caller output schema: %+v", call.Body)
	}
	if text := messagesText(call.Body.Messages); strings.Contains(text, `"phase"`) || strings.Contains(text, `"executionId"`) || strings.Contains(text, `"outputSchema"`) {
		t.Fatalf("ai.runtime wire format leaked into model messages:\n%s", text)
	}
}

func TestAuthorizationHeaderWhenAPIKeyPresent(t *testing.T) {
	client, calls := fakeUpstream(t, chatReply(`{"version":1,"kind":"ANSWER","text":"ok"}`))
	config := `{"base_url":"https://api.openai.com/v1/","model":"gpt-test","api_key":"sk-test-123"}`
	decodeRuntime(t, NewServer(log.New(io.Discard, "", 0), client).handleAIRuntime(runtimeParams(t, config, startWire("Hello", nil))))
	if got := (*calls)[0]; got.Authorization != "Bearer sk-test-123" || got.URL != "https://api.openai.com/v1/chat/completions" {
		t.Fatalf("unexpected auth/url: %+v", got)
	}
}

func TestCoreReadResumeLeadsToFinal(t *testing.T) {
	client, calls := fakeUpstream(t,
		chatReply(`{"version":1,"decision":"CALL","calls":[{"capability_id":"workitem.list","arguments_json":"{\"limit\":10}"}]}`),
		chatReply(`{"version":1,"decision":"FINAL","calls":[]}`),
		chatReply(`{"version":1,"kind":"ANSWER","text":"You have one work item: Ship the fix."}`),
	)
	server := NewServer(log.New(io.Discard, "", 0), client)

	need := decodeRuntime(t, server.handleAIRuntime(runtimeParams(t, ollamaConfig, startWire("What work items currently exist?", testReadCatalog))))
	if need.Status != "NEED_CAPABILITIES" || len(need.Calls) != 1 || need.Calls[0].ID != "workitem.list" || string(need.Calls[0].Arguments) != `{"limit":10}` {
		t.Fatalf("START must request the Core read: %+v", need)
	}
	if decision := (*calls)[0].Body; string(decision.ResponseFormat.JSONSchema.Schema) != string(decisionSchema) ||
		!strings.Contains(messagesText(decision.Messages), "workitem.list [READ]") || strings.Contains(messagesText(decision.Messages), "planner.item.create") {
		t.Fatalf("decision call must offer only READ capabilities under the decision schema: %+v", decision)
	}

	final := decodeRuntime(t, server.handleAIRuntime(runtimeParams(t, ollamaConfig, protocol.AIRuntimeInvocation{
		Phase: "RESUME", ExecutionID: "exec-1", State: need.State,
		Results: []protocol.AICapabilityResult{{Index: 0, Call: need.Calls[0], Output: json.RawMessage(`[{"id":"wi-1","title":"Ship the fix"}]`)}},
	})))
	if final.Status != "FINAL" || !strings.Contains(string(final.Output), "Ship the fix") || len(final.Calls) != 0 {
		t.Fatalf("RESUME must finalize: %+v", final)
	}
	if final.Usage == nil || final.Usage.TotalTokens != 30 {
		t.Fatalf("usage must cover decision and final calls: %+v", final.Usage)
	}
	for _, index := range []int{1, 2} {
		if text := messagesText((*calls)[index].Body.Messages); !strings.Contains(text, `"title":"Ship the fix"`) {
			t.Fatalf("call %d must see Core evidence:\n%s", index, text)
		}
	}
}

func TestResumeAccumulatesEvidenceAcrossRounds(t *testing.T) {
	client, calls := fakeUpstream(t,
		chatReply(`{"version":1,"decision":"CALL","calls":[{"capability_id":"workitem.list","arguments_json":"{}"}]}`),
		chatReply(`{"version":1,"decision":"CALL","calls":[{"capability_id":"workitem.list","arguments_json":"{\"limit\":50}"}]}`),
		chatReply(`{"version":1,"decision":"FINAL","calls":[]}`),
		chatReply(`{"version":1,"kind":"ANSWER","text":"done"}`),
	)
	server := NewServer(log.New(io.Discard, "", 0), client)
	response := decodeRuntime(t, server.handleAIRuntime(runtimeParams(t, ollamaConfig, startWire("List everything", testReadCatalog))))
	for round, evidence := range []string{"first-round-item", "second-round-item"} {
		response = decodeRuntime(t, server.handleAIRuntime(runtimeParams(t, ollamaConfig, protocol.AIRuntimeInvocation{
			Phase: "RESUME", ExecutionID: "exec-1", State: response.State,
			Results: []protocol.AICapabilityResult{{Index: 0, Call: response.Calls[0], Output: json.RawMessage(`["` + evidence + `"]`)}},
		})))
		if want := []string{"NEED_CAPABILITIES", "FINAL"}[round]; response.Status != want {
			t.Fatalf("round %d: expected %s, got %+v", round, want, response)
		}
	}
	if text := messagesText((*calls)[3].Body.Messages); !strings.Contains(text, "first-round-item") || !strings.Contains(text, "second-round-item") {
		t.Fatalf("final call must see evidence from every round:\n%s", text)
	}
}

func TestMalformedUpstreamResponsesFailClosed(t *testing.T) {
	cases := []struct {
		name    string
		reply   [2]string
		catalog []protocol.AICapability
		code    string
	}{
		{"non-JSON body", [2]string{"", "<html>gateway</html>"}, nil, "decode_upstream_response_error"},
		{"no choices", [2]string{"", `{"choices":[]}`}, nil, "empty_choices"},
		{"prose instead of JSON", chatReply("Here are your work items."), nil, "invalid_upstream_output"},
		{"bad decision arguments", chatReply(`{"version":1,"decision":"CALL","calls":[{"capability_id":"workitem.list","arguments_json":"limit=10"}]}`), testReadCatalog, "plugin_protocol_error"},
		{"unknown decision", chatReply(`{"version":1,"decision":"MAYBE","calls":[]}`), testReadCatalog, "plugin_protocol_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := fakeUpstream(t, tc.reply)
			result := NewServer(log.New(io.Discard, "", 0), client).handleAIRuntime(runtimeParams(t, ollamaConfig, startWire("hello", tc.catalog)))
			if result.Error == nil || result.Error.Code != tc.code {
				t.Fatalf("expected %s, got %+v", tc.code, result)
			}
		})
	}
}

func TestNon2xxUpstreamStatusIsSurfaced(t *testing.T) {
	client, _ := fakeUpstream(t, [2]string{"404", `{"error":{"message":"model 'qwen3:8b' not found"}}`})
	var diagnostics bytes.Buffer
	result := NewServer(log.New(&diagnostics, "", 0), client).handleAIRuntime(runtimeParams(t, ollamaConfig, startWire("hello", nil)))
	if result.Error == nil || result.Error.Code != "upstream_status_error" || !strings.Contains(result.Error.Message, "status 404") || !strings.Contains(result.Error.Message, "not found") {
		t.Fatalf("expected surfaced 404, got %+v", result)
	}
	if !strings.Contains(diagnostics.String(), "status 404") {
		t.Fatalf("status must be logged to diagnostics (stderr): %q", diagnostics.String())
	}
}

func TestUpstreamTimeoutIsConfiguredAndBounded(t *testing.T) {
	var deadline time.Time
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, _ = r.Context().Deadline()
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(chatReply(`{"ok":true}`)[1]))}, nil
	})}
	started := time.Now()
	decodeRuntime(t, NewServer(log.New(io.Discard, "", 0), client).handleAIRuntime(runtimeParams(t, ollamaConfig, startWire("hello", nil))))
	if deadline.Before(started.Add(110*time.Second)) || deadline.After(started.Add(121*time.Second)) {
		t.Fatalf("expected ~120s deadline from timeout_seconds, got %v", deadline.Sub(started))
	}

	hanging := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(5 * time.Second):
			return nil, fmt.Errorf("deadline was not enforced")
		}
	})}
	started = time.Now()
	result := NewServer(log.New(io.Discard, "", 0), hanging).handleAIRuntime(runtimeParams(t, `{"base_url":"http://127.0.0.1:11434/v1","model":"m","timeout_seconds":0.05}`, startWire("hello", nil)))
	if result.Error == nil || result.Error.Code != "upstream_network_error" || !strings.Contains(result.Error.Message, "deadline exceeded") {
		t.Fatalf("expected bounded timeout failure, got %+v", result)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("timeout was not bounded: %v", elapsed)
	}
}
