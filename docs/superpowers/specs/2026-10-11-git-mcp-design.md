# git-mcp Design Spec

Read-only git operations as an MCP server for AI agents.

## Problem

AI agents need to search and read source code — for incident diagnosis, code review, architecture analysis, and more. Giving agents direct filesystem access is an exfiltration risk. Air-gapped approaches (send logs in, get findings back) are safe but sacrifice knowledge — the agent can't follow call chains, understand business logic, or connect errors to specific code paths.

git-mcp provides a middle ground: a read-only git operations server that gives agents controlled, audited access to source code over MCP. Code stays on the server. Agents search, read, and explore through a defined tool interface. Every access is authenticated and logged.

## Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Language | Go | Infrastructure service managing disk, subprocesses, concurrency. Single binary deployment. |
| Git operations | Shell out to `git` CLI | Battle-tested, full feature coverage. Gitaly (GitLab) validated this approach at scale over library alternatives (go-git, libgit2). |
| Repo storage | Bare mirror clones on local disk | Shared across agents. No checkout needed — all read operations work on bare repos. |
| Indexing | None (git grep) | No Zoekt/vector index. `git grep` on local bare repos is fast enough. Avoids extra infrastructure. |
| Session/handles | Stateless | No server-side sessions. Each tool call specifies `repo` + `ref`. Repo cache is an internal optimization, invisible to callers. |
| Auth model | Service-to-service | git-mcp authenticates services (not end users). Services handle their own user-level authorization. |
| Auth protocol | JWT (OAuth 2.1 + SPIFFE compatible) | Both are standard JWTs. Issuers configured explicitly; identity is `(iss, sub)` pair. `aud` must equal `git-mcp`. |
| Access control | Static YAML config | Maps service identity → allowed repos. Simple, sufficient for thousands of services. |
| Upstream credentials | One read-only PAT per git host | git-mcp holds credentials. Access scoping happens in the YAML config, not at the credential level. |
| Transport | Streamable HTTP + stdio | HTTP for production (remote agents). stdio for local development (plug into Claude Code). |

## Architecture

```
┌─────────────────────────────────────────────┐
│                  git-mcp                     │
│                                              │
│  ┌──────────┐  ┌───────────┐  ┌───────────┐ │
│  │ Transport │  │   Auth    │  │   Audit   │ │
│  │ HTTP/stdio│  │ JWT→YAML  │  │   Logger  │ │
│  └────┬─────┘  └─────┬─────┘  └─────┬─────┘ │
│       │               │              │       │
│  ┌────▼───────────────▼──────────────▼─────┐ │
│  │            Tool Handlers                 │ │
│  │  resolve_ref · search_code · read_file  │ │
│  │  list_files · git_log · git_show        │ │
│  └────────────────┬────────────────────────┘ │
│                   │                          │
│  ┌────────────────▼────────────────────────┐ │
│  │          Repo Cache Manager              │ │
│  │  bare mirrors · lazy fetch · LRU evict  │ │
│  └────────────────┬────────────────────────┘ │
│                   │                          │
│              git CLI (subprocess)            │
└─────────────────────────────────────────────┘
         │                          │
    local disk                 upstream git
   (bare mirrors)          (GitHub/GitLab PAT)
```

Single Go binary with four internal layers:

1. **Transport** — Streamable HTTP + stdio. Routes MCP JSON-RPC to tool handlers.
2. **Auth + Audit** — JWT validation (OAuth / SPIFFE), maps identity to service name, checks YAML permissions, logs every call.
3. **Tool Handlers** — Six MCP tools. Each constructs and executes a `git` command scoped to a repo + SHA.
4. **Repo Cache Manager** — Manages bare mirrors on disk. Fetches on demand, evicts under disk pressure.

## Repo Cache Manager

### Directory Layout

```
/data/repos/
  github.com/
    org/
      checkout-service.git/
      payment-service.git/
  gitlab.com/
    team/
      api-gateway.git/
```

### Core Operation: Ensure(repo, ref) → SHA

Entry point for every tool call. The resolved SHA is returned in every tool response, so callers that need consistency across multiple calls can switch from a branch name to the resolved SHA after their first call.

1. Validate `ref`: must match `^[0-9a-f]{4,40}$` (hex SHA) or `^[A-Za-z0-9._/-]+$` (branch/tag name). Reject git rev syntax (`sha:path`, `@{...}`, `^{/regex}`).
2. Resolve with `git rev-parse --verify --end-of-options <ref>^{commit}`.
3. Ref not found? → `git fetch` from upstream (including `refs/tags/*`), then resolve.
4. Repo not on disk? → `git clone --bare`, then resolve.
5. Returns resolved SHA. Tools use it for all subsequent git commands.

### Fetch Strategy

Lazy — only fetch when a requested ref can't be resolved locally. No periodic background sync. Note: a branch that already resolves locally will NOT trigger a fetch, so callers see the cached version. Callers that need the latest should use `resolve_ref` and compare SHAs, or pass a specific SHA. Tags and SHAs are immutable and always safe to serve from cache.

### Eviction

- LRU based on last access time per repo.
- Triggered when total disk usage exceeds a configured threshold (e.g., `max_cache_size: 50GB`).
- Evicts least-recently-accessed repos until under threshold.
- Never evicts a repo mid-request (reference counting).
- Runs after each clone or fetch, and at startup. Empty or `0` means no limit.
- Last access is stored as the repo directory mtime, so the order survives a restart.
- An evicted repo is renamed into `<cache_dir>/.trash/` and then deleted, so a new request can clone it again at once.

### Concurrency

- Per-repo mutex: multiple tool calls against the same repo are serialized for fetch, parallel for reads.
- Fetch deduplication: if two requests trigger a fetch for the same repo simultaneously, only one runs, the other waits.

## MCP Tools

Each tool takes `repo` (e.g., `github.com/org/service`) and `ref` (branch, tag, or SHA). Internally, `ref` is resolved to a SHA via the cache manager before executing. Every tool response includes the resolved `sha` so callers can pin subsequent calls.

### resolve_ref

```
git rev-parse --verify --end-of-options <ref>^{commit}
```

- **Inputs:** `repo`, `ref`, `refresh` (optional boolean, default false — when true, fetches from upstream before resolving)
- **Returns:** `{sha}` — the resolved commit SHA
- **Purpose:** allows callers to validate access and pin a SHA at session start without needing to call another tool. Use `refresh: true` to get the latest SHA for a branch that may have moved.

### search_code

```
git grep -n -E -e <pattern> <sha> -- [<pathspec>]
```

- **Inputs:** `repo`, `ref`, `pattern` (regex), `path` (optional filter), `max_matches` (default 50)
- **Returns:** list of `{path, line_number, line_text}`
- **Guards:** max matches capped server-side to prevent unbounded output

### read_file

```
git show <sha>:<path>
```

- **Inputs:** `repo`, `ref`, `path`, `start_line` (optional), `end_line` (optional)
- **Returns:** file contents with 1-based line numbers (format: `line_number\ttext`), line range slicing applied server-side
- **Guards:** rejects binary files, max file size limit (default 1MB)

### list_files

```
git ls-tree --name-only <sha> <path>
```

- **Inputs:** `repo`, `ref`, `path` (optional, defaults to root)
- **Returns:** list of `{name, type}` (file or directory)
- **Options:** `recursive` flag available, non-recursive by default

### git_log

```
git log --first-parent --format=<format> <sha> -- [<pathspec>]
```

- **Inputs:** `repo`, `ref`, `count` (default 20, max 100), `path` (optional pathspec)
- **Returns:** list of `{sha, author, date, message}`
- Always `--first-parent` for cleaner history

### git_show

```
git show --first-parent --format=<format> <commit_sha>
```

- **Inputs:** `repo`, `ref`, `commit` (the specific commit SHA to show — validated with the same ref validation rules)
- **Returns:** `{sha, author, date, message, diff}`
- **Guards:** diff output size capped (default 500KB), optional pathspec filter

### git_diff

```
git diff <base_sha> <head_sha> [-- <path>]        # merge_base=false (default)
git diff <base_sha>...<head_sha> [-- <path>]      # merge_base=true
```

- **Inputs:** `repo`, `base`, `head` (both validated with the ref rules), `path` (optional), `merge_base` (optional boolean, default false — when true, diff from the common ancestor, like a pull request)
- **Returns:** `{base_sha, head_sha, diff, truncated}`
- **Guards:** diff output size capped (`max_diff_size`), cut at a UTF-8 boundary

### git_blame

```
git blame --porcelain [-L <start>,<end>] <sha> -- <path>
```

- **Inputs:** `repo`, `ref`, `path`, `start_line` (optional), `end_line` (optional)
- **Returns:** `{sha, lines: [{line, commit, text}], commits: {<commit_sha>: {author, date, summary}}, truncated}` — commit details appear once per commit, not once per line
- **Guards:** output size capped (`max_output_size`); a partial last record is dropped

## Security Hardening

Applied to all tools:

- **Ref validation:** `ref` must match `^[0-9a-f]{4,40}$` (hex SHA) or `^[A-Za-z0-9._/-]+$` (branch/tag name). Git rev syntax (`sha:path`, `@{...}`, `^{/regex}`) is rejected. Resolved with `--end-of-options`.
- **Argument injection prevention:** `path` and `pattern` are validated. No absolute paths, no `.git` directory access. Patterns passed with `-e` to prevent option parsing. All git commands use `--` to separate options from arguments.
- **Symlink confinement:** symlinks resolved within repo boundary.
- **Environment sanitization:** no credential leaks via `GIT_*` environment variables.
- **Timeouts:** per-command timeout (default 30s) to prevent hanging operations.
- **Output limits:** all tools cap output size to prevent memory exhaustion.
- **Process cleanup:** on timeout, the whole git process group is killed, including helpers such as `git-remote-http`.

### Accepted Risks

Reviewed on 2026-10-11. No code change; revisit if the assumptions change.

- **Git config and hooks from upstream.** A clone does not copy the upstream's config or hooks. A test upstream with `core.fsmonitor`, a `diff.<driver>.textconv` command and a matching `.gitattributes` ran no command during `clone`, `show`, `log -p`, `grep` or `fetch`. `.gitattributes` can name a driver, but the command must come from config, and global/system config is disabled (`GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_NOSYSTEM=1`). **Assumption:** only git-mcp can write to `cache_dir`. Anyone who can write there can set repo config that runs commands.
- **Commits outside the ref's history.** `git_show` (and any tool given a SHA) accepts any commit in the cached repo, not only ancestors of `ref`. Access control is per repo, so this gives no access beyond what `resolve_ref` on another branch gives. Note: `fetch --prune` removes refs but not objects, so commits from deleted or force-pushed branches stay readable by SHA until git garbage-collects them. Upstreams such as GitHub also serve such commits by SHA. If the cache must match the upstream exactly, run `git gc --prune=now` after a fetch that removed refs.
- **Fetch URL of cached repos.** Fetch uses the `origin` URL saved at clone time. After a change to `base_url`, cached repos keep the old URL until they are evicted or the cache is cleared.

## Auth & Access Control

### Config File

```yaml
upstream:
  github.com:
    token: "${GITHUB_PAT}"
  gitlab.com:
    token: "${GITLAB_TOKEN}"
  git.internal.example:              # optional base_url replaces "https://<host>"
    base_url: "http://gitserver:8080"   # http + token logs a startup warning

auth:
  audience: "git-mcp"
  issuers:
    - name: "corp-oauth"
      iss: "https://oauth.example.com"
      jwks_uri: "https://oauth.example.com/.well-known/jwks.json"
    - name: "spire"
      iss: "https://spire.example.com"
      jwks_uri: "https://spire.example.com/keys"

services:
  diagnostics-agent:
    issuer: "spire"
    subject: "spiffe://example.com/service/diagnostics"
    repos:
      - "github.com/org/checkout-service"
      - "github.com/org/payment-service"

  sre-bot:
    issuer: "corp-oauth"
    subject: "sre-bot-client-id"
    repos:
      - "github.com/org/*"
```

### Auth Flow

1. Extract Bearer token from `Authorization` header (HTTP). Skip for stdio (trusted local).
2. Decode JWT (unverified) to read `iss` claim.
3. Look up issuer in config by `iss` value. Reject if unknown.
4. Validate signature against that issuer's JWKS.
5. Validate `aud` contains `git-mcp` (from `auth.audience`). Validate `exp`/`nbf`.
6. Match `(iss, sub)` pair → service name in config.
7. Check if requested repo matches the service's allowed repos. A repo path that is not clean (`.`, `..`, `//`) never matches.
8. Auth failure (steps 1–6) → HTTP 401 with `WWW-Authenticate: Bearer realm="git-mcp"` (plus `error="invalid_token"` for a bad token). Repo denied (step 7) → MCP tool error `access denied to repo <repo>` (HTTP 200, `isError: true`). Allow → proceed. Every tool call is logged (see Audit Log).
9. The check fails closed: with auth on, a call without an authenticated service is denied.

### Audit Log

Structured JSON, one line per tool call. Callers pass a correlation ID via the `X-Request-Id` header (or W3C `traceparent`), logged alongside both the requested ref and the resolved SHA:

```json
{"time":"2026-10-11T14:30:00Z","level":"INFO","msg":"tool_call","service":"diagnostics-agent","request_id":"req-abc123","tool":"read_file","repo":"github.com/org/checkout-service","ref":"v2.7.0","resolved_sha":"a1b2c3d4...","path":"src/handler.ts","duration_ms":12}
```

Denied and failed calls are logged too, with an `error` field. For `git_diff`, `ref` is `<base>...<head>`.

## Deployment

### CLI

```
git-mcp serve --config config.yaml          # Streamable HTTP
git-mcp serve --config config.yaml --stdio  # stdio (local dev)
```

### Requirements

- `git` CLI on PATH (2.38+)
- Local disk for repo cache (SSD recommended)
- No database, no external services beyond upstream git hosts

### Configuration

```yaml
server:
  listen: "0.0.0.0:8080"
  cache_dir: "/data/repos"
  max_cache_size: "50GB"
  max_file_size: "1MB"
  max_matches: 50
  max_diff_size: "500KB"
  max_output_size: "1MB"         # search_code, list_files, git_log, git_blame
  git_timeout: "30s"
```

### Container

See `Dockerfile`: a static Go build on `alpine` with `git`, running as a non-root user (uid 10001), with a `HEALTHCHECK` on `/health`. The default command is `serve --config /etc/git-mcp/config.yaml`.

Mount a persistent volume at `/data/repos` for the cache, writable only by git-mcp (see Accepted Risks). Config via file; `${VAR}` references in it are expanded from the environment.

### Kubernetes

StatefulSet with persistent volume, or a Deployment with a PVC. Single replica is sufficient — the cache is an optimization, cold start is a fetch.

### Health Check

`GET /health` — returns `{"status":"ok"}` without auth. Cache stats are not exposed yet.

## Future Additions (Not in MVP)

- `find_symbols` — find definitions (functions, types, methods) instead of text, e.g. `{name, kind, path, line, scope}`, or list a file's symbols as an outline. Analysis (2026-10-11):
  - **universal-ctags** (preferred): one extra binary in the image, 100+ languages, no CGO.
  - **tree-sitter**: more exact, but Go bindings need CGO and a grammar per language.
  - Both need the files at a SHA exported from the bare repo (e.g. `git archive` to a temp dir), a per-SHA result cache counted in `max_cache_size`, and the usual timeout and output limits, since the parser runs on untrusted code.
  - Deferred until real use shows that `search_code` with a regex is not enough.
- `/health` cache stats (repo count, disk usage, uptime)
- Repo management API (list cached repos, force refresh, evict)
- Webhook-triggered cache refresh (on push events)
- Multi-replica with shared storage
