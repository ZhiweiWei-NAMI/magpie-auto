package auto

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Strategy is a user-directed policy snapshot, not a claim about model quality.
// Its scores are preferences compiled once when the instruction or routes change.
type Strategy struct {
	Mode         string             `json:"mode"`
	Weights      map[string]float64 `json:"weights"`
	RouteWeights map[string]float64 `json:"routeWeights"`
	InputID      string             `json:"inputId"`
	Decision     PlanResult         `json:"decision"`
}

func strategyInputID(c Config) string {
	raw, _ := json.Marshal(struct {
		Prompt string
		Routes []Route
	}{c.StrategyPrompt, c.Routes})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (m *Manager) Configure(ctx context.Context, c Config) error {
	c.StrategyPrompt = strings.TrimSpace(c.StrategyPrompt)
	if len(c.StrategyPrompt) > 8000 {
		return errors.New("strategy instruction exceeds 8000 bytes")
	}
	if err := validateConfig(normalizeConfig(c)); err != nil {
		return err
	}
	previous := m.Config()
	c.Strategy = nil // A caller cannot install invented Jev scores.
	if c.StrategyPrompt != "" {
		if c.Decider != JevFree || c.JevKeyFile == "" {
			return errors.New("compiling a strategy requires Jev Free and a Console key file")
		}
		if previous.Strategy != nil && previous.Strategy.InputID == strategyInputID(c) {
			c.Strategy = previous.Strategy
		} else {
			strategy, err := compileStrategy(ctx, c, AskJev)
			if err != nil {
				return err
			}
			c.Strategy = &strategy
		}
	}
	return m.SetConfig(c)
}

func compileStrategy(ctx context.Context, c Config, ask func(context.Context, string, PlanRequest) (PlanResult, error)) (Strategy, error) {
	request := PlanRequest{Task: c.StrategyPrompt, Trigger: "configure", Observation: map[string]any{"routes": c.Routes}, Questions: map[string]JevQuestion{}}
	request.Questions["mode"] = JevQuestion{Type: "choice", Instructions: "Which execution policy does the user's configuration instruction request? Classify only explicit preference; use automatic unless the user requests a fixed baseline.", Criteria: map[string]string{
		"automatic": "Use Jev to judge context and delegation proposals within configured limits.",
		"single":    "The user explicitly requires a single agent with native context management and no per-task Jev routing.",
		"native":    "The user explicitly wants the execution model to control native delegation and context without Jev routing.",
	}}
	for _, dimension := range []string{"quality", "latency", "usage", "cache"} {
		request.Questions[dimension] = JevQuestion{Type: "score", Instructions: "How much priority does the user assign to " + dimension + " in the configuration instruction? Judge this dimension alone.", Criteria: []string{
			"The user explicitly accepts trading this objective away.", "The user leaves this objective unspecified or asks for a balance.", "The user explicitly prioritizes this objective.",
		}}
	}
	for i, route := range c.Routes {
		request.Questions[fmt.Sprintf("profile_%d", i)] = JevQuestion{Type: "score", Instructions: "What initial preference does the user express for configured role " + route.ID + " (" + route.Label + ": " + route.Description + ")? This is preference, not measured model capability.", Criteria: []string{
			"The user explicitly discourages this role.", "The user expresses no special preference for this role.", "The user explicitly prefers this role for matching tasks.",
		}}
	}
	decision, err := ask(ctx, c.JevKeyFile, request)
	if err != nil {
		return Strategy{}, err
	}
	var answers map[string]PrimitiveAnswer
	if err := json.Unmarshal(decision.Answers, &answers); err != nil {
		return Strategy{}, err
	}
	for id, q := range request.Questions {
		raw, err := json.Marshal(answers[id])
		if err != nil {
			return Strategy{}, err
		}
		if err := validateAnswer(q, raw); err != nil {
			return Strategy{}, fmt.Errorf("strategy %s: %w", id, err)
		}
	}
	if *answers["mode"].Confidence < 0.5 {
		return Strategy{}, errors.New("strategy instruction is ambiguous about automatic versus fixed execution; make that preference explicit")
	}
	result := Strategy{Mode: answers["mode"].Choice, Weights: map[string]float64{}, RouteWeights: map[string]float64{}, InputID: strategyInputID(c), Decision: decision}
	for _, key := range []string{"quality", "latency", "usage", "cache"} {
		result.Weights[key] = *answers[key].Score / 2
	}
	for i, route := range c.Routes {
		result.RouteWeights[route.ID] = *answers[fmt.Sprintf("profile_%d", i)].Score / 2
	}
	return result, nil
}
