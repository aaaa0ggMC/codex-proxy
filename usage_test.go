package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int) *int           { return &v }
func int64Ptr(v int64) *int64     { return &v }
func boolPtr(v bool) *bool        { return &v }

func sampleUsagePayload() []byte {
	return []byte(`{
	  "plan_type": "plus",
	  "rate_limit": {
	    "allowed": true,
	    "limit_reached": false,
	    "primary_window": {"used_percent": 12, "limit_window_seconds": 18000, "reset_after_seconds": 17772, "reset_at": 1789536659},
	    "secondary_window": {"used_percent": 94, "limit_window_seconds": 604800, "reset_after_seconds": 318791, "reset_at": 1789837677}
	  },
	  "credits": {"has_credits": false, "unlimited": false, "overage_limit_reached": false, "balance": "0"}
	}`)
}

func TestNormalizeUsageNamesTheTwoWindows(t *testing.T) {
	var raw codexUsageResponse
	if err := json.Unmarshal(sampleUsagePayload(), &raw); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	report := NormalizeUsage(&raw, time.Unix(1789500000, 0))

	if report.PlanType != "plus" {
		t.Errorf("plan_type = %q, want plus", report.PlanType)
	}
	if report.LimitReached {
		t.Error("limit_reached should be false")
	}

	fiveHour, ok := report.Windows["five_hour"]
	if !ok {
		t.Fatalf("windows = %v, want a five_hour entry", report.Windows)
	}
	if fiveHour.Label != "5h" || fiveHour.Slot != "primary" {
		t.Errorf("five hour window = %+v, want label 5h on the primary slot", fiveHour)
	}
	if *fiveHour.UsedPercent != 12 || *fiveHour.RemainingPercent != 88 {
		t.Errorf("used/remaining = %v/%v, want 12/88", *fiveHour.UsedPercent, *fiveHour.RemainingPercent)
	}
	if fiveHour.ResetsAt != time.Unix(1789536659, 0).UTC().Format(time.RFC3339) {
		t.Errorf("resets_at = %q", fiveHour.ResetsAt)
	}

	weekly, ok := report.Windows["weekly"]
	if !ok {
		t.Fatalf("windows = %v, want a weekly entry", report.Windows)
	}
	if weekly.Label != "weekly" || weekly.Slot != "secondary" {
		t.Errorf("weekly window = %+v", weekly)
	}
	if *weekly.RemainingPercent != 6 {
		t.Errorf("remaining = %v, want 6", *weekly.RemainingPercent)
	}
	if weekly.ResetAfterSeconds == nil || *weekly.ResetAfterSeconds != 318791 {
		t.Errorf("reset_after_seconds = %v", weekly.ResetAfterSeconds)
	}
}

func TestNormalizeUsageFallsBackToSlotsAndClamps(t *testing.T) {
	raw := &codexUsageResponse{
		PlanType: "free",
		RateLimit: &usageRateLimit{
			Allowed:         boolPtr(false),
			LimitReached:    boolPtr(true),
			PrimaryWindow:   &usageWindow{UsedPercent: floatPtr(140), LimitWindowSeconds: intPtr(3600)},
			SecondaryWindow: &usageWindow{UsedPercent: floatPtr(-5), LimitWindowSeconds: intPtr(0)},
		},
	}
	report := NormalizeUsage(raw, time.Unix(0, 0))

	if !report.LimitReached || report.Allowed == nil || *report.Allowed {
		t.Errorf("limit state = %+v, want limit_reached with allowed false", report)
	}
	primary := report.Windows["primary"]
	if primary.Label != "1h" {
		t.Errorf("label = %q, want 1h for an unknown window length", primary.Label)
	}
	if *primary.UsedPercent != 100 || *primary.RemainingPercent != 0 {
		t.Errorf("used/remaining = %v/%v, want a clamp to 100/0", *primary.UsedPercent, *primary.RemainingPercent)
	}
	secondary := report.Windows["secondary"]
	if *secondary.RemainingPercent != 100 {
		t.Errorf("remaining = %v, want 100 after clamping a negative usage", *secondary.RemainingPercent)
	}
}

func TestNormalizeUsageWithoutRateLimit(t *testing.T) {
	report := NormalizeUsage(&codexUsageResponse{PlanType: "plus"}, time.Unix(0, 0))
	if len(report.Windows) != 0 {
		t.Errorf("windows = %v, want none", report.Windows)
	}
	if report.LimitReached {
		t.Error("limit_reached should default to false")
	}
}

func fakeCodexHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(time.Hour).Unix())))
	auth := map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token":  "header." + payload + ".signature",
			"refresh_token": "refresh-token",
			"account_id":    "acct-1",
		},
	}
	body, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), body, 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	return dir
}

func TestCodexClientUsageUsesTheBackendEndpoint(t *testing.T) {
	var gotPath, gotAuth, gotAccount string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("ChatGPT-Account-ID")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(sampleUsagePayload())
	}))
	defer server.Close()

	original := codexUsageURL
	codexUsageURL = server.URL + "/backend-api/wham/usage"
	defer func() { codexUsageURL = original }()

	client := &CodexClient{tokens: &TokenSource{codexHome: fakeCodexHome(t)}}
	report, err := client.Usage(context.Background())
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if gotPath != "/backend-api/wham/usage" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer header."+base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(time.Hour).Unix())))+".signature" {
		t.Errorf("Authorization header was not the bearer token: %q", gotAuth)
	}
	if gotAccount != "acct-1" {
		t.Errorf("ChatGPT-Account-ID = %q, want acct-1", gotAccount)
	}
	if *report.Windows["weekly"].RemainingPercent != 6 {
		t.Errorf("weekly remaining = %v, want 6", *report.Windows["weekly"].RemainingPercent)
	}
}

func TestUsageHandlerServesJSONAndStaleCopy(t *testing.T) {
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(sampleUsagePayload())
	}))
	defer server.Close()

	original := codexUsageURL
	codexUsageURL = server.URL + "/usage"
	defer func() { codexUsageURL = original }()

	srv := &Server{
		codex:    &CodexClient{tokens: &TokenSource{codexHome: fakeCodexHome(t)}},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		usageTTL: 0, // always ask the backend, so the stale path is reachable in a test
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	recorder := httptest.NewRecorder()
	srv.handleUsage(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	var report UsageReport
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if *report.Windows["five_hour"].UsedPercent != 12 || report.Stale {
		t.Errorf("report = %+v", report)
	}

	// Once the backend fails, the last known numbers are still served, marked stale.
	fail = true
	recorder = httptest.NewRecorder()
	srv.handleUsage(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want the cached report", recorder.Code)
	}
	report = UsageReport{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode stale report: %v", err)
	}
	if !report.Stale || *report.Windows["weekly"].UsedPercent != 94 {
		t.Errorf("stale report = %+v", report)
	}
}

func TestUsageHandlerWithoutCacheReportsTheError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer server.Close()

	original := codexUsageURL
	codexUsageURL = server.URL + "/usage"
	defer func() { codexUsageURL = original }()

	srv := &Server{codex: &CodexClient{tokens: &TokenSource{codexHome: fakeCodexHome(t)}}}

	recorder := httptest.NewRecorder()
	srv.handleUsage(recorder, httptest.NewRequest(http.MethodGet, "/v1/usage", nil))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", recorder.Code)
	}
	if body := recorder.Body.String(); !json.Valid([]byte(body)) {
		t.Errorf("body is not JSON: %s", body)
	}
}

func sampleReport(t *testing.T) *UsageReport {
	t.Helper()
	var raw codexUsageResponse
	if err := json.Unmarshal(sampleUsagePayload(), &raw); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return NormalizeUsage(&raw, time.Unix(1789500000, 0))
}

func TestUsageTextNamesBothWindows(t *testing.T) {
	report := sampleReport(t)
	if got, want := usageText(report), "5h 88% · 7d 6%"; got != want {
		t.Errorf("usageText = %q, want %q", got, want)
	}
}

func TestUsageValuePicksTheTightestWindow(t *testing.T) {
	report := sampleReport(t)
	cases := []struct {
		window string
		want   float64
	}{
		{"", 6},
		{"tightest", 6},
		{"five_hour", 88},
		{"5h", 88},
		{"weekly", 6},
		{"7d", 6},
	}
	for _, tc := range cases {
		got, ok := usageValue(report, tc.window)
		if !ok || got != tc.want {
			t.Errorf("usageValue(%q) = %v/%v, want %v", tc.window, got, ok, tc.want)
		}
	}
	if _, ok := usageValue(report, "monthly"); ok {
		t.Error("usageValue should reject a window it does not have")
	}
}

func TestUsageHandlerFormats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(sampleUsagePayload())
	}))
	defer server.Close()

	original := codexUsageURL
	codexUsageURL = server.URL + "/usage"
	defer func() { codexUsageURL = original }()

	srv := &Server{
		codex:    &CodexClient{tokens: &TokenSource{codexHome: fakeCodexHome(t)}},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		usageTTL: 60 * time.Second,
	}

	cases := []struct {
		query  string
		status int
		want   string
	}{
		{"", http.StatusOK, `"five_hour"`},
		{"?format=json", http.StatusOK, `"weekly"`},
		{"?format=text", http.StatusOK, "5h 88% · 7d 6%"},
		{"?format=number", http.StatusOK, "6"},
		{"?format=number&window=five_hour", http.StatusOK, "88"},
		{"?format=number&window=weekly", http.StatusOK, "6"},
		{"?format=yaml", http.StatusBadRequest, "unsupported format"},
		{"?format=number&window=monthly", http.StatusBadGateway, "no usage window matches"},
	}
	for _, tc := range cases {
		recorder := httptest.NewRecorder()
		srv.handleUsage(recorder, httptest.NewRequest(http.MethodGet, "/v1/usage"+tc.query, nil))
		if recorder.Code != tc.status {
			t.Errorf("%q: status = %d, want %d", tc.query, recorder.Code, tc.status)
			continue
		}
		if !strings.Contains(recorder.Body.String(), tc.want) {
			t.Errorf("%q: body %q does not contain %q", tc.query, recorder.Body.String(), tc.want)
		}
	}
}

func TestUsageHeadersAreAttachedWithoutFetching(t *testing.T) {
	srv := &Server{usageCache: sampleReport(t)}
	handler := srv.withUsageHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	want := map[string]string{
		"X-Codex-Usage-Remaining": "6",
		"X-Codex-Usage-5h":        "88",
		"X-Codex-Usage-Weekly":    "6",
	}
	for header, value := range want {
		if got := recorder.Header().Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}

	// Without a report the middleware must stay quiet, not fail the request.
	empty := &Server{}
	recorder = httptest.NewRecorder()
	empty.withUsageHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Header().Get("X-Codex-Usage-Remaining") != "" {
		t.Error("no usage cache means no usage headers")
	}
}
