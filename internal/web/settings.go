package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type modelMapping struct {
	PublicModel           string `json:"publicModel"`
	UpstreamTone          string `json:"upstreamTone"`
	DisplayName           string `json:"displayName"`
	DefaultReasoningLevel string `json:"defaultReasoningLevel"`
}

var defaultModelMappings = []modelMapping{
	{PublicModel: "gpt-5.6-sol", UpstreamTone: "Gpt_5_6_Reasoning", DisplayName: "GPT-5.6-Sol", DefaultReasoningLevel: "low"},
	{PublicModel: "gpt-5.6-terra", UpstreamTone: "Gpt_5_6_Reasoning", DisplayName: "GPT-5.6-Terra", DefaultReasoningLevel: "medium"},
	{PublicModel: "gpt-5.6-luna", UpstreamTone: "Gpt_5_6_Reasoning", DisplayName: "GPT-5.6-Luna", DefaultReasoningLevel: "medium"},
}

var publicModelID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

var configurableCodexModels = []string{
	"gpt-5.2",
	"gpt-5.4",
	"gpt-5.4-mini",
	"gpt-5.5",
	"gpt-5.6-sol",
	"gpt-5.6-terra",
	"gpt-5.6-luna",
	"codex-auto-review",
}

type runtimeSettings struct {
	MaxToolCallsPerTurn int            `json:"maxToolCallsPerTurn"`
	MaxToolRounds       int            `json:"maxToolRounds"`
	ContextWindow       int            `json:"contextWindow"`
	MaxOutputTokens     int            `json:"maxOutputTokens"`
	ChatTimeoutSeconds  int            `json:"chatTimeoutSeconds"`
	ImageTimeoutSeconds int            `json:"imageTimeoutSeconds"`
	LogLevel            string         `json:"logLevel"`
	DebugLogPath        string         `json:"debugLogPath"`
	ListenAddress       string         `json:"listenAddress"`
	ConfigPath          string         `json:"configPath"`
	TokenCachePath      string         `json:"tokenCachePath"`
	SessionCachePath    string         `json:"sessionCachePath"`
	ClientID            string         `json:"clientId"`
	Authority           string         `json:"authority"`
	RedirectURI         string         `json:"redirectUri"`
	Scope               string         `json:"scope"`
	ModelMappings       []modelMapping `json:"modelMappings"`
	ToolPlanningMode    string         `json:"toolPlanningMode"`
}

type settingsStore struct {
	mu   sync.RWMutex
	path string
	v    runtimeSettings
}

func envInt(name string, fallback int) int {
	n, e := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if e == nil && n > 0 {
		return n
	}
	return fallback
}
func defaultRuntimeSettings() runtimeSettings {
	return runtimeSettings{
		MaxToolCallsPerTurn: envInt("M365_MAX_TOOL_CALLS_PER_TURN", 1), MaxToolRounds: envInt("M365_MAX_TOOL_ROUNDS", 16),
		ContextWindow: envInt("M365_CONTEXT_WINDOW", 128000), MaxOutputTokens: envInt("M365_MAX_OUTPUT_TOKENS", 16384),
		ChatTimeoutSeconds: envInt("M365_CHAT_TIMEOUT_SECONDS", 120), ImageTimeoutSeconds: envInt("M365_IMAGE_TIMEOUT_SECONDS", 150), LogLevel: firstNonEmptySetting(os.Getenv("M365_LOG_LEVEL"), "info"),
		DebugLogPath: os.Getenv("M365_DEBUG_LOG"), ListenAddress: os.Getenv("M365_LISTEN"), ConfigPath: os.Getenv("M365_CONFIG"),
		TokenCachePath: os.Getenv("M365_TOKEN_CACHE"), SessionCachePath: os.Getenv("M365_SESSION_CACHE"), ClientID: os.Getenv("M365_CLIENT_ID"),
		Authority: os.Getenv("M365_AUTHORITY"), RedirectURI: os.Getenv("M365_REDIRECT_URI"), Scope: os.Getenv("M365_SCOPE"),
		ModelMappings:    append([]modelMapping(nil), defaultModelMappings...),
		ToolPlanningMode: toolPlanningMode(os.Getenv("M365_TOOL_PLANNING_MODE")),
	}
}
func settingsPath() string {
	if p := strings.TrimSpace(os.Getenv("M365_SETTINGS_FILE")); p != "" {
		return p
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "m365-native", "settings.json")
}

var sharedSettings *settingsStore

func openSettingsStore() *settingsStore {
	if sharedSettings != nil {
		return sharedSettings
	}
	s := &settingsStore{path: settingsPath(), v: defaultRuntimeSettings()}
	if b, e := os.ReadFile(s.path); e == nil {
		_ = json.Unmarshal(b, &s.v)
	}
	_ = validateSettings(s.v)
	sharedSettings = s
	return s
}
func firstNonEmptySetting(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func validateSettings(v runtimeSettings) error {
	if v.MaxToolCallsPerTurn < 1 || v.MaxToolCallsPerTurn > 64 {
		return fmt.Errorf("max tool calls per turn must be 1-64")
	}
	if v.MaxToolRounds < 1 || v.MaxToolRounds > 512 {
		return fmt.Errorf("max tool rounds must be 1-512")
	}
	if v.ContextWindow < 1024 {
		return fmt.Errorf("context window must be at least 1024")
	}
	if v.MaxOutputTokens < 1 || v.MaxOutputTokens >= v.ContextWindow {
		return fmt.Errorf("max output tokens must be > 0 and < context window")
	}
	if v.ChatTimeoutSeconds < 5 || v.ChatTimeoutSeconds > 3600 {
		return fmt.Errorf("chat timeout must be 5-3600 seconds")
	}
	if v.ImageTimeoutSeconds < 5 || v.ImageTimeoutSeconds > 3600 {
		return fmt.Errorf("image timeout must be 5-3600 seconds")
	}
	if v.LogLevel != "silent" && v.LogLevel != "error" && v.LogLevel != "warn" && v.LogLevel != "info" && v.LogLevel != "debug" {
		return fmt.Errorf("log level must be one of: silent, error, warn, info, debug")
	}
	seen := make(map[string]struct{}, len(v.ModelMappings))
	for _, mapping := range v.ModelMappings {
		model := strings.TrimSpace(mapping.PublicModel)
		if !publicModelID.MatchString(model) {
			return fmt.Errorf("public model ID must contain only letters, digits, dots, underscores, or hyphens, and be 1-128 characters")
		}
		key := strings.ToLower(model)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate public model ID %q", model)
		}
		seen[key] = struct{}{}
		if !validUpstreamTone(strings.TrimSpace(mapping.UpstreamTone)) {
			return fmt.Errorf("unsupported upstream tone %q", mapping.UpstreamTone)
		}
		if strings.TrimSpace(mapping.DisplayName) == "" {
			return fmt.Errorf("public model %q is missing a display name", model)
		}
		if _, err := normalizeReasoningEffort(mapping.DefaultReasoningLevel); err != nil || strings.TrimSpace(mapping.DefaultReasoningLevel) == "" {
			return fmt.Errorf("invalid default reasoning level for public model %q", model)
		}
	}
	return nil
}
func (s *settingsStore) get() runtimeSettings { s.mu.RLock(); defer s.mu.RUnlock(); return s.v }
func (s *settingsStore) save(v runtimeSettings) error {
	if e := validateSettings(v); e != nil {
		return e
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	if e := os.MkdirAll(filepath.Dir(s.path), 0700); e != nil {
		return e
	}
	if e := os.WriteFile(s.path, b, 0600); e != nil {
		return e
	}
	s.mu.Lock()
	s.v = v
	s.mu.Unlock()
	return nil
}
func (s *Server) adminSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonOut(w, map[string]any{"settings": s.settings.get(), "codexModels": configurableCodexModels, "upstreamTones": knownUpstreamTones(), "restartRequiredFields": []string{"listenAddress", "configPath", "tokenCachePath", "sessionCachePath", "clientId", "authority", "redirectUri", "scope", "debugLogPath"}})
	case http.MethodPut:
		var v runtimeSettings
		if json.NewDecoder(r.Body).Decode(&v) != nil {
			writeOpenAIError(w, 400, "invalid_request_error", "bad json")
			return
		}
		if e := s.settings.save(v); e != nil {
			writeOpenAIError(w, 400, "invalid_request_error", e.Error())
			return
		}
		jsonOut(w, map[string]any{"ok": true, "settings": v})
	default:
		writeOpenAIError(w, 405, "invalid_request_error", "method not allowed")
	}
}
func configuredToolCallLimit(s *settingsStore) int {
	if raw, ok := os.LookupEnv("M365_MAX_TOOL_CALLS_PER_TURN"); ok {
		if n, e := strconv.Atoi(strings.TrimSpace(raw)); e == nil && n >= 1 && n <= 64 {
			return n
		}
		return 1
	}
	return s.get().MaxToolCallsPerTurn
}
func limitToolCalls(c []detectedToolCall, n int) []detectedToolCall {
	if n < 1 {
		n = 1
	}
	if len(c) > n {
		return c[:n]
	}
	return c
}

func currentSettings() runtimeSettings { return openSettingsStore().get() }

// ApplyStartupSettingsEnv loads persisted restart-required fields before the
// rest of the application initializes. Explicit process environment variables
// always win over values saved from the web console.
func ApplyStartupSettingsEnv() {
	s := openSettingsStore().get()
	values := map[string]string{"M365_LISTEN": s.ListenAddress, "M365_CONFIG": s.ConfigPath, "M365_TOKEN_CACHE": s.TokenCachePath, "M365_SESSION_CACHE": s.SessionCachePath, "M365_CLIENT_ID": s.ClientID, "M365_AUTHORITY": s.Authority, "M365_REDIRECT_URI": s.RedirectURI, "M365_SCOPE": s.Scope, "M365_DEBUG_LOG": s.DebugLogPath}
	for k, v := range values {
		if _, exists := os.LookupEnv(k); !exists && strings.TrimSpace(v) != "" {
			_ = os.Setenv(k, v)
		}
	}
}
