package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	codex     *CodexClient
	log       *slog.Logger
	apiKey    string
	webSearch bool

	usageTTL     time.Duration
	usageMu      sync.Mutex
	usageCache   *UsageReport
	usageCacheAt time.Time
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("GET /v1/usage", s.handleUsage)
	mux.HandleFunc("POST /v1/responses", s.handleResponses)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	return s.logRequests(s.withUsageHeaders(s.requireAPIKey(mux)))
}

func (s *Server) requireAPIKey(next http.Handler) http.Handler {
	if s.apiKey == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validBearerToken(r.Header.Get("Authorization"), s.apiKey) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="codex-proxy"`)
			writeOpenAIError(w, http.StatusUnauthorized, "missing or invalid API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func validBearerToken(header, apiKey string) bool {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	tokenHash := sha256.Sum256([]byte(token))
	apiKeyHash := sha256.Sum256([]byte(apiKey))
	return subtle.ConstantTimeCompare(tokenHash[:], apiKeyHash[:]) == 1
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := randomHex(4)
		log := s.log.With(
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
		)
		lw := &loggingResponseWriter{ResponseWriter: w, status: http.StatusOK}
		log.Info("request started")
		next.ServeHTTP(lw, r)

		log.Info("request finished",
			"status", lw.status,
			"bytes", lw.bytes,
			"duration", time.Since(start).Round(time.Millisecond).String(),
		)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *loggingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.codex.Models(r.Context())
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, openAIModelsResponse(models))
}

// withUsageHeaders labels every response with the last known quota. It never triggers a fetch:
// a chat request must not wait on the usage endpoint. Clients that only have room for one value
// can read X-Codex-Usage-Remaining instead of polling /v1/usage.
func (s *Server) withUsageHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.usageMu.Lock()
		report := s.usageCache
		s.usageMu.Unlock()
		if report != nil {
			for header, window := range map[string]string{
				"X-Codex-Usage-Remaining": "tightest",
				"X-Codex-Usage-5h":        "five_hour",
				"X-Codex-Usage-Weekly":    "weekly",
			} {
				if value, ok := usageValue(report, window); ok {
					w.Header().Set(header, strconv.FormatFloat(value, 'f', -1, 64))
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// handleUsage reports the remaining Codex quota (the 5 hour window and the weekly one).
//
//	GET /v1/usage                    full JSON report (default); bind remaining_percent, or
//	                                 five_hour_remaining_percent / weekly_remaining_percent
//	GET /v1/usage?format=text        one line: "5h 100% · 7d 6%"
//	GET /v1/usage?format=number      one number: remaining percent, tightest window by default
//	                                 (?window=five_hour|weekly|tightest picks another one)
//
// ?refresh=1 bypasses the cache.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	format := strings.ToLower(strings.TrimSpace(query.Get("format")))
	if format == "" {
		format = "json"
	}
	refresh := query.Get("refresh") != ""
	window := strings.ToLower(strings.TrimSpace(query.Get("window")))

	report, err := s.usageReport(r.Context(), refresh)
	if err != nil {
		if report == nil {
			writeOpenAIError(w, http.StatusBadGateway, err.Error())
			return
		}
		s.log.Warn("usage refresh failed; serving the last known report", "error", err.Error())
	}

	w.Header().Set("Cache-Control", "no-store")
	switch format {
	case "json":
		writeJSON(w, http.StatusOK, report)
	case "text", "plain":
		writeText(w, usageText(report))
	case "number", "value", "percent":
		value, ok := usageValue(report, window)
		if !ok {
			writeOpenAIError(w, http.StatusBadGateway, fmt.Sprintf("no usage window matches %q", window))
			return
		}
		writeText(w, strconv.FormatFloat(value, 'f', -1, 64))
	default:
		writeOpenAIError(w, http.StatusBadRequest, fmt.Sprintf("unsupported format %q; use json, text or number", format))
	}
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	raw, err := decodeJSONMap(r)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error())
		return
	}
	upstream, stream, err := NormalizeResponsesRequest(raw, s.webSearch)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if stream {
		s.streamResponses(w, r.Context(), upstream)
		return
	}
	agg, err := s.aggregateResponses(r.Context(), upstream)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agg)
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	raw, err := decodeJSONMap(r)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error())
		return
	}
	upstream, stream, err := BuildResponsesRequestFromChat(raw, s.webSearch)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error())
		return
	}
	model := stringValue(raw, "model")
	if stream {
		s.streamChatCompletions(w, r.Context(), upstream, model, includeUsage(raw))
		return
	}
	agg, err := s.aggregateResponses(r.Context(), upstream)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ChatCompletionFromAggregate(agg, model))
}

func (s *Server) aggregateResponses(ctx context.Context, upstream map[string]any) (OpenAIResponse, error) {
	resp, err := s.codex.StreamResponses(ctx, upstream)
	if err != nil {
		return OpenAIResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return OpenAIResponse{}, upstreamError(resp)
	}
	return AggregateResponsesStream(resp.Body, upstream)
}

func (s *Server) streamChatCompletions(w http.ResponseWriter, ctx context.Context, upstream map[string]any, model string, sendUsage bool) {
	resp, err := s.codex.StreamResponses(ctx, upstream)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeOpenAIError(w, resp.StatusCode, upstreamError(resp).Error())
		return
	}

	setSSEHeaders(w.Header())
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	id := "chatcmpl-" + randomHex(16)
	created := time.Now().Unix()
	finishReason := "stop"
	var usage any
	toolIndex := 0

	sendChunk := func(choices []any, usage any) error {
		chunk := openAIChatCompletionChunk(id, model, created, choices, usage)
		if err := writeSSEData(w, chunk); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}

	if err := sendChunk([]any{openAIChatDeltaChoice(map[string]any{"role": "assistant", "content": ""}, nil)}, nil); err != nil {
		return
	}

	err = ReadStreamEvents(resp.Body, func(event StreamEvent) error {
		switch event.Type {
		case "response.output_text.delta":
			return sendChunk([]any{openAIChatDeltaChoice(map[string]any{"content": stringValue(event.Data, "delta")}, nil)}, nil)
		case "response.output_text.annotation.added":
			if ann, ok := event.Data["annotation"]; ok && ann != nil {
				return sendChunk([]any{openAIChatDeltaChoice(map[string]any{"annotations": []any{ann}}, nil)}, nil)
			}
		case "response.output_item.done":
			item, _ := event.Data["item"].(map[string]any)
			if stringValue(item, "type") != "function_call" {
				return nil
			}
			finishReason = "tool_calls"
			callID := defaultedString(item, "call_id", defaultedString(item, "id", "call_"+randomHex(8)))
			arguments := defaultedString(item, "arguments", "{}")
			delta := openAIChatToolCallDelta(toolIndex, callID, stringValue(item, "name"), arguments)
			toolIndex++
			return sendChunk([]any{openAIChatDeltaChoice(delta, nil)}, nil)
		case "response.completed":
			if response, ok := event.Data["response"].(map[string]any); ok {
				usage = response["usage"]
			}
		}
		return nil
	})
	if err != nil || ctx.Err() != nil {
		return
	}

	_ = sendChunk([]any{openAIChatDeltaChoice(map[string]any{}, finishReason)}, nil)
	if sendUsage {
		_ = sendChunk([]any{}, chatUsageFromResponsesUsage(usage))
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func (s *Server) streamResponses(w http.ResponseWriter, ctx context.Context, upstream map[string]any) {
	resp, err := s.codex.StreamResponses(ctx, upstream)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeOpenAIError(w, resp.StatusCode, upstreamError(resp).Error())
		return
	}

	setSSEHeaders(w.Header())
	w.WriteHeader(http.StatusOK)
	if err := copyAndFlush(w, resp.Body); err != nil || ctx.Err() != nil {
		return
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	flush(w)
}

func decodeJSONMap(r *http.Request) (map[string]any, error) {
	dec := json.NewDecoder(r.Body)
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	return raw, nil
}

func upstreamError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	message := strings.TrimSpace(string(body))
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err == nil {
		if detail := stringValue(payload, "detail"); detail != "" {
			message = detail
		} else if errObj, ok := payload["error"].(map[string]any); ok {
			if msg := stringValue(errObj, "message"); msg != "" {
				message = msg
			}
		}
	}
	if message == "" {
		message = resp.Status
	}
	return fmt.Errorf("Codex upstream returned HTTP %d: %s", resp.StatusCode, message)
}

func includeUsage(raw map[string]any) bool {
	options, ok := raw["stream_options"].(map[string]any)
	return ok && boolValue(options, "include_usage")
}

func copyAndFlush(w http.ResponseWriter, r io.Reader) error {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			flush(w)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func flush(w http.ResponseWriter) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeSSEData(w io.Writer, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}

func setSSEHeaders(header http.Header) {
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
}

// writeText is used by the compact formats: a widget or a chat client field wants one value,
// not a JSON document.
func writeText(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body+"\n")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOpenAIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, openAIErrorResponse(message))
}
