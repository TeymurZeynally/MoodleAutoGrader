package llm

import (
	"encoding/json"
	"fmt"

	"moodle-autograder/internal/domain/assessment"
)

// CheckReply is the model's answer to the checklist call, in the shape CheckSchema asks for.
type CheckReply struct {
	Criteria []assessment.Verdict `json:"criteria"`
	Extras   []assessment.Verdict `json:"extras"`
}

// ParseCheck reads the answer to the checklist call.
func ParseCheck(raw json.RawMessage) (CheckReply, error) {
	var r CheckReply
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, fmt.Errorf("decode checklist reply: %w", err)
	}

	return r, nil
}

// Unquoted lists the criteria and extra tasks the model marked met or partial without any quote; an unreadable answer
// lists none.
func Unquoted(raw json.RawMessage) []string {
	r, err := ParseCheck(raw)
	if err != nil {
		return nil
	}

	return assessment.Unquoted(r.Criteria, r.Extras)
}
