# MCP Library — Community Catalog

This directory contains the community-curated catalog of [MCP (Model Context Protocol)](https://modelcontextprotocol.io) servers available in the Bifrost MCP Library.

**Anyone can add an MCP server** by editing [`servers.json`](./servers.json) and opening a pull request.

---

## Quick Start

### 1. Fork and clone the repository

```bash
git clone https://github.com/<your-username>/bifrost.git
cd bifrost
```

### 2. Add your server to `community/mcp-library/servers.json`

Append your entry to the `servers` array in **alphabetical order by `slug`**. See [Server Entry Reference](#server-entry-reference) below.

Make sure your entry is valid JSON and follows the schema — check the field reference table and the existing entries for guidance.

### 3. Open a pull request

Target the `dev` branch. Use a descriptive title like: `community: add <server-name> to MCP library`.

---

## Server Entry Reference

Each server entry is a JSON object in the `servers` array. Here's a complete example:

### HTTP / SSE Server

```json
{
  "slug": "my-server",
  "name": "My Server",
  "description": "A short, factual description of what this server does.",
  "category": "Developer Tools",
  "connection_type": "http",
  "connection_url": "https://api.example.com/mcp",
  "auth_type": "headers",
  "required_header_keys": ["Authorization"],
  "icon_url": "https://example.com/icon.png",
  "docs_url": "https://docs.example.com",
  "publisher": "Your Name or Organization",
  "version": "1.0.0",
  "tags": ["api", "example"]
}
```

### STDIO Server

```json
{
  "slug": "my-stdio-server",
  "name": "My STDIO Server",
  "description": "A local STDIO-based MCP server.",
  "category": "Developer Tools",
  "connection_type": "stdio",
  "stdio_config": {
    "command": "npx",
    "args": ["-y", "@example/mcp-server"],
    "envs": ["API_KEY"]
  },
  "auth_type": "none",
  "docs_url": "https://github.com/example/mcp-server",
  "publisher": "Your Name",
  "version": "1.0.0",
  "tags": ["local", "example"]
}
```

### Field Reference

| Field | Type | Required | Description |
| ----- | ---- | -------- | ----------- |
| `slug` | `string` | ✅ | Unique, URL-safe identifier (`kebab-case`). **Never change after merge.** |
| `name` | `string` | ✅ | Human-readable display name. |
| `description` | `string` | | Short summary of what the server does. |
| `category` | `string` | | Grouping category (e.g. `Developer Tools`, `Data & Analytics`, `Communication`). |
| `connection_type` | `string` | ✅ | One of: `http`, `stdio`, `sse`, `inprocess`. |
| `connection_url` | `string` | For `http`/`sse` | The server's endpoint URL. |
| `stdio_config` | `object` | For `stdio` | Launch configuration (see below). |
| `auth_type` | `string` | | One of: `none` (default), `headers`, `oauth`, `per_user_oauth`, `per_user_headers`. |
| `required_header_keys` | `string[]` | | Header names required for authentication. **Never include secret values.** |
| `icon_url` | `string` | | URL to a square icon (PNG or SVG). |
| `docs_url` | `string` | | Link to documentation or homepage. |
| `publisher` | `string` | | Person or organization maintaining the server. |
| `version` | `string` | | Server version (e.g. `1.0.0`). |
| `tags` | `string[]` | | Freeform tags for search and filtering. |
| `metadata` | `object` | | Arbitrary key/value data. Use sparingly. |

#### `stdio_config` Fields

| Field | Type | Required | Description |
| ----- | ---- | -------- | ----------- |
| `command` | `string` | ✅ | Executable to launch (e.g. `npx`, `uvx`, `node`, `python`). |
| `args` | `string[]` | | Arguments passed to the command. |
| `envs` | `string[]` | | Environment variable **names** the user must provide. **Never include values.** |

---

## Guidelines

### Do

- ✅ Use a descriptive, unique `slug` in `kebab-case` (e.g. `github`, `slack-bot`, `postgres-readonly`).
- ✅ Write a clear, concise `description`.
- ✅ Include a `docs_url` so users can learn more.
- ✅ Verify your entry is valid JSON and matches the [Field Reference](#field-reference) before submitting.
- ✅ One server per pull request for easier review.

### Don't

- ❌ **Never include secrets, tokens, or API keys** anywhere in the entry.
- ❌ Don't change the `slug` of an existing entry — it's the primary key used for syncing.
- ❌ Don't add duplicate entries — search the file for your server first.
- ❌ Don't add servers that require custom binaries not installable via standard package managers.

---

## Categories

To keep the library organized, reuse existing categories when possible:

| Category | Examples |
| -------- | -------- |
| `Developer Tools` | GitHub, GitLab, filesystem, code search |
| `Data & Analytics` | PostgreSQL, BigQuery, Snowflake |
| `Communication` | Slack, Discord, email |
| `Productivity` | Google Drive, Notion, Jira |
| `AI & ML` | Hugging Face, Replicate |
| `Cloud & Infrastructure` | AWS, GCP, Kubernetes |
| `Web & Search` | Brave Search, Google Search |
| `Security` | Vault, CrowdStrike |
| `Other` | Anything that doesn't fit above |

If none of these fit, use a short, descriptive new category and note it in your PR description.

---

## How Syncing Works

1. You submit a PR adding your server to `servers.json`.
2. Maintainers review and merge into the `dev` branch.
3. The Bifrost platform periodically fetches `servers.json` from the `dev` branch.
4. Your server appears in the MCP Library for all Bifrost users.

Syncing is **additive** — existing entries are updated by `slug`, and new entries are inserted. The sync runs automatically on a configurable interval.

---

## Schema Validation

The [`schema.json`](./schema.json) file contains a [JSON Schema (draft-07)](https://json-schema.org) definition for `servers.json`. CI will validate all changes against this schema before merge.

---

## Questions?

- Open an [issue](https://github.com/maximhq/bifrost/issues/new) for questions or problems.
- See the [Bifrost docs](https://docs.getbifrost.ai) for general platform documentation.
