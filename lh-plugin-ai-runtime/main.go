package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	pluginenv "lh-plugin-base/env"
	"lh-plugin-base/protocol"
	pluginruntime "lh-plugin-base/runtime"
)

const (
	extensionID      = "lh-plugin-ai-runtime"
	extensionVersion = "0.1.0"
	aiRuntime        = "ai.runtime"
)

type aiMessage = protocol.AIMessage

type aiUsage = protocol.AIUsage

type pluginConfig struct {
	BaseURL        string  `json:"base_url,omitempty"`
	Endpoint       string  `json:"endpoint,omitempty"`
	Model          string  `json:"model,omitempty"`
	APIKey         string  `json:"api_key,omitempty"`
	TimeoutSeconds float64 `json:"timeout_seconds,omitempty"`
	MockMode       bool    `json:"mock_mode,omitempty"`
}

func (c pluginConfig) resolveBaseURL() string {
	if c.BaseURL != "" {
		return strings.TrimSpace(c.BaseURL)
	}
	return strings.TrimSpace(c.Endpoint)
}

func (c pluginConfig) resolveAPIKey() string {
	if c.APIKey != "" {
		return strings.TrimSpace(c.APIKey)
	}
	return strings.TrimSpace(pluginenv.OpenAIAPIKey())
}

type openAIChatRequest struct {
	Model          string             `json:"model"`
	Messages       []aiMessage        `json:"messages"`
	Temperature    float64            `json:"temperature"`
	ResponseFormat *openAIResponseFmt `json:"response_format,omitempty"`
}

type openAIResponseFmt struct {
	Type       string           `json:"type"`
	JSONSchema openAIJSONSchema `json:"json_schema"`
}

type openAIJSONSchema struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type openAIUsage struct {
	PromptTokensSnake     int `json:"prompt_tokens"`
	PromptTokensCamel     int `json:"promptTokens"`
	CompletionTokensSnake int `json:"completion_tokens"`
	CompletionTokensCamel int `json:"completionTokens"`
	TotalTokensSnake      int `json:"total_tokens"`
	TotalTokensCamel      int `json:"totalTokens"`
}

func (u openAIUsage) promptTokens() int {
	if u.PromptTokensSnake > 0 {
		return u.PromptTokensSnake
	}
	return u.PromptTokensCamel
}

func (u openAIUsage) completionTokens() int {
	if u.CompletionTokensSnake > 0 {
		return u.CompletionTokensSnake
	}
	return u.CompletionTokensCamel
}

func (u openAIUsage) totalTokens() int {
	if u.TotalTokensSnake > 0 {
		return u.TotalTokensSnake
	}
	return u.TotalTokensCamel
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage openAIUsage `json:"usage"`
}

type Server struct {
	logger     *log.Logger
	httpClient *http.Client
}

func NewServer(logger *log.Logger, client *http.Client) *Server {
	if client == nil {
		client = &http.Client{}
	}
	return &Server{
		logger:     logger,
		httpClient: client,
	}
}

func main() {
	if err := run(os.Stdin, os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "lh-plugin-ai-runtime: %v\n", err)
		os.Exit(1)
	}
}

func run(input io.Reader, output io.Writer, diagnostics io.Writer) error {
	logger := log.New(diagnostics, "lh-plugin-ai-runtime: ", 0)
	server := NewServer(logger, nil)
	runtime, err := pluginruntime.New(pluginruntime.Metadata{ID: extensionID, Version: extensionVersion}, logger)
	if err != nil {
		return err
	}
	if err := runtime.Register(aiRuntime, server.handleAIRuntime); err != nil {
		return err
	}
	return runtime.Serve(input, output)
}

// runtimeState is the opaque Plugin-owned state Core echoes on RESUME. Core
// sends only the current round's results, so evidence accumulates here.
type runtimeState struct {
	Request      protocol.AIRuntimeRequest     `json:"request"`
	Capabilities []protocol.AICapability       `json:"capabilities"`
	Evidence     []protocol.AICapabilityResult `json:"evidence,omitempty"`
	Rounds       int                           `json:"rounds,omitempty"`
}

func (s *Server) handleAIRuntime(params protocol.CapabilityInvokeParams) protocol.CapabilityResult {
	var wire protocol.AIRuntimeInvocation
	if err := protocol.Decode(params.Input, &wire); err != nil {
		return protocol.Failure("invalid_runtime_request", err.Error())
	}
	var cfg pluginConfig
	if len(params.Config) > 0 && string(params.Config) != "null" {
		if err := json.Unmarshal(params.Config, &cfg); err != nil {
			return protocol.Failure("configuration_error", "configuration is invalid")
		}
	}
	if cfg.MockMode {
		return s.serveMockRuntime(wire)
	}
	// Never silently fall back to mock mode; fail closed explicitly.
	missingBaseURL := cfg.resolveBaseURL() == ""
	missingModel := strings.TrimSpace(cfg.Model) == ""
	switch {
	case missingBaseURL && missingModel:
		return protocol.Failure("configuration_error", "base_url and model are required when mock_mode is false")
	case missingBaseURL:
		return protocol.Failure("configuration_error", "base_url is required when mock_mode is false")
	case missingModel:
		return protocol.Failure("configuration_error", "model is required when mock_mode is false")
	}
	return s.serveModelRuntime(wire, cfg)
}

func (s *Server) serveMockRuntime(wire protocol.AIRuntimeInvocation) protocol.CapabilityResult {
	if wire.Phase == "START" {
		if wire.Request == nil || wire.Request.RequestID == "" {
			return protocol.Failure("invalid_runtime_request", "START requires request")
		}
		if wire.Request.Purpose == "source.clean" {
			return protocol.Success(protocol.AIRuntimeResponse{Status: "FINAL", Output: json.RawMessage(`{"version":"source.clean.v1","normalizedDocument":"{\"cleaned\":true,\"summary\":\"Normalized source observation\"}"}`), Metadata: map[string]string{"runtime": extensionID, "mode": "mock-offline"}})
		}
		if wire.Request.Purpose == "runtime.verify" {
			return protocol.Success(protocol.AIRuntimeResponse{Status: "FINAL", Output: json.RawMessage(`{"ok":true}`), Metadata: map[string]string{"runtime": extensionID, "mode": "mock-offline"}})
		}
		state, _ := json.Marshal(runtimeState{Request: *wire.Request, Capabilities: wire.Capabilities})
		if capabilityID := firstReadableCapability(wire.Capabilities); capabilityID != "" {
			arguments := json.RawMessage(`{}`)
			switch capabilityID {
			case "workitem.list":
				arguments = json.RawMessage(`{"limit":20}`)
			case "entity.search":
				arguments = json.RawMessage(`{"query":"work"}`)
			case "read.query":
				arguments = json.RawMessage(`{"query":"current project state"}`)
			}
			return protocol.Success(protocol.AIRuntimeResponse{Status: "NEED_CAPABILITIES", State: state, Calls: []protocol.AICapabilityCall{{ID: capabilityID, Arguments: arguments}}, Metadata: map[string]string{"runtime": extensionID, "mode": "mock-offline"}})
		}
		return protocol.Success(mockFinal(*wire.Request, nil))
	}
	if wire.Phase != "RESUME" {
		return protocol.Failure("invalid_runtime_request", "phase must be START or RESUME")
	}
	var state runtimeState
	if err := protocol.Decode(wire.State, &state); err != nil {
		return protocol.Failure("invalid_runtime_state", err.Error())
	}
	return protocol.Success(mockFinal(state.Request, wire.Results))
}

func firstReadableCapability(capabilities []protocol.AICapability) string {
	for _, preferred := range []string{"workitem.list", "entity.search"} {
		for _, capability := range capabilities {
			if capability.Kind == "READ" && capability.ID == preferred {
				return capability.ID
			}
		}
	}
	for _, capability := range capabilities {
		if capability.Kind == "READ" {
			return capability.ID
		}
	}
	return ""
}

func mockFinal(request protocol.AIRuntimeRequest, results []protocol.AICapabilityResult) protocol.AIRuntimeResponse {
	metadata := map[string]string{"runtime": extensionID, "mode": "mock-offline"}
	switch request.Purpose {
	case "source.interpret":
		return protocol.AIRuntimeResponse{Status: "FINAL", Output: json.RawMessage(`{"version":"source.interpret.v1","summary":"Interpreted source using Host-governed capabilities.","interpretationDocument":{"candidate":{"version":1,"capability_id":"planner.item.create","arguments":"{\"title\":\"Review interpreted source\"}","reason":"The source requests a reviewable action."},"plan":null}}`), Metadata: metadata}
	case "assistant.ask":
		lower := strings.ToLower(request.Input)
		if strings.Contains(lower, "create") || strings.Contains(lower, "remind") || strings.Contains(lower, "add a todo") {
			return protocol.AIRuntimeResponse{Status: "FINAL", Output: json.RawMessage(`{"version":1,"kind":"MUTATION_CANDIDATE","text":"I prepared a reviewable Todo proposal.","candidate":{"version":1,"capability_id":"planner.item.create","arguments":"{\"title\":\"Plugin AI requested Todo\"}","reason":"Requested by the user through the Plugin AI Runtime."},"plan":null}`), Metadata: metadata}
		}
		encoded, _ := json.Marshal(results)
		text := "Plugin AI Runtime read current Lum Harbor state through Core capabilities."
		if len(results) > 0 {
			text += " Evidence: " + string(encoded)
		}
		output, _ := json.Marshal(map[string]any{"version": 1, "kind": "ANSWER", "text": text, "candidate": nil, "plan": nil})
		return protocol.AIRuntimeResponse{Status: "FINAL", Output: output, Metadata: metadata}
	default:
		output, _ := json.Marshal(map[string]any{"status": "ok", "purpose": request.Purpose})
		return protocol.AIRuntimeResponse{Status: "FINAL", Output: output, Metadata: metadata}
	}
}

// decisionSchema mirrors Core's Native Runtime decision envelope. Arguments
// travel as JSON text so the schema stays valid under OpenAI strict mode.
var decisionSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["version","decision","calls"],"properties":{"version":{"type":"integer","const":1},"decision":{"type":"string","enum":["FINAL","CALL"]},"calls":{"type":"array","maxItems":4,"items":{"type":"object","additionalProperties":false,"required":["capability_id","arguments_json"],"properties":{"capability_id":{"type":"string"},"arguments_json":{"type":"string"}}}}}}`)

// serveModelRuntime translates the Runtime request into model messages. Each
// START/RESUME first asks the model whether Core READ capabilities are needed
// (NEED_CAPABILITIES); otherwise a second call produces the caller's output
// under the caller's own schema (FINAL).
func (s *Server) serveModelRuntime(wire protocol.AIRuntimeInvocation, cfg pluginConfig) protocol.CapabilityResult {
	var state runtimeState
	switch wire.Phase {
	case "START":
		if wire.Request == nil || wire.Request.RequestID == "" {
			return protocol.Failure("invalid_runtime_request", "START requires request")
		}
		state = runtimeState{Request: *wire.Request, Capabilities: wire.Capabilities}
	case "RESUME":
		if err := protocol.Decode(wire.State, &state); err != nil {
			return protocol.Failure("invalid_runtime_state", err.Error())
		}
		state.Evidence = append(state.Evidence, wire.Results...)
		state.Rounds++
	default:
		return protocol.Failure("invalid_runtime_request", "phase must be START or RESUME")
	}
	metadata := map[string]string{"runtime": extensionID, "model": cfg.Model}
	usage := &aiUsage{}

	remaining := state.Request.Limits.MaxCalls - len(state.Evidence)
	canRead := hasReadCapability(state.Capabilities) && remaining > 0 &&
		(state.Request.Limits.MaxRounds == 0 || state.Rounds < state.Request.Limits.MaxRounds)
	if canRead {
		output, stepUsage, failure := s.callUpstreamOpenAI(buildDecisionMessages(state, remaining), decisionSchema, cfg)
		if failure != nil {
			return *failure
		}
		addUsage(usage, stepUsage)
		calls, err := decodeDecision(output)
		if err != nil {
			return protocol.Failure("plugin_protocol_error", err.Error())
		}
		if len(calls) > 0 {
			stateJSON, _ := json.Marshal(state)
			return protocol.Success(protocol.AIRuntimeResponse{Status: "NEED_CAPABILITIES", State: stateJSON, Calls: calls, Usage: usage, Metadata: metadata})
		}
	}

	output, stepUsage, failure := s.callUpstreamOpenAI(buildFinalMessages(state), state.Request.OutputSchema, cfg)
	if failure != nil {
		return *failure
	}
	addUsage(usage, stepUsage)
	return protocol.Success(protocol.AIRuntimeResponse{Status: "FINAL", Output: output, Usage: usage, Metadata: metadata})
}

func hasReadCapability(capabilities []protocol.AICapability) bool {
	for _, capability := range capabilities {
		if capability.Kind == "READ" {
			return true
		}
	}
	return false
}

func buildDecisionMessages(state runtimeState, remaining int) []aiMessage {
	messages := []aiMessage{{Role: "system", Content: `You are the capability-selection stage of a Lum Harbor AI Runtime.
Decide whether authoritative READ capabilities are needed before producing the caller's final result.
Return CALL with one or more independent READ calls when facts about current Lum Harbor state are needed.
Return FINAL when the supplied context and accumulated results are sufficient.
For each CALL item, return capability_id and arguments_json. arguments_json must be a JSON-encoded object string matching that capability's documented input.
Never request a PROPOSE capability here. Never invent capability IDs. Do not repeat a call whose result is already present.
Return only the required JSON envelope.

Caller final-result policy:
` + state.Request.Instructions + "\n\n" + renderCapabilities(state.Capabilities, "READ") + fmt.Sprintf("\nRemaining total call budget: %d", remaining)}}
	messages = append(messages, state.Request.Context...)
	if len(state.Evidence) > 0 {
		messages = append(messages, aiMessage{Role: "system", Content: "Capability results already returned by Lum Harbor, in request order:\n" + renderEvidence(state.Evidence)})
	}
	return append(messages, aiMessage{Role: "user", Content: state.Request.Input})
}

func buildFinalMessages(state runtimeState) []aiMessage {
	messages := []aiMessage{{Role: "system", Content: state.Request.Instructions + "\n\n" + renderCapabilities(state.Capabilities, "")}}
	messages = append(messages, state.Request.Context...)
	if len(state.Evidence) == 0 {
		messages = append(messages, aiMessage{Role: "system", Content: "No capabilities were executed. Do not claim that Lum Harbor state was queried."})
	} else {
		messages = append(messages, aiMessage{Role: "system", Content: "Capability results returned by Lum Harbor, in request order:\n" + renderEvidence(state.Evidence)})
	}
	return append(messages, aiMessage{Role: "user", Content: state.Request.Input})
}

func renderCapabilities(capabilities []protocol.AICapability, kind string) string {
	var builder strings.Builder
	builder.WriteString("Available capabilities:\n")
	count := 0
	for _, capability := range capabilities {
		if kind != "" && capability.Kind != kind {
			continue
		}
		count++
		fmt.Fprintf(&builder, "- %s [%s]: %s\n  Use when: %s\n  Input: %s\n  Result: %s\n", capability.ID, capability.Kind, capability.Description, capability.UseWhen, capability.InputSemantics, capability.ResultSemantics)
	}
	if count == 0 {
		builder.WriteString("(none)\n")
	}
	return builder.String()
}

func renderEvidence(evidence []protocol.AICapabilityResult) string {
	var builder strings.Builder
	for i, result := range evidence {
		fmt.Fprintf(&builder, "%d. %s %s\n", i+1, result.Call.ID, string(result.Call.Arguments))
		if result.Error != nil {
			fmt.Fprintf(&builder, "   error: %s: %s\n", result.Error.Code, result.Error.Message)
		} else {
			fmt.Fprintf(&builder, "   result: %s\n", string(result.Output))
		}
	}
	return builder.String()
}

func decodeDecision(raw json.RawMessage) ([]protocol.AICapabilityCall, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope struct {
		Version  int    `json:"version"`
		Decision string `json:"decision"`
		Calls    []struct {
			CapabilityID  string `json:"capability_id"`
			ArgumentsJSON string `json:"arguments_json"`
		} `json:"calls"`
	}
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode capability decision: %v", err)
	}
	if envelope.Version != 1 {
		return nil, fmt.Errorf("unsupported decision version %d", envelope.Version)
	}
	switch envelope.Decision {
	case "FINAL":
		return nil, nil
	case "CALL":
		if len(envelope.Calls) == 0 {
			return nil, fmt.Errorf("CALL decision must include calls")
		}
	default:
		return nil, fmt.Errorf("unsupported decision %q", envelope.Decision)
	}
	calls := make([]protocol.AICapabilityCall, len(envelope.Calls))
	for i, call := range envelope.Calls {
		id := strings.TrimSpace(call.CapabilityID)
		arguments := bytes.TrimSpace([]byte(call.ArgumentsJSON))
		var object map[string]json.RawMessage
		if id == "" || len(arguments) == 0 || arguments[0] != '{' || json.Unmarshal(arguments, &object) != nil {
			return nil, fmt.Errorf("invalid capability call %d", i)
		}
		calls[i] = protocol.AICapabilityCall{ID: id, Arguments: json.RawMessage(arguments)}
	}
	return calls, nil
}

func addUsage(total *aiUsage, step *aiUsage) {
	if step == nil {
		return
	}
	total.InputTokens += step.InputTokens
	total.OutputTokens += step.OutputTokens
	total.TotalTokens += step.TotalTokens
}

func sanitizeError(msg string) string {
	msg = strings.TrimSpace(msg)
	const maxLen = 500
	if len(msg) > maxLen {
		msg = msg[:maxLen] + "..."
	}
	if strings.Contains(msg, "Bearer ") {
		re := regexp.MustCompile(`Bearer\s+[A-Za-z0-9._~+/-]+=*`)
		msg = re.ReplaceAllString(msg, "Bearer [REDACTED]")
	}
	return msg
}

// callUpstreamOpenAI performs one OpenAI-compatible chat completion and returns
// the JSON content of the first choice. Malformed responses fail closed.
func (s *Server) callUpstreamOpenAI(messages []aiMessage, outputSchema json.RawMessage, cfg pluginConfig) (json.RawMessage, *aiUsage, *protocol.CapabilityResult) {
	fail := func(code, message string) (json.RawMessage, *aiUsage, *protocol.CapabilityResult) {
		result := protocol.Failure(code, message)
		return nil, nil, &result
	}
	endpoint := strings.TrimRight(cfg.resolveBaseURL(), "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}

	openAIReq := openAIChatRequest{
		Model:       cfg.Model,
		Messages:    messages,
		Temperature: 0.1,
	}
	if hasOutputSchema(outputSchema) {
		var schemaMap map[string]any
		if err := json.Unmarshal(outputSchema, &schemaMap); err != nil {
			return fail("invalid_output_schema", fmt.Sprintf("malformed output schema: %v", err))
		}
		openAIReq.ResponseFormat = &openAIResponseFmt{
			Type: "json_schema",
			JSONSchema: openAIJSONSchema{
				Name:   "lum_harbor_response",
				Strict: true,
				Schema: outputSchema,
			},
		}
	}

	reqBody, err := json.Marshal(openAIReq)
	if err != nil {
		return fail("encode_error", sanitizeError(err.Error()))
	}

	timeout := 30 * time.Second
	if cfg.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds * float64(time.Second))
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return fail("http_request_creation_failed", sanitizeError(err.Error()))
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey := cfg.resolveAPIKey(); apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	httpResp, err := s.httpClient.Do(httpReq)
	if err != nil {
		s.logger.Printf("upstream OpenAI call failed: %v", err)
		return fail("upstream_network_error", sanitizeError(err.Error()))
	}
	defer httpResp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(httpResp.Body, 1024*1024))
	if err != nil {
		return fail("read_response_error", sanitizeError(err.Error()))
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode > 299 {
		s.logger.Printf("upstream OpenAI status %d: %s", httpResp.StatusCode, sanitizeError(string(bodyBytes)))
		return fail("upstream_status_error", fmt.Sprintf("status %d: %s", httpResp.StatusCode, sanitizeError(string(bodyBytes))))
	}

	var chatResp openAIChatResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		return fail("decode_upstream_response_error", sanitizeError(err.Error()))
	}
	if len(chatResp.Choices) == 0 {
		return fail("empty_choices", "upstream returned no completion choices")
	}
	content := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	if content == "" || !json.Valid([]byte(content)) {
		s.logger.Printf("upstream OpenAI returned non-JSON content: %s", sanitizeError(content))
		return fail("invalid_upstream_output", "upstream model content is not valid JSON")
	}
	return json.RawMessage(content), &aiUsage{
		InputTokens:  chatResp.Usage.promptTokens(),
		OutputTokens: chatResp.Usage.completionTokens(),
		TotalTokens:  chatResp.Usage.totalTokens(),
	}, nil
}

func hasOutputSchema(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}
