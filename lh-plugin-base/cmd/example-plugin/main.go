package main

import (
	"fmt"
	"log"
	"os"

	"lh-plugin-base/protocol"
	pluginruntime "lh-plugin-base/runtime"
)

const (
	extensionID      = "lh-plugin-base"
	extensionVersion = "0.1.0"
	sourceFetch      = "source.fetch"
	readQuery        = "read.query"
)

func main() {
	logger := log.New(os.Stderr, extensionID+": ", 0)
	runtime, err := newRuntime(logger)
	if err == nil {
		err = runtime.Serve(os.Stdin, os.Stdout)
	}
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%s: %v\n", extensionID, err)
		os.Exit(1)
	}
}

func newRuntime(logger *log.Logger) (*pluginruntime.Runtime, error) {
	runtime, err := pluginruntime.New(pluginruntime.Metadata{ID: extensionID, Version: extensionVersion}, logger)
	if err != nil {
		return nil, err
	}
	if err := runtime.Register(sourceFetch, handleSourceFetch); err != nil {
		return nil, err
	}
	if err := runtime.Register(readQuery, handleReadQuery); err != nil {
		return nil, err
	}
	return runtime, nil
}

func handleSourceFetch(params protocol.CapabilityInvokeParams) protocol.CapabilityResult {
	var input protocol.SourceFetchRequest
	if err := protocol.Decode(params.Input, &input); err != nil {
		return protocol.Failure("invalid_source_fetch", err.Error())
	}
	cursor := "v1"
	if input.Cursor != nil {
		if *input.Cursor != cursor {
			return protocol.Failure("cursor_not_supported", "unsupported cursor")
		}
		return protocol.Success(protocol.SourceFetchResult{Records: []protocol.SourceRecord{}, NextCursor: &cursor})
	}
	return protocol.Success(protocol.SourceFetchResult{
		Records: []protocol.SourceRecord{{
			ExternalID: "base-item-1",
			SourceKind: "lm.base.item",
			ObservedAt: "2026-09-17T00:00:00Z",
			Payload:    []byte(`{"status":"ready","title":"Lum Harbor protocol sample","version":1}`),
		}},
		NextCursor: &cursor,
	})
}

func handleReadQuery(params protocol.CapabilityInvokeParams) protocol.CapabilityResult {
	var input protocol.ReadQueryRequest
	if err := protocol.Decode(params.Input, &input); err != nil {
		return protocol.Failure("invalid_read_query", err.Error())
	}
	return protocol.Success(protocol.ReadQueryResult{
		Summary: fmt.Sprintf("lh-plugin-base example answered %q", input.Query),
		Facts: []protocol.ReadFact{
			{Name: "instanceId", Value: params.InstanceID},
			{Name: "query", Value: input.Query},
		},
	})
}
