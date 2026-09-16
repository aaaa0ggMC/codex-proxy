package main

import (
	"testing"
)

func TestOpenAIModelsResponse(t *testing.T) {
	models := []CodexModel{
		{Slug: "gpt-5.5", SupportedInAPI: true, Visibility: "list"},
		{Slug: "gpt-6-astra", SupportedInAPI: true, Visibility: "list"},
	}

	resp := openAIModelsResponse(models)
	data, ok := resp["data"].([]any)
	if !ok {
		t.Fatalf("expected data array, got %T", resp["data"])
	}

	modelIDs := make(map[string]bool)
	for _, item := range data {
		m := item.(map[string]any)
		modelIDs[m["id"].(string)] = true
	}

	expected := []string{
		"gpt-5.5", "gpt-5.5-search", "gpt-5.5-search-preview",
		"gpt-6-astra", "gpt-6-astra-search", "gpt-6-astra-search-preview",
		"gpt-4o-search-preview",
	}
	for _, id := range expected {
		if !modelIDs[id] {
			t.Errorf("expected model %s in models response", id)
		}
	}
}

func TestChatCompletionFromAggregate_Annotations(t *testing.T) {
	agg := OpenAIResponse{
		ID:         "resp_123456",
		CreatedAt:  1000,
		OutputText: "test answer",
		Output: []any{
			map[string]any{
				"type": "message",
				"role": "assistant",
				"content": []any{
					map[string]any{
						"type": "output_text",
						"text": "test answer",
						"annotations": []any{
							map[string]any{
								"type":  "url_citation",
								"url":   "https://example.com",
								"title": "Example",
							},
						},
					},
				},
			},
		},
	}

	chatCmpl := ChatCompletionFromAggregate(agg, "gpt-5.5")
	choices := chatCmpl["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(choices))
	}
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	annotations, ok := msg["annotations"].([]any)
	if !ok || len(annotations) != 1 {
		t.Fatalf("expected 1 annotation in message, got %v", msg["annotations"])
	}
	ann := annotations[0].(map[string]any)
	if ann["url"] != "https://example.com" {
		t.Errorf("expected url https://example.com, got %v", ann["url"])
	}
}
