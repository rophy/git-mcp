# git-mcp

An [MCP](https://modelcontextprotocol.io) server that gives AI agents read-only, audited access to git repositories.

git-mcp keeps bare mirrors of upstream repos on local disk and answers tool calls with the `git` CLI. Every call names a `repo` and a `ref` and returns the resolved commit SHA, so agents can pin a version and get consistent results. The server keeps no sessions.

## Tools

| Tool | Purpose |
|---|---|
| `resolve_ref` | Resolve a branch, tag or SHA to a commit SHA. `refresh: true` fetches from upstream first. |
| `search_code` | Regex search (`git grep -E`) with file paths and line numbers. |
| `read_file` | Read a file with line numbers; optional line range. |
| `list_files` | List files and directories, optionally recursive. |
| `git_log` | Commit history, optionally for one path. Page with `count` (max 100) and `skip`. `first_parent: false` also lists commits inside merged branches. |
| `git_show` | A commit's metadata and diff. |
| `git_diff` | Diff between two refs; `merge_base: true` diffs from their common ancestor, like a pull request. |
| `git_blame` | The commit that last changed each line of a file; optional line range. |

### Result format

Every tool returns a JSON object. On success, the MCP result has the object in `structuredContent`, and the SDK also puts the same JSON, as a string, in one `content` text block (for clients that do not read `structuredContent`). So the response carries the data twice; read one of them. On error, `isError` is `true` and `content` has one text block with the message; there is no `structuredContent`.

| Tool | `structuredContent` | Where code text appears |
|---|---|---|
| `resolve_ref` | `{sha}` | — |
| `search_code` | `{sha, matches: [{path, line_number, line_text}], truncated}` | `line_text`: one matched line |
| `read_file` | `{sha, content}` | `content`: one string; each line is `<number>\t<line>\n` |
| `list_files` | `{sha, entries: [{name, type}], truncated}` | — |
| `git_log` | `{sha, commits: [{sha, author, date, message}], truncated}` | — |
| `git_show` | `{sha, author, date, message, diff, truncated}` | `diff`: unified diff |
| `git_diff` | `{base_sha, head_sha, diff, truncated}` | `diff`: unified diff |
| `git_blame` | `{sha, lines: [{line, commit, text}], commits: {<sha>: {author, date, summary}}, truncated}` | `text`: one source line |

`sha` is the commit that `ref` resolved to. `truncated: true` means an output limit cut the result; `read_file` returns an error instead when the file is larger than `max_file_size`.

A branch that already exists in the cache is not fetched again. Use `resolve_ref` with `refresh: true` to get the latest commit. Tags and SHAs are served from the cache.

## Run

```sh
git-mcp serve --config config.yaml          # Streamable HTTP on server.listen (default 0.0.0.0:8080)
git-mcp serve --config config.yaml --stdio  # stdio, for local use; no authentication
```

With Docker:

```sh
docker build -t git-mcp .
docker run -p 8080:8080 \
  -v "$PWD/config.yaml:/etc/git-mcp/config.yaml:ro" \
  -v git-mcp-cache:/data/repos \
  git-mcp
```

The image runs as a non-root user (uid 10001) and has a health check on `GET /health`.

Requirements: `git` 2.38 or newer on `PATH`, and local disk for the cache.

## Configuration

```yaml
server:
  listen: "0.0.0.0:8080"
  cache_dir: "/data/repos"
  max_cache_size: "50GB"      # evict least recently used repos above this; empty or 0 = no limit
  max_file_size: "1MB"        # read_file
  max_diff_size: "500KB"      # git_show, git_diff
  max_output_size: "1MB"      # search_code, list_files, git_log, git_blame
  max_matches: 50             # search_code
  git_timeout: "30s"          # per git command

upstream:
  github.com:
    token: "${GITHUB_TOKEN}"  # read-only token; ${VAR} is expanded from the environment
  git.example.internal:
    base_url: "http://gitserver:8080"   # optional; default is https://<host>

auth:
  audience: "git-mcp"
  issuers:
    - name: "corp-oauth"
      iss: "https://oauth.example.com"
      jwks_uri: "https://oauth.example.com/.well-known/jwks.json"

services:
  sre-bot:
    issuer: "corp-oauth"
    subject: "sre-bot-client-id"
    repos:
      - "github.com/org/*"            # every repo directly under github.com/org
      - "github.com/other/one-repo"
```

Sizes accept `B`, `KB`, `MB` and `GB` (base 1024, any case). An invalid size stops startup.

A repo name is `<host>/<path>`, for example `github.com/org/repo`. Its clone URL is `https://<host>/<path>`, or `<base_url>/<path>` when `base_url` is set. git-mcp logs a warning at startup if a token would be sent over plain `http://`.

## Authentication and access

In HTTP mode, each request needs `Authorization: Bearer <JWT>`. git-mcp checks the token against the JWKS of its `iss` issuer, requires `aud` to match `auth.audience` and `exp` to be valid, and maps the `(issuer, sub)` pair to a service. The service's `repos` list controls which repos it can read.

- A missing or invalid token gets HTTP 401 with a `WWW-Authenticate` header.
- A repo that the service may not read gets the MCP tool error `access denied to repo <repo>`.
- If `auth.issuers` is empty, authentication is off and git-mcp logs a warning. Use this only on a trusted network.

## Audit log

git-mcp writes one JSON line to stderr for each tool call, including denied and failed calls:

```json
{"time":"...","level":"INFO","msg":"tool_call","service":"sre-bot","request_id":"req-123","tool":"read_file","repo":"github.com/org/api","ref":"main","resolved_sha":"a1b2...","path":"src/main.go","duration_ms":4}
```

Send `X-Request-Id` (or a W3C `traceparent`) to correlate calls.

## Security notes

- Refs, paths, repo names and patterns are validated: no option-like values, no `..`, no absolute paths, no `.git` access.
- Output size, git run time and cache size are limited. On timeout the whole git process group is killed.
- The cache directory must be writable only by git-mcp. See "Accepted Risks" in the [design spec](docs/superpowers/specs/2026-10-11-git-mcp-design.md) for what was reviewed and why.

## Development

```sh
make              # list targets
make unit-test    # go test with coverage
make e2e-test     # build the image and run e2e tests in docker compose
```

The e2e setup in `e2e/` runs a test git server, the git-mcp container and a Go test container that calls git-mcp through MCP with signed JWTs.

See the [design spec](docs/superpowers/specs/2026-10-11-git-mcp-design.md) for details and future work.
