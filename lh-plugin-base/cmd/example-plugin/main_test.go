package main

import (
	"encoding/json"
	"os"
	"testing"

	"lh-plugin-base/protocol"
)

func TestExampleManifestIdentityAndCapabilities(t *testing.T) {
	data, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		Version         string `json:"version"`
		ProtocolVersion int    `json:"protocolVersion"`
		Capabilities    []struct {
			ID string `json:"id"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ID != extensionID || manifest.Name != "Lum Harbor Go Base Example" || manifest.Version != extensionVersion || manifest.ProtocolVersion != protocol.Version || len(manifest.Capabilities) != 2 {
		t.Fatalf("manifest = %+v", manifest)
	}
}

func TestExampleHandlers(t *testing.T) {
	fetch := handleSourceFetch(protocol.CapabilityInvokeParams{Input: json.RawMessage(`{}`)})
	if fetch.Error != nil {
		t.Fatal(fetch.Error)
	}
	var fetchResult protocol.SourceFetchResult
	if err := json.Unmarshal(fetch.Result, &fetchResult); err != nil {
		t.Fatal(err)
	}
	if len(fetchResult.Records) != 1 || fetchResult.NextCursor == nil || *fetchResult.NextCursor != "v1" {
		t.Fatalf("fetch result = %+v", fetchResult)
	}

	read := handleReadQuery(protocol.CapabilityInvokeParams{InstanceID: "example", Input: json.RawMessage(`{"query":"status"}`)})
	if read.Error != nil {
		t.Fatal(read.Error)
	}
	var readResult protocol.ReadQueryResult
	if err := json.Unmarshal(read.Result, &readResult); err != nil {
		t.Fatal(err)
	}
	if len(readResult.Facts) != 2 {
		t.Fatalf("read result = %+v", readResult)
	}
}
