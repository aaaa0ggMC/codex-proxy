package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The Codex backend exposes the same numbers the CLI shows in /status: a short metering window
// (5 hours on the current plans), a long one (a week) and optional credits. codex-proxy asks
// the same endpoint the CLI does and normalises it for clients.
var codexUsageURL = "https://chatgpt.com/backend-api/wham/usage"

const (
	fiveHourWindowSeconds = 5 * 60 * 60
	weeklyWindowSeconds   = 7 * 24 * 60 * 60
)

type usageWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds *int     `json:"limit_window_seconds"`
	ResetAfterSeconds  *int     `json:"reset_after_seconds"`
	ResetAt            *int64   `json:"reset_at"`
}

type usageRateLimit struct {
	Allowed         *bool        `json:"allowed"`
	LimitReached    *bool        `json:"limit_reached"`
	PrimaryWindow   *usageWindow `json:"primary_window"`
	SecondaryWindow *usageWindow `json:"secondary_window"`
}

type usageCredits struct {
	HasCredits          *bool  `json:"has_credits"`
	Unlimited           *bool  `json:"unlimited"`
	OverageLimitReached *bool  `json:"overage_limit_reached"`
	Balance             string `json:"balance"`
}

type codexUsageResponse struct {
	PlanType             string          `json:"plan_type"`
	RateLimit            *usageRateLimit `json:"rate_limit"`
	Credits              *usageCredits   `json:"credits"`
	RateLimitReachedType any             `json:"rate_limit_reached_type"`
}

// UsageWindow is one metering window with what is left of it.
type UsageWindow struct {
	Label             string   `json:"label"`
	ShortLabel        string   `json:"short_label"`
	Slot              string   `json:"slot"`
	UsedPercent       *float64 `json:"used_percent"`
	RemainingPercent  *float64 `json:"remaining_percent"`
	WindowSeconds     *int     `json:"window_seconds"`
	ResetAfterSeconds *int     `json:"reset_after_seconds"`
	ResetsAt          string   `json:"resets_at,omitempty"`
	ResetsAtUnix      *int64   `json:"resets_at_unix,omitempty"`
}

// UsageReport is the shape the proxy serves to clients. The nested windows carry everything,
// and the flat scalar fields exist for UIs that can only bind one JSON key to one number.
type UsageReport struct {
	PlanType     string                 `json:"plan_type,omitempty"`
	Allowed      *bool                  `json:"allowed,omitempty"`
	LimitReached bool                   `json:"limit_reached"`
	Windows      map[string]UsageWindow `json:"windows"`
	Credits      *usageCredits          `json:"credits,omitempty"`
	FetchedAt    time.Time              `json:"fetched_at"`
	Stale        bool                   `json:"stale,omitempty"`

	// Remaining percent of the window that is closest to running out, and its label. This is the
	// number to bind when a client only has room for one: it is the one that gates you.
	RemainingPercent *float64 `json:"remaining_percent,omitempty"`
	RemainingLabel   string   `json:"remaining_label,omitempty"`
	// Per window remaining percent, keyed by what the plan meters (5 hours / a week).
	FiveHourRemainingPercent *float64 `json:"five_hour_remaining_percent,omitempty"`
	WeeklyRemainingPercent   *float64 `json:"weekly_remaining_percent,omitempty"`
}

// Usage asks the Codex backend for the current rate limit state.
func (c *CodexClient) Usage(ctx context.Context) (*UsageReport, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexUsageURL, nil)
	if err != nil {
		return nil, err
	}
	c.setAuthHeaders(req, token)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Codex usage request failed: HTTP %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	var raw codexUsageResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode Codex usage response: %w", err)
	}
	return NormalizeUsage(&raw, time.Now()), nil
}

// NormalizeUsage turns the backend payload into the stable report clients see: the windows are
// keyed by how long they last (five_hour / weekly) so a client does not have to know which slot
// the plan happens to meter.
func NormalizeUsage(raw *codexUsageResponse, now time.Time) *UsageReport {
	report := &UsageReport{
		PlanType:  raw.PlanType,
		Windows:   map[string]UsageWindow{},
		Credits:   raw.Credits,
		FetchedAt: now.UTC(),
	}
	if raw.RateLimit == nil {
		return report
	}
	report.Allowed = raw.RateLimit.Allowed
	report.LimitReached = raw.RateLimit.LimitReached != nil && *raw.RateLimit.LimitReached

	slots := []struct {
		slot   string
		window *usageWindow
	}{
		{"primary", raw.RateLimit.PrimaryWindow},
		{"secondary", raw.RateLimit.SecondaryWindow},
	}
	for _, entry := range slots {
		if entry.window == nil {
			continue
		}
		key := windowKey(entry.slot, entry.window)
		next := normalizeWindow(entry.slot, entry.window)
		// Two slots can collide on one key if a plan meters something unexpected; the window
		// that is further used is the one worth showing.
		if prev, ok := report.Windows[key]; ok && !moreUsed(next, prev) {
			continue
		}
		report.Windows[key] = next
	}
	report.attachSummaries()
	return report
}

// attachSummaries fills the flat fields: the tightest window (the number that decides whether
// you can keep working) and the two named windows. The 5 hour / weekly fields are matched by
// window length first and fall back to the plan's primary / secondary slot.
func (r *UsageReport) attachSummaries() {
	if tightest, ok := tightestWindow(r); ok {
		remaining := round2(remainingOf(tightest))
		r.RemainingPercent = &remaining
		r.RemainingLabel = tightest.ShortLabel
	}
	for _, window := range r.Windows {
		if window.RemainingPercent == nil {
			continue
		}
		name := window.ShortLabel
		switch {
		case window.WindowSeconds != nil && *window.WindowSeconds == fiveHourWindowSeconds:
			name = "5h"
		case window.WindowSeconds != nil && *window.WindowSeconds == weeklyWindowSeconds:
			name = "7d"
		}
		remaining := round2(remainingOf(window))
		if name == "5h" && r.FiveHourRemainingPercent == nil {
			r.FiveHourRemainingPercent = &remaining
		}
		if name == "7d" && r.WeeklyRemainingPercent == nil {
			r.WeeklyRemainingPercent = &remaining
		}
	}
	if r.FiveHourRemainingPercent == nil {
		if window, ok := findWindow(r, "primary"); ok {
			remaining := round2(remainingOf(window))
			r.FiveHourRemainingPercent = &remaining
		}
	}
	if r.WeeklyRemainingPercent == nil {
		if window, ok := findWindow(r, "secondary"); ok {
			remaining := round2(remainingOf(window))
			r.WeeklyRemainingPercent = &remaining
		}
	}
}

func findWindow(report *UsageReport, slot string) (UsageWindow, bool) {
	for _, window := range report.Windows {
		if window.Slot == slot {
			return window, true
		}
	}
	return UsageWindow{}, false
}

func tightestWindow(report *UsageReport) (UsageWindow, bool) {
	windows := usageWindows(report)
	if len(windows) == 0 {
		return UsageWindow{}, false
	}
	tightest := windows[0]
	for _, candidate := range windows[1:] {
		if remainingOf(candidate) < remainingOf(tightest) {
			tightest = candidate
		}
	}
	return tightest, true
}

func moreUsed(candidate, current UsageWindow) bool {
	if candidate.UsedPercent == nil {
		return false
	}
	if current.UsedPercent == nil {
		return true
	}
	return *candidate.UsedPercent > *current.UsedPercent
}

func windowKey(slot string, window *usageWindow) string {
	if window != nil && window.LimitWindowSeconds != nil {
		switch {
		case *window.LimitWindowSeconds == fiveHourWindowSeconds:
			return "five_hour"
		case *window.LimitWindowSeconds == weeklyWindowSeconds:
			return "weekly"
		}
	}
	return slot
}

func normalizeWindow(slot string, window *usageWindow) UsageWindow {
	out := UsageWindow{Slot: slot, WindowSeconds: window.LimitWindowSeconds}
	out.Label = windowLabel(out.WindowSeconds)
	out.ShortLabel = shortWindowLabel(out.WindowSeconds)
	if window.UsedPercent != nil {
		used := clampPercent(*window.UsedPercent)
		remaining := clampPercent(100 - used)
		out.UsedPercent = &used
		out.RemainingPercent = &remaining
	}
	out.ResetAfterSeconds = window.ResetAfterSeconds
	if window.ResetAt != nil {
		out.ResetsAtUnix = window.ResetAt
		out.ResetsAt = time.Unix(*window.ResetAt, 0).UTC().Format(time.RFC3339)
	}
	return out
}

func windowLabel(seconds *int) string {
	if seconds == nil {
		return ""
	}
	hours := *seconds / 3600
	switch {
	case *seconds == fiveHourWindowSeconds:
		return "5h"
	case *seconds == weeklyWindowSeconds:
		return "weekly"
	case *seconds%3600 == 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", *seconds/60)
	}
}

// shortWindowLabel is what fits in a one line display: 5h, 7d, 30m.
func shortWindowLabel(seconds *int) string {
	if seconds == nil || *seconds <= 0 {
		return "quota"
	}
	switch {
	case *seconds == fiveHourWindowSeconds:
		return "5h"
	case *seconds == weeklyWindowSeconds:
		return "7d"
	case *seconds%3600 == 0:
		return fmt.Sprintf("%dh", *seconds/3600)
	case *seconds%60 == 0:
		return fmt.Sprintf("%dm", *seconds/60)
	default:
		return fmt.Sprintf("%ds", *seconds)
	}
}

// usageWindows returns the report's windows ordered shortest first, which is the order a one
// line display wants: the 5 hour window before the weekly one.
func usageWindows(report *UsageReport) []UsageWindow {
	if report == nil {
		return nil
	}
	windows := make([]UsageWindow, 0, len(report.Windows))
	for _, window := range report.Windows {
		windows = append(windows, window)
	}
	sort.SliceStable(windows, func(i, j int) bool {
		left, right := windows[i].WindowSeconds, windows[j].WindowSeconds
		switch {
		case left == nil:
			return false
		case right == nil:
			return true
		default:
			return *left < *right
		}
	})
	return windows
}

// usageText is the one line form: "5h 100% · 7d 6%".
func usageText(report *UsageReport) string {
	windows := usageWindows(report)
	if len(windows) == 0 {
		return "usage unavailable"
	}
	parts := make([]string, 0, len(windows))
	for _, window := range windows {
		parts = append(parts, fmt.Sprintf("%s %.0f%%", window.ShortLabel, remainingOf(window)))
	}
	return strings.Join(parts, " · ")
}

// usageValue is the single number a plain "remaining" field wants: by default the window that
// is closest to running out, because that is the one that actually gates you.
func usageValue(report *UsageReport, window string) (float64, bool) {
	if report == nil {
		return 0, false
	}
	switch window {
	case "", "tightest", "min":
		if report.RemainingPercent != nil {
			return *report.RemainingPercent, true
		}
	case "five_hour", "5h", "primary":
		if report.FiveHourRemainingPercent != nil {
			return *report.FiveHourRemainingPercent, true
		}
	case "weekly", "7d", "secondary":
		if report.WeeklyRemainingPercent != nil {
			return *report.WeeklyRemainingPercent, true
		}
	}
	return 0, false
}

func remainingOf(window UsageWindow) float64 {
	if window.RemainingPercent == nil {
		return 0
	}
	return *window.RemainingPercent
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

const (
	usageTTLEnv     = "CODEX_PROXY_USAGE_TTL_SECONDS"
	defaultUsageTTL = 15 * time.Second
)

// usageTTLFromEnv reads how long a fetched report may be reused. 0 always refreshes.
func usageTTLFromEnv() time.Duration {
	value := os.Getenv(usageTTLEnv)
	if value == "" {
		return defaultUsageTTL
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds < 0 {
		return defaultUsageTTL
	}
	return time.Duration(seconds) * time.Second
}

// usageReport returns a cached report when it is still fresh, otherwise it asks the backend.
// When the refresh fails but an older report exists, that report is served with stale=true
// instead of an error, so a dashboard keeps showing the last known numbers.
func (s *Server) usageReport(ctx context.Context, refresh bool) (*UsageReport, error) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()

	ttl := s.usageTTL
	if ttl < 0 {
		ttl = defaultUsageTTL
	}
	if !refresh && s.usageCache != nil && time.Since(s.usageCacheAt) < ttl {
		return s.usageCache, nil
	}

	report, err := s.codex.Usage(ctx)
	if err != nil {
		if s.usageCache == nil {
			return nil, err
		}
		stale := *s.usageCache
		stale.Stale = true
		return &stale, err
	}
	s.usageCache = report
	s.usageCacheAt = time.Now()
	return report, nil
}
