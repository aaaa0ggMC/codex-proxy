# codex-proxy

A small local OpenAI-compatible proxy backed by your existing Codex CLI ChatGPT login.

```text
OpenAI-compatible client -> http://127.0.0.1:6769/v1 -> ChatGPT Codex backend
```

This is a compatibility adapter, not a transparent OpenAI proxy. The Codex backend currently requires streaming upstream requests and rejects some common OpenAI parameters, so `codex-proxy` normalizes requests before forwarding them.

## Upstream

This repository is a fork of [Max-Leopold/codex-proxy](https://github.com/Max-Leopold/codex-proxy),
and the fork relationship is kept so the original work stays visible. Everything up to and
including commit `942ee9f` is Max Leopold's; the original author and copyright are recorded in
[LICENSE](LICENSE).

Changes on top of upstream, all in the maintenance of this fork:

- `GET /v1/usage` reports the Codex quota windows, with a cached lookup that never delays a chat request.
- `--web-search` / `CODEX_PROXY_WEB_SEARCH` force the web search tool on every request.
- A wider `chat/completions` → Responses compatibility translation, with tests for the translator.
- Termux-friendly install and build notes, with the installer pointed at this fork.

Bug reports against upstream behaviour are best checked against
[Max-Leopold/codex-proxy](https://github.com/Max-Leopold/codex-proxy) first, since that is where the
adapter's original design comes from.

## Requirements

- Codex CLI authenticated with ChatGPT:

```bash
codex login
```

- Go 1.22+ only if building from source

## Install

macOS/Linux users can install the latest main build directly:

```bash
curl -fsSL https://raw.githubusercontent.com/aaaa0ggMC/codex-proxy/main/scripts/install.sh | bash
```

By default this installs to `/usr/local/bin`. To install somewhere else:

```bash
curl -fsSL https://raw.githubusercontent.com/aaaa0ggMC/codex-proxy/main/scripts/install.sh | INSTALL_DIR="$HOME/.local/bin" bash
```

Downloadable binaries for macOS, Linux, and Windows are published on the [GitHub Releases page](https://github.com/aaaa0ggMC/codex-proxy/releases).

### Android / Termux

`codex-proxy` is a plain static Go binary, so the Linux release builds run unmodified on
Termux. Termux sets `PREFIX`, and the installer already prefers it, so the default one-liner
installs to `$PREFIX/bin` where the shell can find it:

```bash
curl -fsSL https://raw.githubusercontent.com/aaaa0ggMC/codex-proxy/main/scripts/install.sh | bash
```

Building from source on device also works:

```bash
pkg install golang
git clone https://github.com/aaaa0ggMC/codex-proxy.git
cd codex-proxy && go build -o "$PREFIX/bin/codex-proxy" .
```

Nothing here is Termux-specific: the same flags, environment variables, and routes behave
identically on a laptop or a VPS. Termux only needs the `termux-wake-lock` treatment if you
want the proxy to survive Android's background process killer.

## Run

```bash
codex-proxy --port 6769
```

By default the server binds to `127.0.0.1` with no proxy API key.

To listen on a non-loopback interface, you must set a proxy API key. Prefer the environment variable on shared systems because `--api-key` can appear in shell history and process lists:

```bash
CODEX_PROXY_API_KEY='replace-with-a-long-random-key' codex-proxy --host 0.0.0.0 --port 6769
```

You can also pass `--api-key` directly. OpenAI-compatible clients should set `OPENAI_API_KEY` to the same value; they will send it as `Authorization: Bearer <OPENAI_API_KEY>`.

To enable web search by default for all incoming requests:

```bash
codex-proxy --port 6769 --web-search
# or via environment variable:
CODEX_PROXY_WEB_SEARCH=1 codex-proxy --port 6769
```

## Build from source

```bash
go run . --port 6769
```

or:

```bash
go build -o codex-proxy .
./codex-proxy --port 6769
```

`scripts/build.sh` wraps the same rebuild into one step and drops the binary at the repo root,
which is where the MCPHub service entry points:

```bash
./scripts/build.sh          # -> ./codex-proxy
OUT="$PREFIX/bin/codex-proxy" ./scripts/build.sh
```

The binary is git-ignored, so after every `git pull` run the script again before restarting the
service. A service entry whose `command` points at a binary that was never built exits
immediately and shows up as disconnected in MCPHub.

## Client configuration

Most OpenAI-compatible clients can use:

```bash
export OPENAI_BASE_URL=http://127.0.0.1:6769/v1
export OPENAI_API_KEY=dummy
```

The proxy ignores the incoming API key unless a proxy API key is configured; then `OPENAI_API_KEY` must match the proxy API key.

Logs include request metadata, status, bytes, and duration. Request and response bodies are not logged.

## Examples

If a proxy API key is configured, add `-H 'Authorization: Bearer <api-key>'` to these `curl` examples.

List models:

```bash
curl http://127.0.0.1:6769/v1/models
```

Responses API:

```bash
curl http://127.0.0.1:6769/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-5.4-mini","input":"Reply with exactly: pong"}'
```

Streaming Responses API:

```bash
curl http://127.0.0.1:6769/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-5.4-mini","stream":true,"input":"Reply with exactly: pong"}'
```

Chat Completions API:

```bash
curl http://127.0.0.1:6769/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-5.4-mini","messages":[{"role":"user","content":"Reply with exactly: pong"}]}'
```

Streaming chat completions:

```bash
curl http://127.0.0.1:6769/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-5.4-mini","stream":true,"messages":[{"role":"user","content":"Reply with exactly: pong"}]}'
```

Web search (Chat Completions):

```bash
# Using standard web_search_options
curl http://127.0.0.1:6769/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-5.5","web_search_options":{},"messages":[{"role":"user","content":"What is the latest world news today?"}]}'

# Or using -search model alias
curl http://127.0.0.1:6769/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-5.5-search","messages":[{"role":"user","content":"What is the latest world news today?"}]}'

# Or using tools
curl http://127.0.0.1:6769/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-5.5","tools":[{"type":"web_search"}],"messages":[{"role":"user","content":"What is the latest world news today?"}]}'
```

Web search (Responses API):

```bash
curl http://127.0.0.1:6769/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-5.5","tools":[{"type":"web_search"}],"input":"What is the latest world news today?"}'
```

## Quota and usage

`GET /v1/usage` reports what is left of the Codex quota. The ChatGPT backend meters two windows,
a five hour one and a weekly one, and both are reported:

```bash
curl -s http://127.0.0.1:6769/v1/usage
```

```json
{
  "plan_type": "plus",
  "allowed": true,
  "limit_reached": false,
  "windows": {
    "five_hour": {
      "label": "5h", "short_label": "5h", "slot": "primary",
      "used_percent": 0, "remaining_percent": 100, "window_seconds": 18000,
      "reset_after_seconds": 17434, "resets_at": "2026-09-16T05:30:59Z"
    },
    "weekly": {
      "label": "weekly", "short_label": "7d", "slot": "secondary",
      "used_percent": 94, "remaining_percent": 6, "window_seconds": 604800,
      "reset_after_seconds": 318452, "resets_at": "2026-09-19T17:07:57Z"
    }
  },
  "credits": { "has_credits": false, "unlimited": false, "balance": "0" },
  "fetched_at": "2026-09-16T00:40:25Z",

  "remaining_percent": 6,
  "remaining_label": "7d",
  "five_hour_remaining_percent": 100,
  "weekly_remaining_percent": 6
}
```

The windows are keyed by how long they last, so a client does not have to know which slot the
plan happens to meter. Percentages are 0-100 and mean *remaining*, not used.

### Picking one number

Clients that can bind exactly one JSON key to one number (RikkaHub's usage field, for example)
should use the flat values at the end of the payload:

- `remaining_percent` is the window closest to running out, and `remaining_label` says which one
  it is (`5h` or `7d`). This is the value that decides whether you can keep working.
- `five_hour_remaining_percent` and `weekly_remaining_percent` isolate one window each. Bind one
  of these when the widget should always show that window's gauge.

They are plain numbers, so no arithmetic is needed on the client side. Pick one key and bind it;
the nested `windows` object stays available for a dashboard that can show both.

### Other formats

```bash
curl -s 'http://127.0.0.1:6769/v1/usage?format=text'                     # 5h 100% · 7d 6%
curl -s 'http://127.0.0.1:6769/v1/usage?format=number'                   # 6
curl -s 'http://127.0.0.1:6769/v1/usage?format=number&window=five_hour'  # 100
```

`format` accepts `json` (default), `text` and `number`; `window` accepts `tightest` (default),
`five_hour` and `weekly`. Reports are cached for 15 seconds, and `?refresh=1` skips the cache.
`CODEX_PROXY_USAGE_TTL_SECONDS` changes that lifetime (`0` always asks the backend).

Every other response also carries the last known quota as headers, so a client can show it
without calling `/v1/usage` at all:

```text
X-Codex-Usage-Remaining: 6
X-Codex-Usage-5h: 100
X-Codex-Usage-Weekly: 6
```

The headers come from the cache and never trigger a backend request, so a chat request is never
delayed by the quota lookup; they are simply absent until the first `/v1/usage` call.

## Remote access

By default `codex-proxy` only listens on `127.0.0.1`. The safest way to use it remotely is still an SSH tunnel:

```bash
ssh -L 6769:127.0.0.1:6769 user@your-vps
```

If you intentionally expose it from a VPS, bind to a public interface and require a proxy API key:

```bash
CODEX_PROXY_API_KEY='replace-with-a-long-random-key' codex-proxy --host 0.0.0.0 --port 6769
```

## Supported routes

- `GET /healthz`
- `GET /v1/usage`
- `GET /v1/models`
- `POST /v1/responses`
- `POST /v1/chat/completions`

## Current limitations

Unsupported:

- embeddings
- images generation
- audio
- files
- batches
- fine-tuning
- full optional-field parity across every OpenAI response variant

Function tools, web search (`web_search` and `web_search_preview`), `tool_choice`, and chat `response_format` are translated where the Codex backend supports them.

Some OpenAI parameters are accepted from clients but intentionally stripped before the Codex backend call, including `temperature`, `top_p`, `stop`, `max_tokens`, `max_output_tokens`, `max_completion_tokens`, and `user`. Upstream `store` is always forced to `false`.
