// Package runtime serves the Lum Harbor Plugin Runtime V1 protocol over
// newline-delimited JSON on stdin/stdout.
package runtime

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sort"
	"sync"

	"lh-plugin-base/protocol"
)

const maxFrameBytes = 4 * 1024 * 1024

type Handler func(protocol.CapabilityInvokeParams) protocol.CapabilityResult

type Metadata struct {
	ID      string
	Version string
}

type Runtime struct {
	metadata Metadata
	logger   *log.Logger

	mu       sync.RWMutex
	handlers map[string]Handler
	serving  bool
}

func New(metadata Metadata, logger *log.Logger) (*Runtime, error) {
	if metadata.ID == "" || metadata.Version == "" {
		return nil, fmt.Errorf("plugin id and version must not be empty")
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Runtime{metadata: metadata, logger: logger, handlers: make(map[string]Handler)}, nil
}

func (r *Runtime) Register(capabilityID string, handler Handler) error {
	if capabilityID == "" {
		return fmt.Errorf("capability id must not be empty")
	}
	if handler == nil {
		return fmt.Errorf("capability %q handler must not be nil", capabilityID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.serving {
		return fmt.Errorf("cannot register capability %q after serving starts", capabilityID)
	}
	if _, exists := r.handlers[capabilityID]; exists {
		return fmt.Errorf("capability %q already registered", capabilityID)
	}
	r.handlers[capabilityID] = handler
	return nil
}

// Serve runs until a valid system.shutdown request, stdin EOF, or an I/O
// failure. Stdout receives protocol responses only; diagnostics use logger.
func (r *Runtime) Serve(input io.Reader, output io.Writer) error {
	if input == nil || output == nil {
		return fmt.Errorf("input and output must not be nil")
	}
	r.mu.Lock()
	if r.serving {
		r.mu.Unlock()
		return fmt.Errorf("runtime is already serving")
	}
	r.serving = true
	r.mu.Unlock()

	r.logger.Print("started")
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), maxFrameBytes)
	encoder := json.NewEncoder(output)

	for scanner.Scan() {
		request, err := decodeRequest(scanner.Bytes())
		if err != nil {
			r.logger.Printf("ignored invalid request: %v", err)
			continue
		}
		response, stop := r.handleRequest(request)
		if err := encoder.Encode(response); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
		if stop {
			r.logger.Print("shutdown accepted")
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read request: %w", err)
	}
	r.logger.Print("stdin closed")
	return nil
}

func decodeRequest(line []byte) (protocol.Request, error) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	var request protocol.Request
	if err := decoder.Decode(&request); err != nil {
		return protocol.Request{}, err
	}
	if err := protocol.RequireEOF(decoder); err != nil {
		return protocol.Request{}, err
	}
	if request.ID == "" || request.Method == "" {
		return protocol.Request{}, fmt.Errorf("id and method must not be empty")
	}
	return request, nil
}

func (r *Runtime) handleRequest(request protocol.Request) (protocol.Response, bool) {
	switch request.Method {
	case protocol.MethodHandshake:
		var handshake protocol.Handshake
		if err := protocol.Decode(request.Params, &handshake); err != nil {
			return errorResponse(request.ID, protocol.ErrorInvalidHandshake, err.Error()), false
		}
		capabilities := r.capabilities()
		if handshake.ProtocolVersion != protocol.Version ||
			handshake.ExtensionID != r.metadata.ID ||
			handshake.ExtensionVersion != r.metadata.Version ||
			!equalStrings(handshake.Capabilities, capabilities) {
			return errorResponse(request.ID, protocol.ErrorIncompatibleHandshake, "host handshake does not match this plugin artifact"), false
		}
		return resultResponse(request.ID, protocol.Handshake{
			ProtocolVersion:  protocol.Version,
			ExtensionID:      r.metadata.ID,
			ExtensionVersion: r.metadata.Version,
			Capabilities:     capabilities,
		}), false
	case protocol.MethodCapabilityInvoke:
		return r.handleCapability(request), false
	case protocol.MethodShutdown:
		return resultResponse(request.ID, map[string]bool{"accepted": true}), true
	default:
		return errorResponse(request.ID, protocol.ErrorMethodUnsupported, "unsupported method"), false
	}
}

func (r *Runtime) handleCapability(request protocol.Request) protocol.Response {
	var params protocol.CapabilityInvokeParams
	if err := protocol.Decode(request.Params, &params); err != nil {
		return errorResponse(request.ID, protocol.ErrorInvalidInvocation, err.Error())
	}
	r.mu.RLock()
	handler := r.handlers[params.CapabilityID]
	r.mu.RUnlock()
	if handler == nil {
		return errorResponse(request.ID, protocol.ErrorCapabilityUnsupported, "unsupported capability")
	}
	result := handler(params)
	if result.Error != nil {
		return protocol.Response{ID: request.ID, Error: result.Error}
	}
	if len(result.Result) == 0 {
		return errorResponse(request.ID, protocol.ErrorInternal, "capability handler returned no result")
	}
	return protocol.Response{ID: request.ID, Result: result.Result}
}

func (r *Runtime) capabilities() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	capabilities := make([]string, 0, len(r.handlers))
	for capabilityID := range r.handlers {
		capabilities = append(capabilities, capabilityID)
	}
	sort.Strings(capabilities)
	return capabilities
}

func resultResponse(id string, result any) protocol.Response {
	encoded, err := json.Marshal(result)
	if err != nil {
		return errorResponse(id, protocol.ErrorInternal, "could not encode result")
	}
	return protocol.Response{ID: id, Result: encoded}
}

func errorResponse(id, code, message string) protocol.Response {
	return protocol.Response{ID: id, Error: protocol.NewError(code, message)}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}
