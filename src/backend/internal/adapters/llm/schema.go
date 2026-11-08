package llm

import (
	"moodle-autograder/internal/domain/assessment"
)

// ---------- checklist ----------

var whereSchema = map[string]any{"type": "string", "enum": []any{"report", "screenshots", "code", "archive"}}

var RubricSchema = obj(map[string]any{
	"criteria": map[string]any{
		"type": "array", "minItems": 3, "maxItems": 20,
		"items": obj(map[string]any{"text": str(), "where": whereSchema, "points": map[string]any{"type": "number"}}, "text", "where", "points"),
	},
	"extras": map[string]any{"type": "array", "maxItems": 5, "items": obj(map[string]any{"text": str(), "where": whereSchema}, "text", "where")},
}, "criteria", "extras")

// ---------- screenshots ----------

var PageSchema = obj(map[string]any{"transcript": str(), "description": str(), "addressed": map[string]any{"type": "array", "items": str()}}, "transcript", "description", "addressed")

// ---------- injection detector ----------

var InjectionSchema = obj(map[string]any{"suspected": map[string]any{"type": "boolean"}, "quotes": map[string]any{"type": "array", "items": str()}, "explanation": str()},
	"suspected", "quotes", "explanation")

// ---------- checklist evaluation ----------

func CheckSchema(criteria, extras []assessment.Criterion) any {
	ev := obj(map[string]any{"source": map[string]any{"type": "string", "enum": []any{"code", "report", "screenshot", "archive"}}, "file": str(), "quote": str()}, "source", "quote")
	list := func(cs []assessment.Criterion) map[string]any {
		ids := make([]any, len(cs))
		for i, c := range cs {
			ids[i] = c.ID
		}
		item := obj(map[string]any{
			"id": map[string]any{"type": "string", "enum": ids}, "status": map[string]any{"type": "string", "enum": []any{"met", "partial", "not_met"}},
			"evidence": map[string]any{"type": "array", "items": ev}, "note": str(),
		}, "id", "status", "evidence", "note")
		if len(cs) == 0 {
			return map[string]any{"type": "array", "maxItems": 0}
		}
		return map[string]any{"type": "array", "items": item}
	}
	return obj(map[string]any{"criteria": list(criteria), "extras": list(extras)}, "criteria", "extras")
}

// ---------- comment for the student ----------

var CommentSchema = obj(map[string]any{"comment": str()}, "comment")

func str() map[string]any { return map[string]any{"type": "string"} }

func obj(props map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}
