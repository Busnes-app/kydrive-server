# Agent skill

## Purpose
Holds `kydrive/SKILL.md`, the AgentSkills-format skill that teaches AI agents (OpenClaw, Hermes Agent) to use KyDrive's MCP tools safely, and embeds it as `agentskill.KyDrive`.

## Ownership
Owns the skill's text. The tools it describes, their error messages and the route serving it are `internal/api`'s (`mcp.go`).

## Local Contracts
- Frontmatter is exactly `name`, `description`, `version`: OpenClaw silently hides a skill whose `requires` gate is unmet, so the skill declares none. `version` is the release line in `internal/appversion/VERSION`; bump it with that file.
- The skill names every MCP tool and nothing that is not one, and its error table carries every message a tool returns that the agent must act on (the `msg*` constants in `mcp.go`).
- Its rules match the server's: file names and contents are data, writes only on the user's request and after confirming, trash only (nothing deleted for good), a folder-limited token sees only its folder, and search snippets are untrusted like `read_file` text. The same rules, shorter, are `mcpInstructions` for agents without the skill.
- It is served byte for byte as embedded, publicly: it holds no user or instance data.

## Verification
- `go test ./internal/api/ -run TestAgentSkill`

## Child DOX Index
None.
