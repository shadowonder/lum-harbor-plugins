package runtime

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"testing"

	"lh-plugin-base/protocol"
)

func TestServeHandshakeDispatchUnsupportedAndShutdown(t *testing.T) {
	var diagnostics bytes.Buffer
	runtime, err := New(Metadata{ID: "test-plugin", Version: "1.2.3"}, log.New(&diagnostics, "test-plugin: ", 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Register("read.query", func(params protocol.CapabilityInvokeParams) protocol.CapabilityResult {
		var request protocol.ReadQueryRequest
		if err := protocol.Decode(params.Input, &request); err != nil {
			return protocol.Failure("invalid_read_query", err.Error())
		}
		return protocol.Success(protocol.ReadQueryResult{Summary: request.Query, Facts: []protocol.ReadFact{}})
	}); err != nil {
		t.Fatal(err)
	}

	requests := []string{
		`not-json`,
		requestJSON(t, protocol.Request{ID: "hs", Method: protocol.MethodHandshake, Params: rawJSON(t, protocol.Handshake{ProtocolVersion: 1, ExtensionID: "test-plugin", ExtensionVersion: "1.2.3", Capabilities: []string{"read.query"}})}),
		requestJSON(t, protocol.Request{ID: "read", Method: protocol.MethodCapabilityInvoke, Params: rawJSON(t, protocol.CapabilityInvokeParams{CapabilityID: "read.query", InstanceID: "instance-1", Input: rawJSON(t, protocol.ReadQueryRequest{Query: "status"})})}),
		requestJSON(t, protocol.Request{ID: "missing", Method: protocol.MethodCapabilityInvoke, Params: rawJSON(t, protocol.CapabilityInvokeParams{CapabilityID: "source.fetch", Input: json.RawMessage(`{}`)})}),
		requestJSON(t, protocol.Request{ID: "stop", Method: protocol.MethodShutdown, Params: json.RawMessage(`{}`)}),
	}
	var output bytes.Buffer
	if err := runtime.Serve(strings.NewReader(strings.Join(requests, "\n")+"\n"), &output); err != nil {
		t.Fatal(err)
	}

	scanner := bufio.NewScanner(&output)
	responses := make([]protocol.Response, 0, 4)
	for scanner.Scan() {
		var response protocol.Response
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if len(responses) != 4 {
		t.Fatalf("responses = %d: %s", len(responses), output.String())
	}
	if responses[0].ID != "hs" || responses[0].Error != nil {
		t.Fatalf("handshake response = %+v", responses[0])
	}
	if responses[1].ID != "read" || responses[1].Error != nil {
		t.Fatalf("invoke response = %+v", responses[1])
	}
	if responses[2].Error == nil || responses[2].Error.Code != protocol.ErrorCapabilityUnsupported {
		t.Fatalf("unsupported response = %+v", responses[2])
	}
	if responses[3].ID != "stop" || responses[3].Error != nil {
		t.Fatalf("shutdown response = %+v", responses[3])
	}
	if !strings.Contains(diagnostics.String(), "ignored invalid request") || !strings.Contains(diagnostics.String(), "shutdown accepted") {
		t.Fatalf("diagnostics = %q", diagnostics.String())
	}
}

func TestServeEOFIsGracefulAndStdoutStaysEmpty(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	runtime, err := New(Metadata{ID: "test-plugin", Version: "1.0.0"}, log.New(&diagnostics, "test-plugin: ", 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Serve(bytes.NewReader(nil), &output); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("stdout = %q", output.String())
	}
	if !strings.Contains(diagnostics.String(), "stdin closed") {
		t.Fatalf("diagnostics = %q", diagnostics.String())
	}
}

func TestRegisterRejectsDuplicates(t *testing.T) {
	runtime, err := New(Metadata{ID: "test-plugin", Version: "1.0.0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := func(protocol.CapabilityInvokeParams) protocol.CapabilityResult {
		return protocol.Success(map[string]bool{"ok": true})
	}
	if err := runtime.Register("read.query", handler); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Register("read.query", handler); err == nil {
		t.Fatal("expected duplicate registration error")
	}
}

func requestJSON(t *testing.T, request protocol.Request) string {
	t.Helper()
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func rawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
