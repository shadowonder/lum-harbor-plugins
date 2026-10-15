package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"lh-plugin-base/protocol"
)

type requestEnvelope = protocol.Request
type responseEnvelope = protocol.Response
type handshake = protocol.Handshake

const protocolVersion = protocol.Version

var requireEOF = protocol.RequireEOF

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
		ID              string `json:"id"`
		Description     string `json:"description,omitempty"`
		InputSemantics  string `json:"inputSemantics,omitempty"`
		ResultSemantics string `json:"resultSemantics,omitempty"`
	} `json:"capabilities"`
	Bindings []struct {
		ID                     string `json:"id"`
		Type                   string `json:"type"`
		Capability             string `json:"capability"`
		DefaultIntervalSeconds int    `json:"defaultIntervalSeconds"`
	} `json:"bindings,omitempty"`
	Configuration struct {
		Version int               `json:"version"`
		Fields  []json.RawMessage `json:"fields,omitempty"`
	} `json:"configuration"`
}

func TestManifestDeclaresSyntheticProjectArtifact(t *testing.T) {
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
	if manifest.Name != "Sample Synthetic Project" || manifest.Executable != "lh-plugin-synthetic-project" {
		t.Fatalf("invalid name/executable: %+v", manifest)
	}
	if len(manifest.Capabilities) != 2 || manifest.Capabilities[0].ID != sourceFetch || len(manifest.Bindings) != 1 {
		t.Fatalf("manifest is not source.fetch + read.query: %+v", manifest)
	}
	readCapability := manifest.Capabilities[1]
	if readCapability.ID != readQuery || readCapability.Description == "" || readCapability.InputSemantics == "" || readCapability.ResultSemantics == "" {
		t.Fatalf("manifest read.query capability missing semantic metadata: %+v", readCapability)
	}
	binding := manifest.Bindings[0]
	if binding.ID != "poll" || binding.Type != "SCHEDULE" || binding.Capability != sourceFetch || binding.DefaultIntervalSeconds != 60 {
		t.Fatalf("manifest schedule binding: %+v", binding)
	}
}

func TestServerFetchInitialAndEvolutionWithDedupeHash(t *testing.T) {
	tempDataDir := t.TempDir()
	var diag bytes.Buffer
	logger := log.New(&diag, "test: ", 0)

	server := NewServer(logger, tempDataDir)

	// Fetch 1: initial fetch (cursor is nil)
	fetchParams := capabilityInvokeParams{
		CapabilityID: sourceFetch,
		InstanceID:   "test-instance-1",
		Config:       json.RawMessage(`{"tick_advance":true}`),
		Input:        mustJSON(t, sourceFetchRequest{Cursor: nil}),
	}
	resp1 := server.handleSourceFetch(fetchParams)
	if resp1.Error != nil {
		t.Fatalf("fetch 1 failed: %+v", resp1.Error)
	}
	var res1 sourceFetchResult
	if err := json.Unmarshal(resp1.Result, &res1); err != nil {
		t.Fatal(err)
	}
	if len(res1.Records) != 3 {
		t.Fatalf("expected 3 initial records in workflow-001, got %d", len(res1.Records))
	}
	if res1.NextCursor == nil || *res1.NextCursor != "tick-0" {
		t.Fatalf("expected cursor tick-0, got %v", res1.NextCursor)
	}

	// Compute initial hashes
	hashes1 := make(map[string]string)
	for _, rec := range res1.Records {
		h := sha256.Sum256(rec.Payload)
		hashes1[rec.ExternalID] = hex.EncodeToString(h[:])
	}

	// Fetch 2: same cursor, but tick_advance: false
	fetchParamsNoAdvance := capabilityInvokeParams{
		CapabilityID: sourceFetch,
		InstanceID:   "test-instance-1",
		Config:       json.RawMessage(`{"tick_advance":false}`),
		Input:        mustJSON(t, sourceFetchRequest{Cursor: res1.NextCursor}),
	}
	resp2 := server.handleSourceFetch(fetchParamsNoAdvance)
	if resp2.Error != nil {
		t.Fatalf("fetch 2 failed: %+v", resp2.Error)
	}
	var res2 sourceFetchResult
	if err := json.Unmarshal(resp2.Result, &res2); err != nil {
		t.Fatal(err)
	}
	for _, rec := range res2.Records {
		h := sha256.Sum256(rec.Payload)
		oldHash := hashes1[rec.ExternalID]
		if hex.EncodeToString(h[:]) != oldHash {
			t.Fatalf("record %s payload hash changed without tick advance", rec.ExternalID)
		}
	}

	// Fetch 3: subsequent fetch with cursor and tick_advance: true
	fetchParamsAdvance := capabilityInvokeParams{
		CapabilityID: sourceFetch,
		InstanceID:   "test-instance-1",
		Config:       json.RawMessage(`{"tick_advance":true}`),
		Input:        mustJSON(t, sourceFetchRequest{Cursor: res1.NextCursor}),
	}
	resp3 := server.handleSourceFetch(fetchParamsAdvance)
	if resp3.Error != nil {
		t.Fatalf("fetch 3 failed: %+v", resp3.Error)
	}
	var res3 sourceFetchResult
	if err := json.Unmarshal(resp3.Result, &res3); err != nil {
		t.Fatal(err)
	}
	if len(res3.Records) != 5 { // 3 + 2 new stories
		t.Fatalf("expected 5 records at tick 1, got %d", len(res3.Records))
	}
	if res3.NextCursor == nil || *res3.NextCursor != "tick-1" {
		t.Fatalf("expected cursor tick-1, got %v", res3.NextCursor)
	}

	hashes3 := make(map[string]string)
	for _, rec := range res3.Records {
		h := sha256.Sum256(rec.Payload)
		hashes3[rec.ExternalID] = hex.EncodeToString(h[:])
	}

	// PROOF: story-002 and story-003 CHANGED hash (mutated in tick 1)
	if hashes3["workflow-001/story-002"] == hashes1["workflow-001/story-002"] {
		t.Fatalf("expected workflow-001/story-002 hash to change after advance")
	}

	// PROOF: story-001 DID NOT change hash (unchanged, Core dedupe preserved)
	if hashes3["workflow-001/story-001"] != hashes1["workflow-001/story-001"] {
		t.Fatalf("expected workflow-001/story-001 hash to remain identical across ticks for dedupe")
	}

	// PROOF: new stories workflow-001/story-004 and story-005 exist
	if _, ok := hashes3["workflow-001/story-004"]; !ok {
		t.Fatalf("expected new story workflow-001/story-004 to be present")
	}
	if _, ok := hashes3["workflow-001/story-005"]; !ok {
		t.Fatalf("expected new story workflow-001/story-005 to be present")
	}
}

func TestServerWorkflowRolloverAndAccumulation(t *testing.T) {
	tempDataDir := t.TempDir()
	var diag bytes.Buffer
	logger := log.New(&diag, "test: ", 0)

	server := NewServer(logger, tempDataDir)

	cursor := "tick-0"
	// Advance several ticks until workflow-001 completes (12 stories cap)
	for i := 0; i < 7; i++ {
		resp := server.handleSourceFetch(capabilityInvokeParams{
			CapabilityID: sourceFetch,
			InstanceID:   "test-instance-1",
			Config:       json.RawMessage(`{"tick_advance":true}`),
			Input:        mustJSON(t, sourceFetchRequest{Cursor: &cursor}),
		})
		if resp.Error != nil {
			t.Fatalf("fetch at tick %d failed: %+v", i+1, resp.Error)
		}
		var res sourceFetchResult
		if err := json.Unmarshal(resp.Result, &res); err != nil {
			t.Fatal(err)
		}
		cursor = *res.NextCursor
	}

	server.mu.Lock()
	defer server.mu.Unlock()

	if len(server.state.Workflows) < 2 {
		t.Fatalf("expected at least 2 workflows after rollover, got %d", len(server.state.Workflows))
	}
	wf1 := server.state.Workflows[0]
	if wf1.ID != "workflow-001" || wf1.Status != "COMPLETED" || wf1.StoryCount != 12 {
		t.Fatalf("expected workflow-001 to be COMPLETED with 12 stories, got: %+v", wf1)
	}

	wf2 := server.state.Workflows[1]
	if wf2.ID != "workflow-002" || wf2.Status != "ACTIVE" || wf2.StoryCount == 0 {
		t.Fatalf("expected workflow-002 to be ACTIVE with >0 stories, got: %+v", wf2)
	}
}

func TestServerStatePersistenceAcrossColdRestart(t *testing.T) {
	tempDataDir := t.TempDir()
	var diag bytes.Buffer
	logger := log.New(&diag, "test: ", 0)

	server1 := NewServer(logger, tempDataDir)
	cursor := "tick-0"
	for i := 0; i < 3; i++ {
		resp := server1.handleSourceFetch(capabilityInvokeParams{
			CapabilityID: sourceFetch,
			InstanceID:   "test-instance-1",
			Config:       json.RawMessage(`{"tick_advance":true}`),
			Input:        mustJSON(t, sourceFetchRequest{Cursor: &cursor}),
		})
		if resp.Error != nil {
			t.Fatal(resp.Error)
		}
		var res sourceFetchResult
		_ = json.Unmarshal(resp.Result, &res)
		cursor = *res.NextCursor
	}

	// Verify file was written
	stateFile := filepath.Join(tempDataDir, "simulation.json")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("expected simulation.json to exist: %v", err)
	}

	// Cold restart: initialize server2 on same directory
	server2 := NewServer(logger, tempDataDir)
	server2.mu.Lock()
	defer server2.mu.Unlock()

	if server2.state.Tick != 3 {
		t.Fatalf("expected reloaded tick=3, got %d", server2.state.Tick)
	}
	if len(server2.state.Stories) != 9 { // 3 + 2*3 = 9
		t.Fatalf("expected reloaded 9 stories, got %d", len(server2.state.Stories))
	}
	if server2.state.ActiveWorkflow != "workflow-001" {
		t.Fatalf("expected activeWorkflow workflow-001, got %s", server2.state.ActiveWorkflow)
	}
}

func TestServerReadQueryAllCommands(t *testing.T) {
	tempDataDir := t.TempDir()
	var diag bytes.Buffer
	logger := log.New(&diag, "test: ", 0)

	server := NewServer(logger, tempDataDir)

	// Advance one tick to produce a BLOCKED story
	cursor := "tick-0"
	server.handleSourceFetch(capabilityInvokeParams{
		CapabilityID: sourceFetch,
		InstanceID:   "test-inst",
		Config:       json.RawMessage(`{"tick_advance":true}`),
		Input:        mustJSON(t, sourceFetchRequest{Cursor: &cursor}),
	})

	// 1. get_story
	resp1 := server.handleReadQuery(capabilityInvokeParams{
		CapabilityID: readQuery,
		InstanceID:   "test-instance",
		Input:        mustJSON(t, readQueryRequest{Query: "get_story:workflow-001/story-001"}),
	})
	if resp1.Error != nil {
		t.Fatalf("read query get_story failed: %+v", resp1.Error)
	}
	var res1 readQueryResult
	if err := json.Unmarshal(resp1.Result, &res1); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains([]byte(res1.Summary), []byte("workflow-001/story-001")) || len(res1.Facts) < 5 {
		t.Fatalf("unexpected get_story result: %+v", res1)
	}

	// 2. get_workflow
	resp2 := server.handleReadQuery(capabilityInvokeParams{
		CapabilityID: readQuery,
		InstanceID:   "test-instance",
		Input:        mustJSON(t, readQueryRequest{Query: "get_workflow:workflow-001"}),
	})
	if resp2.Error != nil {
		t.Fatalf("read query get_workflow failed: %+v", resp2.Error)
	}
	var res2 readQueryResult
	if err := json.Unmarshal(resp2.Result, &res2); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains([]byte(res2.Summary), []byte("workflow-001")) || len(res2.Facts) < 4 {
		t.Fatalf("unexpected get_workflow result: %+v", res2)
	}

	// 3. list_active
	resp3 := server.handleReadQuery(capabilityInvokeParams{
		CapabilityID: readQuery,
		InstanceID:   "test-instance",
		Input:        mustJSON(t, readQueryRequest{Query: "list_active"}),
	})
	if resp3.Error != nil {
		t.Fatalf("read query list_active failed: %+v", resp3.Error)
	}
	var res3 readQueryResult
	if err := json.Unmarshal(resp3.Result, &res3); err != nil {
		t.Fatal(err)
	}
	if len(res3.Facts) == 0 {
		t.Fatalf("expected active stories, got none")
	}

	// 4. list_blocked
	resp4 := server.handleReadQuery(capabilityInvokeParams{
		CapabilityID: readQuery,
		InstanceID:   "test-instance",
		Input:        mustJSON(t, readQueryRequest{Query: "list_blocked"}),
	})
	if resp4.Error != nil {
		t.Fatalf("read query list_blocked failed: %+v", resp4.Error)
	}
	var res4 readQueryResult
	if err := json.Unmarshal(resp4.Result, &res4); err != nil {
		t.Fatal(err)
	}
	// At tick 1, story-003 with CRITICAL priority transitions to BLOCKED
	if len(res4.Facts) == 0 {
		t.Fatalf("expected blocked story at tick 1, got 0 facts")
	}

	// 5. search_stories
	resp5 := server.handleReadQuery(capabilityInvokeParams{
		CapabilityID: readQuery,
		InstanceID:   "test-instance",
		Input:        mustJSON(t, readQueryRequest{Query: "search_stories:logging"}),
	})
	if resp5.Error != nil {
		t.Fatalf("read query search_stories failed: %+v", resp5.Error)
	}
	var res5 readQueryResult
	if err := json.Unmarshal(resp5.Result, &res5); err != nil {
		t.Fatal(err)
	}
	if len(res5.Facts) != 1 {
		t.Fatalf("search_stories:logging expected 1 match, got: %+v", res5)
	}

	// 6. overview
	resp6 := server.handleReadQuery(capabilityInvokeParams{
		CapabilityID: readQuery,
		InstanceID:   "test-instance",
		Input:        mustJSON(t, readQueryRequest{Query: "overview"}),
	})
	if resp6.Error != nil {
		t.Fatalf("read query overview failed: %+v", resp6.Error)
	}
	var res6 readQueryResult
	if err := json.Unmarshal(resp6.Result, &res6); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains([]byte(res6.Summary), []byte("Synthetic Project Simulator")) || len(res6.Facts) < 5 {
		t.Fatalf("unexpected overview result: %+v", res6)
	}
}

func TestSubprocessProtocolHandshakeAndShutdown(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "lh-plugin-synthetic-project")
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
	decoder := json.NewDecoder(bufio.NewReader(stdout))

	// 1. Handshake
	request(t, encoder, requestEnvelope{
		ID:     "handshake-1",
		Method: "system.handshake",
		Params: mustJSON(t, handshake{
			ProtocolVersion:  1,
			ExtensionID:      extensionID,
			ExtensionVersion: extensionVersion,
			Capabilities:     []string{readQuery, sourceFetch},
		}),
	})
	handshakeResponse := response(t, decoder)
	if handshakeResponse.ID != "handshake-1" || handshakeResponse.Error != nil {
		t.Fatalf("handshake response: %+v", handshakeResponse)
	}

	// 2. Fetch
	request(t, encoder, requestEnvelope{
		ID:     "fetch-1",
		Method: "capability.invoke",
		Params: mustJSON(t, capabilityInvokeParams{
			CapabilityID: sourceFetch,
			InstanceID:   "proc-test",
			Config:       json.RawMessage(`{}`),
			Input:        mustJSON(t, sourceFetchRequest{Cursor: nil}),
		}),
	})
	fetchResponse := response(t, decoder)
	if fetchResponse.ID != "fetch-1" || fetchResponse.Error != nil {
		t.Fatalf("fetch response: %+v", fetchResponse)
	}
	var fetchRes sourceFetchResult
	decodeResult(t, fetchResponse, &fetchRes)
	if len(fetchRes.Records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(fetchRes.Records))
	}

	// 3. Shutdown
	request(t, encoder, requestEnvelope{
		ID:     "shutdown-1",
		Method: "system.shutdown",
		Params: json.RawMessage(`{}`),
	})
	shutdownResponse := response(t, decoder)
	if shutdownResponse.ID != "shutdown-1" || shutdownResponse.Error != nil {
		t.Fatalf("shutdown response: %+v", shutdownResponse)
	}
	_ = stdin.Close()
	_ = command.Wait()

	if !bytes.Contains(stderr.Bytes(), []byte("started")) || !bytes.Contains(stderr.Bytes(), []byte("shutdown accepted")) {
		t.Fatalf("missing diagnostics on stderr: %s", stderr.String())
	}
}

func request(t *testing.T, encoder *json.Encoder, value requestEnvelope) {
	t.Helper()
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
}

func response(t *testing.T, decoder *json.Decoder) responseEnvelope {
	t.Helper()
	var value responseEnvelope
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
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

func TestServerDesignatedStoryNearTermDeadlinePersistenceAndStability(t *testing.T) {
	tempDataDir := t.TempDir()
	var diag bytes.Buffer
	logger := log.New(&diag, "test: ", 0)

	creationTime := time.Now()
	server1 := NewServer(logger, tempDataDir)

	// 1. Initial fetch: verify designated story gets a near-term deadline (~8 min)
	resp1 := server1.handleSourceFetch(capabilityInvokeParams{
		CapabilityID: sourceFetch,
		InstanceID:   "test-instance",
		Config:       json.RawMessage(`{"tick_advance":false}`),
		Input:        mustJSON(t, sourceFetchRequest{}),
	})
	if resp1.Error != nil {
		t.Fatalf("initial fetch failed: %+v", resp1.Error)
	}
	var res1 sourceFetchResult
	if err := json.Unmarshal(resp1.Result, &res1); err != nil {
		t.Fatal(err)
	}

	var designatedStory Story
	foundDesignated := false
	for _, rec := range res1.Records {
		var st Story
		if err := json.Unmarshal(rec.Payload, &st); err != nil {
			t.Fatal(err)
		}
		if st.ID == "workflow-001/story-002" {
			designatedStory = st
			foundDesignated = true
		} else {
			// Normal stories must not have deadlines
			if st.DueDate != "" || st.DueTime != "" {
				t.Fatalf("normal story %s must not have deadlines, got dueDate=%q dueTime=%q", st.ID, st.DueDate, st.DueTime)
			}
		}
	}
	if !foundDesignated {
		t.Fatal("workflow-001/story-002 not found in initial records")
	}

	if designatedStory.DueDate == "" || designatedStory.DueTime == "" {
		t.Fatalf("expected designated story to have both dueDate and dueTime, got dueDate=%q dueTime=%q", designatedStory.DueDate, designatedStory.DueTime)
	}

	// Verify deadline is near-term: ~5-10 minutes from creation
	combined := fmt.Sprintf("%sT%s:00", designatedStory.DueDate, designatedStory.DueTime)
	deadline, err := time.ParseInLocation("2006-01-02T15:04:05", combined, time.Local)
	if err != nil {
		t.Fatalf("parse combined deadline %q: %v", combined, err)
	}
	diff := deadline.Sub(creationTime)
	if diff < 4*time.Minute || diff > 11*time.Minute {
		t.Fatalf("expected deadline diff ~8 min (between 4 and 11 min), got %v", diff)
	}

	initialDueDate := designatedStory.DueDate
	initialDueTime := designatedStory.DueTime

	// 2. Advance ticks: deadline must remain byte-for-byte stable across ticks
	cursor := "tick-0"
	for tick := 1; tick <= 3; tick++ {
		resp := server1.handleSourceFetch(capabilityInvokeParams{
			CapabilityID: sourceFetch,
			InstanceID:   "test-instance",
			Config:       json.RawMessage(`{"tick_advance":true}`),
			Input:        mustJSON(t, sourceFetchRequest{Cursor: &cursor}),
		})
		if resp.Error != nil {
			t.Fatalf("fetch tick %d failed: %+v", tick, resp.Error)
		}
		var res sourceFetchResult
		_ = json.Unmarshal(resp.Result, &res)
		cursor = *res.NextCursor

		for _, rec := range res.Records {
			if rec.ExternalID == "workflow-001/story-002" {
				var st Story
				_ = json.Unmarshal(rec.Payload, &st)
				if st.DueDate != initialDueDate {
					t.Fatalf("tick %d: dueDate shifted from %q to %q", tick, initialDueDate, st.DueDate)
				}
				if st.DueTime != initialDueTime {
					t.Fatalf("tick %d: dueTime shifted from %q to %q", tick, initialDueTime, st.DueTime)
				}
			}
		}
	}

	// 3. Cold restart: reload state from simulation.json and verify byte-for-byte persistence
	server2 := NewServer(logger, tempDataDir)
	server2.mu.Lock()
	st2, ok := server2.state.Stories["workflow-001/story-002"]
	server2.mu.Unlock()
	if !ok {
		t.Fatal("workflow-001/story-002 missing after reload")
	}
	if st2.DueDate != initialDueDate || st2.DueTime != initialDueTime {
		t.Fatalf("reloaded deadline mismatch: got %q %q, want %q %q", st2.DueDate, st2.DueTime, initialDueDate, initialDueTime)
	}

	// 4. Read query verifies facts include dueDate and dueTime
	queryResp := server2.handleReadQuery(capabilityInvokeParams{
		CapabilityID: readQuery,
		InstanceID:   "test-instance",
		Input:        mustJSON(t, readQueryRequest{Query: "get_story:workflow-001/story-002"}),
	})
	if queryResp.Error != nil {
		t.Fatalf("read query failed: %+v", queryResp.Error)
	}
	var qRes readQueryResult
	_ = json.Unmarshal(queryResp.Result, &qRes)
	foundDateFact, foundTimeFact := false, false
	for _, f := range qRes.Facts {
		if f.Name == "dueDate" && f.Value == initialDueDate {
			foundDateFact = true
		}
		if f.Name == "dueTime" && f.Value == initialDueTime {
			foundTimeFact = true
		}
	}
	if !foundDateFact || !foundTimeFact {
		t.Fatalf("read query facts missing dueDate or dueTime: %+v", qRes.Facts)
	}
}
