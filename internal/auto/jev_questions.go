package auto

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

func criteriaOf(q JevQuestion) (map[string]string, []string, error) {
	raw, err := json.Marshal(q.Criteria)
	if err != nil {
		return nil, nil, err
	}
	var choices map[string]string
	var levels []string
	switch q.Type {
	case "choice":
		if err := json.Unmarshal(raw, &choices); err != nil || len(choices) < 2 || len(choices) > 64 {
			return nil, nil, errors.New("choice requires 2–64 described options")
		}
		for id, text := range choices {
			if id == "" || strings.TrimSpace(text) == "" {
				return nil, nil, errors.New("choice options need IDs and descriptions")
			}
		}
	case "score":
		if err := json.Unmarshal(raw, &levels); err != nil || len(levels) < 2 || len(levels) > 10 {
			return nil, nil, errors.New("score requires 2–10 described levels")
		}
		for _, text := range levels {
			if strings.TrimSpace(text) == "" {
				return nil, nil, errors.New("score levels need descriptions")
			}
		}
	case "noul":
		if q.Criteria != nil {
			return nil, nil, errors.New("this endpoint's noul uses its single instruction")
		}
	default:
		return nil, nil, errors.New("unsupported Jev question type")
	}
	return choices, levels, nil
}

func validateQuestion(id string, q JevQuestion) error {
	if id == "" || strings.TrimSpace(q.Instructions) == "" {
		return errors.New("atomic questions need an ID and a specific instruction")
	}
	_, _, err := criteriaOf(q)
	return err
}

type PrimitiveAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Score         *float64           `json:"score,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

func finiteRange(value *float64, max float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= max
}

func validateAnswer(q JevQuestion, raw json.RawMessage) error {
	var answer PrimitiveAnswer
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Type != q.Type {
		return errors.New("missing or mismatched primitive answer")
	}
	choices, levels, err := criteriaOf(q)
	if err != nil {
		return err
	}
	switch q.Type {
	case "noul":
		if !finiteRange(answer.Noul, 1) {
			return errors.New("missing or invalid noul")
		}
		return nil
	case "choice":
		if _, ok := choices[answer.Choice]; !ok {
			return errors.New("choice was not offered")
		}
	case "score":
		if !finiteRange(answer.Score, float64(len(levels)-1)) {
			return errors.New("missing or invalid score")
		}
	}
	if !finiteRange(answer.Confidence, 1) {
		return errors.New("missing or invalid confidence")
	}
	keys := make([]string, 0, len(choices)+len(levels))
	for key := range choices {
		keys = append(keys, key)
	}
	for i := range levels {
		keys = append(keys, fmt.Sprint(i))
	}
	for _, key := range keys {
		value, ok := answer.Probabilities[key]
		if !ok || !finiteRange(&value, 1) {
			return errors.New("missing or invalid answer probability")
		}
	}
	return nil
}
