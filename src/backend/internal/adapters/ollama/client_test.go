package ollama

import (
	"encoding/json"
	"testing"
)

func TestGenerationOptionsComeFromTheInstallation(t *testing.T) {
	var options Options
	raw := `{"*": {"num_ctx": 131072}, "tuned": {"temperature": 0.7, "top_k": 20}, "short": {"num_ctx": 4096, "num_predict": 100}}`
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		t.Fatal(err)
	}

	c := New("http://ollama", options)
	opts, size, limit := c.generation("tuned", true, 42)
	if size != 131072 || limit != maxReasoning || opts["temperature"] != 0.7 || opts["top_k"] != 20.0 || opts["seed"] != 42 {
		t.Fatalf("a model's options go over the common ones: %v, context %d, limit %d", opts, size, limit)
	}

	if _, size, limit = c.generation("other", false, 42); size != 131072 || limit != maxOutput {
		t.Fatalf("context %d, limit %d", size, limit)
	}

	if _, size, limit = c.generation("short", true, 42); size != 4096 || limit != 100 {
		t.Fatalf("the installation's limit wins: context %d, limit %d", size, limit)
	}
}

func TestWithoutOptionsSamplingIsLeftToTheModelAndTheInputHasRoom(t *testing.T) {
	opts, size, limit := New("http://ollama", nil).generation("any", true, 7)
	if size != defaultContext || limit != defaultContext/4 {
		t.Fatalf("context %d, limit %d", size, limit)
	}

	if _, set := opts["temperature"]; set || len(opts) != 3 {
		t.Fatalf("only the context, the limit and the seed are sent: %v", opts)
	}
}
