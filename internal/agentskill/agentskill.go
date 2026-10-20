// Package agentskill holds the SKILL.md that teaches AI agents (OpenClaw, Hermes Agent) to use
// KyDrive's MCP tools. Each instance serves the copy matching its own tools.
package agentskill

import _ "embed"

// KyDrive is kydrive/SKILL.md, served at /agent/skills/kydrive/SKILL.md.
//
//go:embed kydrive/SKILL.md
var KyDrive []byte
