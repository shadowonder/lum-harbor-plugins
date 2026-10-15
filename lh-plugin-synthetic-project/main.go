package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pluginenv "lh-plugin-base/env"
	"lh-plugin-base/protocol"
	pluginruntime "lh-plugin-base/runtime"
)

const (
	extensionID      = "lh-plugin-synthetic-project"
	extensionVersion = "0.1.0"
	sourceFetch      = "source.fetch"
	readQuery        = "read.query"
)

type capabilityInvokeParams = protocol.CapabilityInvokeParams
type sourceFetchRequest = protocol.SourceFetchRequest
type sourceFetchResult = protocol.SourceFetchResult
type sourceRecord = protocol.SourceRecord
type readQueryRequest = protocol.ReadQueryRequest
type readFact = protocol.ReadFact
type readQueryResult = protocol.ReadQueryResult

type pluginConfig struct {
	ProjectKey  string `json:"project_key,omitempty"`
	TickAdvance bool   `json:"tick_advance,omitempty"`
}

type WorkflowMeta struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"` // "ACTIVE", "COMPLETED"
	StoryCap    int    `json:"storyCap"`
	StoryCount  int    `json:"storyCount"`
	CreatedAt   string `json:"createdAt"`
	CompletedAt string `json:"completedAt,omitempty"`
}

type Story struct {
	ID            string `json:"id"`
	WorkflowID    string `json:"workflowId"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Status        string `json:"status"` // "NEW", "READY", "IN_PROGRESS", "BLOCKED", "IN_REVIEW", "DONE"
	StoryPoints   int    `json:"storyPoints"`
	Priority      string `json:"priority"` // "LOW", "MEDIUM", "HIGH", "CRITICAL"
	Version       int    `json:"version"`
	UpdatedAt     string `json:"updatedAt"`
	DueDate       string `json:"dueDate,omitempty"`
	DueTime       string `json:"dueTime,omitempty"`
	BlockedReason string `json:"blockedReason,omitempty"`
}

type SimulationState struct {
	Tick           int              `json:"tick"`
	ActiveWorkflow string           `json:"activeWorkflow"`
	Workflows      []WorkflowMeta   `json:"workflows"`
	Stories        map[string]Story `json:"stories"`
}

type Server struct {
	logger  *log.Logger
	dataDir string
	mu      sync.Mutex
	state   *SimulationState
}

const baseSimulationTime = "2026-09-26T12:00:00Z"

var defaultWorkflowCatalog = []struct {
	ID   string
	Name string
}{
	{"workflow-001", "Core Data Layer & Persistence"},
	{"workflow-002", "Runtime Security & Boundary Enforcement"},
	{"workflow-003", "Observability & Telemetry Pipeline"},
	{"workflow-004", "Plugin Lifecycle & Extension Sandboxing"},
	{"workflow-005", "Distributed Consensus & State Replication"},
}

var defaultStoryCatalog = []struct {
	Title       string
	Description string
	Points      int
	Priority    string
}{
	{"Refactor data access layer", "Extract SQLite connection pool into dedicated repository layer", 5, "HIGH"},
	{"Implement structured logging", "Replace stdout logs with JSON logger conforming to runtime contract", 3, "MEDIUM"},
	{"Database migration rollback tooling", "Add automated down-migration safety verification script", 8, "CRITICAL"},
	{"Optimize query cache eviction", "Implement LRU eviction policy for in-memory statement cache", 5, "LOW"},
	{"Deadlock detection in tx manager", "Add cycle detection to mutex graph in long transactions", 8, "CRITICAL"},
	{"Schema validation for incoming events", "Validate event envelopes against JSON Schema v7 draft", 2, "MEDIUM"},
	{"Metrics collection for batch inserts", "Expose insert rate and latency percentiles to telemetry", 3, "LOW"},
	{"Connection leak watchdog", "Log warning when pooled connection is held longer than 30s", 5, "HIGH"},
	{"Async commit queue flush tuning", "Tune buffer flush thresholds under high write concurrency", 8, "HIGH"},
	{"Connection recovery on disconnect", "Auto-reconnect with exponential backoff on TCP RST", 5, "CRITICAL"},
	{"Audit trail retention policy", "Prune historical audit events older than 90 days", 2, "LOW"},
	{"Secondary index tuning on event_logs", "Add composite index on (aggregate_id, sequence_number)", 5, "MEDIUM"},
	{"Graceful shutdown worker draining", "Drain active work queues before closing database pools", 3, "HIGH"},
	{"Query planner hint support", "Support optional index hints in dynamic query builder", 5, "MEDIUM"},
	{"Encryption key rotation hook", "Support zero-downtime key rotation for data at rest", 8, "CRITICAL"},
}

func NewServer(logger *log.Logger, dataDir string) *Server {
	if dataDir == "" {
		dataDir = pluginenv.PluginDataDir()
	}
	s := &Server{
		logger:  logger,
		dataDir: dataDir,
	}
	s.loadOrInitState()
	return s
}

func initialSimulationState(now time.Time) *SimulationState {
	if now.IsZero() {
		now = time.Now()
	}
	localNow := now.Local()
	// Target practical horizon: approximately 5–10 minutes after the story is FIRST CREATED.
	// Using 8 minutes as a stable, deterministic offset from creation.
	deadline := localNow.Add(8 * time.Minute)
	dueDate := deadline.Format("2006-01-02")
	dueTime := deadline.Format("15:04")
	createdAt := now.UTC().Format(time.RFC3339)

	return &SimulationState{
		Tick:           0,
		ActiveWorkflow: "workflow-001",
		Workflows: []WorkflowMeta{
			{
				ID:         "workflow-001",
				Name:       "Core Data Layer & Persistence",
				Status:     "ACTIVE",
				StoryCap:   12,
				StoryCount: 3,
				CreatedAt:  createdAt,
			},
		},
		Stories: map[string]Story{
			"workflow-001/story-001": {
				ID:          "workflow-001/story-001",
				WorkflowID:  "workflow-001",
				Title:       "Refactor data access layer",
				Description: "Extract SQLite connection pool into dedicated repository layer",
				Status:      "READY",
				StoryPoints: 5,
				Priority:    "HIGH",
				Version:     1,
				UpdatedAt:   createdAt,
			},
			"workflow-001/story-002": {
				ID:          "workflow-001/story-002",
				WorkflowID:  "workflow-001",
				Title:       "Implement structured logging",
				Description: fmt.Sprintf("Replace stdout logs with JSON logger conforming to runtime contract. Target completion due by %s %s.", dueDate, dueTime),
				Status:      "NEW",
				StoryPoints: 3,
				Priority:    "MEDIUM",
				Version:     1,
				UpdatedAt:   createdAt,
				DueDate:     dueDate,
				DueTime:     dueTime,
			},
			"workflow-001/story-003": {
				ID:          "workflow-001/story-003",
				WorkflowID:  "workflow-001",
				Title:       "Database migration rollback tooling",
				Description: "Add automated down-migration safety verification script",
				Status:      "IN_PROGRESS",
				StoryPoints: 8,
				Priority:    "CRITICAL",
				Version:     1,
				UpdatedAt:   createdAt,
			},
		},
	}
}

func (s *Server) stateFilePath() string {
	if s.dataDir == "" {
		return ""
	}
	return filepath.Join(s.dataDir, "simulation.json")
}

func (s *Server) loadOrInitState() {
	filePath := s.stateFilePath()
	if filePath != "" {
		if data, err := os.ReadFile(filePath); err == nil {
			var loaded SimulationState
			if err := json.Unmarshal(data, &loaded); err == nil && len(loaded.Stories) > 0 && len(loaded.Workflows) > 0 {
				s.state = &loaded
				s.logger.Printf("loaded simulation state at tick=%d (%d stories, %d workflows)", s.state.Tick, len(s.state.Stories), len(s.state.Workflows))
				return
			}
		}
	}
	s.state = initialSimulationState(time.Now())
	s.saveStateLocked()
}

func (s *Server) saveStateLocked() {
	filePath := s.stateFilePath()
	if filePath == "" || s.state == nil {
		return
	}
	_ = os.MkdirAll(s.dataDir, 0o700)
	if data, err := json.MarshalIndent(s.state, "", "  "); err == nil {
		_ = os.WriteFile(filePath, data, 0o600)
	}
}

func (s *Server) advanceSimulationLocked() {
	s.state.Tick++
	now := time.Now().UTC().Format(time.RFC3339)

	// 1. Locate active workflow
	activeIdx := -1
	for i, wf := range s.state.Workflows {
		if wf.ID == s.state.ActiveWorkflow && wf.Status == "ACTIVE" {
			activeIdx = i
			break
		}
	}
	if activeIdx == -1 {
		if len(s.state.Workflows) > 0 {
			activeIdx = len(s.state.Workflows) - 1
		}
	}

	// 2. Check workflow rollover: if active workflow reached its StoryCap (10-15 stories)
	if activeIdx >= 0 && s.state.Workflows[activeIdx].StoryCount >= s.state.Workflows[activeIdx].StoryCap {
		s.state.Workflows[activeIdx].Status = "COMPLETED"
		s.state.Workflows[activeIdx].CompletedAt = now

		nextIndex := len(s.state.Workflows)
		var nextID, nextName string
		if nextIndex < len(defaultWorkflowCatalog) {
			nextID = defaultWorkflowCatalog[nextIndex].ID
			nextName = defaultWorkflowCatalog[nextIndex].Name
		} else {
			nextID = fmt.Sprintf("workflow-%03d", nextIndex+1)
			nextName = fmt.Sprintf("Synthetic Milestone Project %d", nextIndex+1)
		}
		newWF := WorkflowMeta{
			ID:         nextID,
			Name:       nextName,
			Status:     "ACTIVE",
			StoryCap:   12,
			StoryCount: 0,
			CreatedAt:  now,
		}
		s.state.Workflows = append(s.state.Workflows, newWF)
		s.state.ActiveWorkflow = newWF.ID
		activeIdx = len(s.state.Workflows) - 1
	}

	activeWF := &s.state.Workflows[activeIdx]

	// 3. Create 1 or 2 new stories in active workflow (if below cap)
	newlyCreatedIDs := make(map[string]bool)
	remaining := activeWF.StoryCap - activeWF.StoryCount
	toCreate := 0
	if remaining > 0 {
		toCreate = 2
		if toCreate > remaining {
			toCreate = remaining
		}
	}

	for i := 0; i < toCreate; i++ {
		activeWF.StoryCount++
		storyID := fmt.Sprintf("%s/story-%03d", activeWF.ID, activeWF.StoryCount)
		catIdx := (activeWF.StoryCount - 1 + activeIdx*3) % len(defaultStoryCatalog)
		template := defaultStoryCatalog[catIdx]

		initStatus := "NEW"
		if activeWF.StoryCount%2 == 1 {
			initStatus = "READY"
		}

		var storyDueDate string
		storyDesc := template.Description
		if activeWF.StoryCount%3 == 2 {
			storyDueDate = "2026-10-15"
			storyDesc = fmt.Sprintf("%s Target completion due by %s.", template.Description, storyDueDate)
		}

		s.state.Stories[storyID] = Story{
			ID:          storyID,
			WorkflowID:  activeWF.ID,
			Title:       template.Title,
			Description: storyDesc,
			Status:      initStatus,
			StoryPoints: template.Points,
			Priority:    template.Priority,
			Version:     1,
			UpdatedAt:   now,
			DueDate:     storyDueDate,
		}
		newlyCreatedIDs[storyID] = true
	}

	// 4. Update 1 or 2 existing active stories (transition state, block/unblock, bump version)
	candidateKeys := make([]string, 0, len(s.state.Stories))
	for k, st := range s.state.Stories {
		if newlyCreatedIDs[k] {
			continue
		}
		if st.Status == "DONE" {
			continue
		}
		candidateKeys = append(candidateKeys, k)
	}

	// Sort candidateKeys deterministically
	for i := 0; i < len(candidateKeys); i++ {
		for j := i + 1; j < len(candidateKeys); j++ {
			if candidateKeys[i] > candidateKeys[j] {
				candidateKeys[i], candidateKeys[j] = candidateKeys[j], candidateKeys[i]
			}
		}
	}

	if len(candidateKeys) > 0 {
		// Mutate candidate 1
		idx1 := s.state.Tick % len(candidateKeys)
		mutateStory(&s.state.Stories, candidateKeys[idx1], now, s.state.Tick)

		// Mutate candidate 2 if available
		if len(candidateKeys) > 1 {
			idx2 := (s.state.Tick + 1) % len(candidateKeys)
			if idx2 != idx1 {
				mutateStory(&s.state.Stories, candidateKeys[idx2], now, s.state.Tick+1)
			}
		}
	}

	// 5. All other stories remain UNCHANGED (preserving identical hash for Core dedupe).
	s.saveStateLocked()
	s.logger.Printf("advanced simulation to tick=%d (%d stories, active=%s)", s.state.Tick, len(s.state.Stories), s.state.ActiveWorkflow)
}

func mutateStory(stories *map[string]Story, id string, now string, tick int) {
	st := (*stories)[id]
	switch st.Status {
	case "NEW":
		st.Status = "READY"
	case "READY":
		st.Status = "IN_PROGRESS"
	case "IN_PROGRESS":
		if st.Priority == "CRITICAL" || tick%3 == 0 {
			st.Status = "BLOCKED"
			st.BlockedReason = "Blocked by external API schema review / security approval"
		} else {
			st.Status = "IN_REVIEW"
			st.BlockedReason = ""
		}
	case "BLOCKED":
		st.Status = "IN_PROGRESS"
		st.BlockedReason = ""
	case "IN_REVIEW":
		st.Status = "DONE"
		st.BlockedReason = ""
	}
	st.Version++
	st.UpdatedAt = now
	(*stories)[id] = st
}

func (s *Server) handleSourceFetch(params capabilityInvokeParams) protocol.CapabilityResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	var input sourceFetchRequest
	if len(params.Input) > 0 && string(params.Input) != "null" {
		if err := protocol.Decode(params.Input, &input); err != nil {
			return protocol.Failure("invalid_source_fetch", err.Error())
		}
	}

	var cfg pluginConfig
	if len(params.Config) > 0 && string(params.Config) != "null" {
		_ = json.Unmarshal(params.Config, &cfg)
	}

	// Should we advance on fetch? Default to true unless explicitly disabled
	advance := true
	if len(params.Config) > 0 && bytes.Contains(params.Config, []byte(`"tick_advance":false`)) {
		advance = false
	}

	// If this is not the initial fetch (cursor is present) and advance is true, advance tick
	if input.Cursor != nil && advance {
		s.advanceSimulationLocked()
	}

	cursorStr := fmt.Sprintf("tick-%d", s.state.Tick)

	// Collect all stories in deterministic order
	keys := make([]string, 0, len(s.state.Stories))
	for k := range s.state.Stories {
		keys = append(keys, k)
	}
	// Sort keys
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}

	records := make([]sourceRecord, 0, len(keys))
	for _, key := range keys {
		story := s.state.Stories[key]
		payloadBytes, err := json.Marshal(story)
		if err != nil {
			continue
		}
		records = append(records, sourceRecord{
			ExternalID: story.ID,
			SourceKind: "synthetic.project.story",
			ObservedAt: story.UpdatedAt,
			Payload:    payloadBytes,
		})
	}

	return protocol.Success(sourceFetchResult{
		Records:    records,
		NextCursor: &cursorStr,
	})
}

func (s *Server) handleReadQuery(params capabilityInvokeParams) protocol.CapabilityResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	var input readQueryRequest
	if err := protocol.Decode(params.Input, &input); err != nil {
		return protocol.Failure("invalid_read_query", err.Error())
	}

	query := strings.TrimSpace(input.Query)

	// Query: get_story:<id>, story_id:<id>, or direct story ID match
	targetStoryID := ""
	if strings.HasPrefix(query, "get_story:") {
		targetStoryID = strings.TrimPrefix(query, "get_story:")
	} else if strings.HasPrefix(query, "story_id:") {
		targetStoryID = strings.TrimPrefix(query, "story_id:")
	} else if _, ok := s.state.Stories[query]; ok {
		targetStoryID = query
	}

	if targetStoryID != "" {
		if story, ok := s.state.Stories[targetStoryID]; ok {
			facts := []readFact{
				{Name: "id", Value: story.ID},
				{Name: "workflowId", Value: story.WorkflowID},
				{Name: "title", Value: story.Title},
				{Name: "description", Value: story.Description},
				{Name: "status", Value: story.Status},
				{Name: "storyPoints", Value: fmt.Sprintf("%d", story.StoryPoints)},
				{Name: "priority", Value: story.Priority},
				{Name: "version", Value: fmt.Sprintf("%d", story.Version)},
				{Name: "updatedAt", Value: story.UpdatedAt},
			}
			if story.DueDate != "" {
				facts = append(facts, readFact{Name: "dueDate", Value: story.DueDate})
			}
			if story.DueTime != "" {
				facts = append(facts, readFact{Name: "dueTime", Value: story.DueTime})
			}
			if story.BlockedReason != "" {
				facts = append(facts, readFact{Name: "blockedReason", Value: story.BlockedReason})
			}
			return protocol.Success(readQueryResult{
				Summary: fmt.Sprintf("[%s] %s (%s, %d pts)", story.ID, story.Title, story.Status, story.StoryPoints),
				Facts:   facts,
			})
		}
		return protocol.Success(readQueryResult{
			Summary: fmt.Sprintf("Story not found: %s", targetStoryID),
			Facts:   []readFact{},
		})
	}

	// Query: get_workflow:<id> or workflow_id:<id>
	if strings.HasPrefix(query, "get_workflow:") || strings.HasPrefix(query, "workflow_id:") {
		wfID := strings.TrimPrefix(query, "get_workflow:")
		wfID = strings.TrimPrefix(wfID, "workflow_id:")
		for _, wf := range s.state.Workflows {
			if wf.ID == wfID {
				facts := []readFact{
					{Name: "id", Value: wf.ID},
					{Name: "name", Value: wf.Name},
					{Name: "status", Value: wf.Status},
					{Name: "storyCap", Value: fmt.Sprintf("%d", wf.StoryCap)},
					{Name: "storyCount", Value: fmt.Sprintf("%d", wf.StoryCount)},
					{Name: "createdAt", Value: wf.CreatedAt},
				}
				if wf.CompletedAt != "" {
					facts = append(facts, readFact{Name: "completedAt", Value: wf.CompletedAt})
				}
				return protocol.Success(readQueryResult{
					Summary: fmt.Sprintf("[%s] %s (status: %s, stories: %d/%d)", wf.ID, wf.Name, wf.Status, wf.StoryCount, wf.StoryCap),
					Facts:   facts,
				})
			}
		}
		return protocol.Success(readQueryResult{
			Summary: fmt.Sprintf("Workflow not found: %s", wfID),
			Facts:   []readFact{},
		})
	}

	// Query: list_active
	if query == "list_active" {
		var active []Story
		for _, st := range s.state.Stories {
			if st.Status != "DONE" {
				active = append(active, st)
			}
		}
		for i := 0; i < len(active); i++ {
			for j := i + 1; j < len(active); j++ {
				if active[i].ID > active[j].ID {
					active[i], active[j] = active[j], active[i]
				}
			}
		}
		facts := make([]readFact, 0, len(active))
		for _, st := range active {
			facts = append(facts, readFact{
				Name:  st.ID,
				Value: fmt.Sprintf("%s (%s, %d pts)", st.Title, st.Status, st.StoryPoints),
			})
		}
		return protocol.Success(readQueryResult{
			Summary: fmt.Sprintf("Active synthetic stories (%d items)", len(active)),
			Facts:   facts,
		})
	}

	// Query: list_blocked
	if query == "list_blocked" {
		var blocked []Story
		for _, st := range s.state.Stories {
			if st.Status == "BLOCKED" {
				blocked = append(blocked, st)
			}
		}
		for i := 0; i < len(blocked); i++ {
			for j := i + 1; j < len(blocked); j++ {
				if blocked[i].ID > blocked[j].ID {
					blocked[i], blocked[j] = blocked[j], blocked[i]
				}
			}
		}
		facts := make([]readFact, 0, len(blocked))
		for _, st := range blocked {
			reason := st.BlockedReason
			if reason == "" {
				reason = "Blocked"
			}
			facts = append(facts, readFact{
				Name:  st.ID,
				Value: fmt.Sprintf("%s - %s", st.Title, reason),
			})
		}
		return protocol.Success(readQueryResult{
			Summary: fmt.Sprintf("Blocked synthetic stories (%d items)", len(blocked)),
			Facts:   facts,
		})
	}

	// Query: search:<term> or search_stories:<term>
	if strings.HasPrefix(query, "search:") || strings.HasPrefix(query, "search_stories:") {
		term := strings.TrimPrefix(query, "search:")
		term = strings.TrimPrefix(term, "search_stories:")
		term = strings.ToLower(strings.TrimSpace(term))

		var matched []Story
		for _, st := range s.state.Stories {
			if strings.Contains(strings.ToLower(st.Title), term) ||
				strings.Contains(strings.ToLower(st.Description), term) ||
				strings.Contains(strings.ToLower(st.ID), term) ||
				strings.Contains(strings.ToLower(st.WorkflowID), term) ||
				strings.Contains(strings.ToLower(st.Status), term) {
				matched = append(matched, st)
			}
		}
		for i := 0; i < len(matched); i++ {
			for j := i + 1; j < len(matched); j++ {
				if matched[i].ID > matched[j].ID {
					matched[i], matched[j] = matched[j], matched[i]
				}
			}
		}
		facts := make([]readFact, 0, len(matched))
		for _, st := range matched {
			facts = append(facts, readFact{
				Name:  st.ID,
				Value: fmt.Sprintf("%s (%s)", st.Title, st.Status),
			})
		}
		return protocol.Success(readQueryResult{
			Summary: fmt.Sprintf("Found %d stories matching %q", len(matched), term),
			Facts:   facts,
		})
	}

	// Default fallback: overview
	activeCount := 0
	blockedCount := 0
	for _, st := range s.state.Stories {
		if st.Status != "DONE" {
			activeCount++
		}
		if st.Status == "BLOCKED" {
			blockedCount++
		}
	}
	return protocol.Success(readQueryResult{
		Summary: fmt.Sprintf("Synthetic Project Simulator at tick %d (active workflow: %s)", s.state.Tick, s.state.ActiveWorkflow),
		Facts: []readFact{
			{Name: "tick", Value: fmt.Sprintf("%d", s.state.Tick)},
			{Name: "activeWorkflow", Value: s.state.ActiveWorkflow},
			{Name: "workflowsCount", Value: fmt.Sprintf("%d", len(s.state.Workflows))},
			{Name: "storiesCount", Value: fmt.Sprintf("%d", len(s.state.Stories))},
			{Name: "activeStoriesCount", Value: fmt.Sprintf("%d", activeCount)},
			{Name: "blockedStoriesCount", Value: fmt.Sprintf("%d", blockedCount)},
			{Name: "instanceId", Value: params.InstanceID},
		},
	})
}

func main() {
	if err := run(os.Stdin, os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "lh-plugin-synthetic-project: %v\n", err)
		os.Exit(1)
	}
}

func run(input io.Reader, output io.Writer, diagnostics io.Writer) error {
	logger := log.New(diagnostics, "lh-plugin-synthetic-project: ", 0)
	server := NewServer(logger, pluginenv.PluginDataDir())
	runtime, err := pluginruntime.New(pluginruntime.Metadata{ID: extensionID, Version: extensionVersion}, logger)
	if err != nil {
		return err
	}
	if err := runtime.Register(sourceFetch, server.handleSourceFetch); err != nil {
		return err
	}
	if err := runtime.Register(readQuery, server.handleReadQuery); err != nil {
		return err
	}
	return runtime.Serve(input, output)
}
