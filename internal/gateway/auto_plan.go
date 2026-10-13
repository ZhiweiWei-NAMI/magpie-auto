package gateway

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	autowork "github.com/yetone/magpie/internal/auto"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// Clients snapshot this once per root session; credentials never leave Magpie.
func (s *Server) serveAutoPolicy(w http.ResponseWriter, r *http.Request) {
	config, err := autowork.StoredConfig(filepath.Join(settings.Dir(), "auto"))
	if err != nil {
		writeError(w, provider.Chat, http.StatusServiceUnavailable, "Auto configuration unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		Enabled     bool               `json:"enabled"`
		Routes      []autowork.Route   `json:"routes"`
		Strategy    *autowork.Strategy `json:"strategy"`
		Instruction string             `json:"instruction"`
	}{config.Enabled, config.Routes, config.Strategy, config.StrategyPrompt})
}

// serveAutoPlan returns a decision only. dsh owns sessions, tools and children.
func (s *Server) serveAutoPlan(w http.ResponseWriter, r *http.Request) {
	config, err := autowork.StoredConfig(filepath.Join(settings.Dir(), "auto"))
	if err != nil || !config.Enabled || config.Decider != autowork.JevFree || config.JevKeyFile == "" {
		writeError(w, provider.Chat, http.StatusServiceUnavailable, "Auto requires enabled Jev Free and a Console key file")
		return
	}
	var request autowork.PlanRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&request); err != nil {
		writeError(w, provider.Chat, http.StatusBadRequest, "invalid Auto decision request: "+err.Error())
		return
	}
	result, err := autowork.AskJev(r.Context(), config.JevKeyFile, request)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{"decision": result, "error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}
