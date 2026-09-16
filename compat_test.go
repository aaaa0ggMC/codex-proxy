package main

import (
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
