// Package protocol defines the Lum Harbor Plugin Runtime V1 wire contract.
//
// These types deliberately mirror the Core host contract. They are data and
// encoding helpers only; capability business logic belongs to each plugin.
package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const (
	Version = 1

	MethodHandshake        = "system.handshake"
	MethodCapabilityInvoke = "capability.invoke"
	MethodShutdown         = "system.shutdown"

	ErrorInvalidHandshake      = "invalid_handshake"
	ErrorIncompatibleHandshake = "incompatible_handshake"
	ErrorInvalidInvocation     = "invalid_invocation"
	ErrorCapabilityUnsupported = "capability_not_supported"
	ErrorMethodUnsupported     = "method_not_supported"
	ErrorInternal              = "internal_error"
)

type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

type Handshake struct {
	ProtocolVersion  int      `json:"protocolVersion"`
	ExtensionID      string   `json:"extensionId"`
	ExtensionVersion string   `json:"extensionVersion"`
	Capabilities     []string `json:"capabilities"`
}

type CapabilityInvokeParams struct {
	CapabilityID string          `json:"capabilityId"`
	InstanceID   string          `json:"instanceId,omitempty"`
	Config       json.RawMessage `json:"config,omitempty"`
	Input        json.RawMessage `json:"input"`
}

// CapabilityResult is returned by a registered capability handler. The
// runtime adds request correlation and the outer response envelope.
type CapabilityResult struct {
	Result json.RawMessage
	Error  *Error
}

func Success(value any) CapabilityResult {
	encoded, err := json.Marshal(value)
	if err != nil {
		return Failure(ErrorInternal, "could not encode result")
	}
	return CapabilityResult{Result: encoded}
}

func Failure(code, message string) CapabilityResult {
	return CapabilityResult{Error: NewError(code, message)}
}

// Decode strictly decodes exactly one JSON value. Unknown object fields and
// trailing JSON values are rejected so plugins behave consistently with Core.
func Decode(data json.RawMessage, target any) error {
	if len(data) == 0 {
		return fmt.Errorf("params must not be empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return RequireEOF(decoder)
}

func RequireEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

type SourceFetchRequest struct {
	Cursor  *string           `json:"cursor,omitempty"`
	Context map[string]string `json:"context,omitempty"`
}

type SourceFetchResult struct {
	Records    []SourceRecord `json:"records"`
	NextCursor *string        `json:"nextCursor,omitempty"`
}

type SourceRecord struct {
	ExternalID string          `json:"externalId"`
	SourceKind string          `json:"sourceKind"`
	ObservedAt string          `json:"observedAt"`
	Payload    json.RawMessage `json:"payload"`
}

type ReadQueryRequest struct {
	Query string `json:"query"`
}

type ReadFact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ReadQueryResult struct {
	Summary string     `json:"summary"`
	Facts   []ReadFact `json:"facts"`
}

// AIRuntimeRequest is the high-level caller-neutral request received on the
// ai.runtime capability. Plugin authors own reasoning/model transport; Core
// owns every capability execution.
type AIRuntimeRequest struct {
	RequestID    string            `json:"requestId"`
	Purpose      string            `json:"purpose"`
	Instructions string            `json:"instructions"`
	Context      []AIMessage       `json:"context,omitempty"`
	Input        string            `json:"input"`
	OutputSchema json.RawMessage   `json:"outputSchema"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Limits       AIRuntimeLimits   `json:"limits"`
}

type AIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type AIRuntimeLimits struct {
	MaxRounds        int `json:"maxRounds"`
	MaxCalls         int `json:"maxCalls"`
	MaxParallelCalls int `json:"maxParallelCalls"`
	MaxContextBytes  int `json:"maxContextBytes"`
	MaxOutputBytes   int `json:"maxOutputBytes"`
}
type AICapability struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	Description     string `json:"description"`
	UseWhen         string `json:"useWhen,omitempty"`
	InputSemantics  string `json:"inputSemantics"`
	ResultSemantics string `json:"resultSemantics"`
	Origin          string `json:"origin"`
}
type AICapabilityCall struct {
	ID        string          `json:"id"`
	Arguments json.RawMessage `json:"arguments"`
}
type AIRuntimeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type AICapabilityResult struct {
	Index  int              `json:"index"`
	Call   AICapabilityCall `json:"call"`
	Output json.RawMessage  `json:"output,omitempty"`
	Error  *AIRuntimeError  `json:"error,omitempty"`
}

// AIRuntimeInvocation is START for a new execution and RESUME when Core sends
// back ordered capability results. State is opaque Plugin-owned JSON echoed by
// Core; Plugins need no reverse RPC or Core object reference.
type AIRuntimeInvocation struct {
	Phase        string               `json:"phase"`
	ExecutionID  string               `json:"executionId"`
	Request      *AIRuntimeRequest    `json:"request,omitempty"`
	Capabilities []AICapability       `json:"capabilities,omitempty"`
	State        json.RawMessage      `json:"state,omitempty"`
	Results      []AICapabilityResult `json:"results,omitempty"`
}

type AIUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
}

type AIRuntimeResponse struct {
	Status   string             `json:"status"`
	State    json.RawMessage    `json:"state,omitempty"`
	Calls    []AICapabilityCall `json:"calls,omitempty"`
	Output   json.RawMessage    `json:"output,omitempty"`
	Usage    *AIUsage           `json:"usage,omitempty"`
	Metadata map[string]string  `json:"metadata,omitempty"`
}
