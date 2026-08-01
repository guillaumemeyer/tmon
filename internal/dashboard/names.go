package dashboard

// agentFullName maps a signature label to the name shown in the popup list
// (ported from the bash plugin).
func agentFullName(label string) string {
	switch label {
	case "Grok":
		return "Grok Build"
	case "Claude":
		return "Claude Code"
	case "Codex":
		return "Codex CLI"
	case "Cursor":
		return "Cursor"
	case "Cline":
		return "Cline"
	case "Aider":
		return "Aider"
	case "Copilot":
		return "Copilot"
	case "CodeBuddy":
		return "CodeBuddy"
	case "Windsurf":
		return "Windsurf"
	case "Hermes":
		return "Hermes Agent"
	case "OpenClaw":
		return "OpenClaw"
	default:
		return label
	}
}

// agentIcon returns the per-agent emoji. The bash popup defined this mapping
// but never rendered it; the Go popup shows it so agents are scannable at a
// glance.
func agentIcon(label string) string {
	switch label {
	case "Grok":
		return "🧠" // deep understanding ("grok")
	case "Claude":
		return "🏛️" // classical / Anthropic aesthetic
	case "Codex":
		return "📖" // codex = ancient manuscript / book
	case "Cursor":
		return "🖱️" // the editor is named after the cursor
	case "Cline":
		return "🔧" // a tool / VS Code extension
	case "Aider":
		return "🤝" // "aider" = "to help" in French
	case "Copilot":
		return "👨‍✈️" // pilot / copilot
	case "CodeBuddy":
		return "🧑‍💻" // coding buddy / developer
	case "Windsurf":
		return "🏄" // windsurfing
	case "Hermes":
		return "🪶" // Hermes' winged sandals / messenger
	case "OpenClaw":
		return "🦞" // claw
	default:
		return "[@]" // generic AI fallback
	}
}
