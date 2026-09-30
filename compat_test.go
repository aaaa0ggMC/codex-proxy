package main

import (
	"strings"
	"testing"
)

func TestNormalizeResponsesRequest_WebSearchPreview(t *testing.T) {
	raw := map[string]any{
		"model": "gpt-5.5",
		"input": "test input",
		"tools": []any{
			map[string]any{
				"type":                "web_search_preview",
				"search_context_size": "high",
			},
		},
		"tool_choice": map[string]any{
			"type": "web_search_preview",
		},
	}

	normalized, _, err := NormalizeResponsesRequest(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tools, ok := normalized["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %v", normalized["tools"])
	}

	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("expected map tool, got %T", tools[0])
	}
	if tool["type"] != "web_search" {
		t.Errorf("expected type web_search, got %v", tool["type"])
	}
	if tool["search_context_size"] != "high" {
		t.Errorf("expected search_context_size high, got %v", tool["search_context_size"])
	}

	tc, ok := normalized["tool_choice"].(map[string]any)
	if !ok || tc["type"] != "web_search" {
		t.Errorf("expected tool_choice type web_search, got %v", normalized["tool_choice"])
	}
}

func TestNormalizeResponsesRequest_WebSearchOptions(t *testing.T) {
	raw := map[string]any{
		"model": "gpt-5.5",
		"input": "test input",
		"web_search_options": map[string]any{
			"search_context_size": "medium",
			"user_location": map[string]any{
				"country": "US",
			},
		},
	}

	normalized, _, err := NormalizeResponsesRequest(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tools, ok := normalized["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %v", normalized["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "web_search" {
		t.Errorf("expected type web_search, got %v", tool["type"])
	}
	if tool["search_context_size"] != "medium" {
		t.Errorf("expected search_context_size medium, got %v", tool["search_context_size"])
	}
	loc, ok := tool["user_location"].(map[string]any)
	if !ok || loc["country"] != "US" || loc["type"] != "approximate" {
		t.Errorf("expected approximate location with US country, got %v", tool["user_location"])
	}
}

func TestNormalizeResponsesRequest_ModelSuffix(t *testing.T) {
	raw := map[string]any{
		"model": "gpt-5.5-search",
		"input": "test input",
	}

	normalized, _, err := NormalizeResponsesRequest(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if normalized["model"] != "gpt-5.5" {
		t.Errorf("expected upstream model gpt-5.5, got %v", normalized["model"])
	}

	tools, ok := normalized["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool added, got %v", normalized["tools"])
	}
	if tools[0].(map[string]any)["type"] != "web_search" {
		t.Errorf("expected web_search tool, got %v", tools[0])
	}
}

func TestBuildResponsesRequestFromChat_WebSearchOptions(t *testing.T) {
	raw := map[string]any{
		"model": "gpt-5.5",
		"messages": []any{
			map[string]any{"role": "user", "content": "What is the weather?"},
		},
		"web_search_options": map[string]any{
			"search_context_size": "high",
		},
	}

	req, _, err := BuildResponsesRequestFromChat(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tools, ok := req["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %v", req["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "web_search" {
		t.Errorf("expected type web_search, got %v", tool["type"])
	}
	if tool["search_context_size"] != "high" {
		t.Errorf("expected search_context_size high, got %v", tool["search_context_size"])
	}
}

func TestBuildResponsesRequestFromChat_ToolsAndToolChoice(t *testing.T) {
	raw := map[string]any{
		"model": "gpt-5.5",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": "my_func",
				},
			},
			map[string]any{
				"type": "web_search_preview",
			},
		},
		"tool_choice": map[string]any{
			"type": "web_search_preview",
		},
	}

	req, _, err := BuildResponsesRequestFromChat(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tools, ok := req["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %v", req["tools"])
	}

	toolChoice, ok := req["tool_choice"].(map[string]any)
	if !ok || toolChoice["type"] != "web_search" {
		t.Errorf("expected tool_choice type web_search, got %v", req["tool_choice"])
	}
}

func TestBuildResponsesRequestFromChat_ModelMapping(t *testing.T) {
	raw := map[string]any{
		"model": "gpt-4o-search-preview",
		"messages": []any{
			map[string]any{"role": "user", "content": "news"},
		},
	}

	req, _, err := BuildResponsesRequestFromChat(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req["model"] != "gpt-5.5" {
		t.Errorf("expected mapped model gpt-5.5, got %v", req["model"])
	}

	tools, ok := req["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %v", req["tools"])
	}
	if tools[0].(map[string]any)["type"] != "web_search" {
		t.Errorf("expected web_search tool, got %v", tools[0])
	}
}

func TestBuildResponsesRequestFromChat_GlobalFlag(t *testing.T) {
	raw := map[string]any{
		"model": "gpt-5.5",
		"messages": []any{
			map[string]any{"role": "user", "content": "ping"},
		},
	}

	req, _, err := BuildResponsesRequestFromChat(raw, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tools, ok := req["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool when global flag is enabled, got %v", req["tools"])
	}
	if tools[0].(map[string]any)["type"] != "web_search" {
		t.Errorf("expected web_search tool, got %v", tools[0])
	}

	rawDisabled := map[string]any{
		"model": "gpt-5.5",
		"messages": []any{
			map[string]any{"role": "user", "content": "ping"},
		},
		"web_search_options": false,
	}
	req2, _, err := BuildResponsesRequestFromChat(rawDisabled, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req2["tools"] != nil {
		t.Errorf("expected nil tools when web_search_options is false, got %v", req2["tools"])
	}
}

func userContent(t *testing.T, req map[string]any) any {
	t.Helper()
	input, ok := req["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("expected 1 input item, got %v", req["input"])
	}
	message, ok := input[0].(map[string]any)
	if !ok || message["role"] != "user" {
		t.Fatalf("expected a user message, got %v", input[0])
	}
	return message["content"]
}

func contentPart(t *testing.T, content any, index int) map[string]any {
	t.Helper()
	parts, ok := content.([]any)
	if !ok {
		t.Fatalf("expected content parts, got %T (%v)", content, content)
	}
	if len(parts) <= index {
		t.Fatalf("expected at least %d parts, got %v", index+1, parts)
	}
	part, ok := parts[index].(map[string]any)
	if !ok {
		t.Fatalf("expected a part object, got %T", parts[index])
	}
	return part
}

func TestBuildResponsesRequestFromChat_ImageAttachment(t *testing.T) {
	dataURL := "data:image/png;base64,iVBORw0KGgo="
	raw := map[string]any{
		"model": "gpt-5.5",
		"messages": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "what is in this image?"},
				map[string]any{"type": "image_url", "image_url": map[string]any{
					"url":    dataURL,
					"detail": "high",
				}},
			}},
		},
	}

	req, _, err := BuildResponsesRequestFromChat(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := userContent(t, req)
	text := contentPart(t, content, 0)
	if text["type"] != "input_text" || text["text"] != "what is in this image?" {
		t.Errorf("unexpected text part: %v", text)
	}
	image := contentPart(t, content, 1)
	if image["type"] != "input_image" {
		t.Errorf("expected input_image, got %v", image["type"])
	}
	if image["image_url"] != dataURL {
		t.Errorf("expected image data url to be preserved, got %v", image["image_url"])
	}
	if image["detail"] != "high" {
		t.Errorf("expected detail high, got %v", image["detail"])
	}
}

func TestBuildResponsesRequestFromChat_ImageURLString(t *testing.T) {
	raw := map[string]any{
		"model": "gpt-5.5",
		"messages": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "image_url", "image_url": "https://example.com/cat.png"},
			}},
		},
	}

	req, _, err := BuildResponsesRequestFromChat(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	image := contentPart(t, userContent(t, req), 0)
	if image["type"] != "input_image" || image["image_url"] != "https://example.com/cat.png" {
		t.Errorf("unexpected image part: %v", image)
	}
}

func TestBuildResponsesRequestFromChat_AudioAttachment(t *testing.T) {
	raw := map[string]any{
		"model": "gpt-5.5",
		"messages": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_audio", "input_audio": map[string]any{
					"data":   "AQID",
					"format": "mp3",
				}},
			}},
		},
	}

	req, _, err := BuildResponsesRequestFromChat(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	audio := contentPart(t, userContent(t, req), 0)
	if audio["type"] != "input_audio" {
		t.Errorf("expected input_audio, got %v", audio["type"])
	}
	if audio["audio_url"] != "data:audio/mp3;base64,AQID" {
		t.Errorf("unexpected audio_url: %v", audio["audio_url"])
	}
}

func TestBuildResponsesRequestFromChat_UnsupportedAttachment(t *testing.T) {
	raw := map[string]any{
		"model": "gpt-5.5",
		"messages": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "file", "file": map[string]any{"filename": "notes.pdf"}},
			}},
		},
	}

	_, _, err := BuildResponsesRequestFromChat(raw)
	if err == nil {
		t.Fatal("expected an error for an unsupported content type")
	}
	if !strings.Contains(err.Error(), "unsupported message content type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildResponsesRequestFromChat_PlainStringContent(t *testing.T) {
	raw := map[string]any{
		"model": "gpt-5.5",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
	}

	req, _, err := BuildResponsesRequestFromChat(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content := userContent(t, req); content != "hello" {
		t.Errorf("expected content to stay a plain string, got %v", content)
	}
}
