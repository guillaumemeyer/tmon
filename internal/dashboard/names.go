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

// agentIdentityColor returns a brand color for the agent label so list rows
// and the preview header are recognizable at a glance. Empty means fall back
// to the theme accent (unknown labels). Colors are fixed brand constants, not
// theme palette slots.
func agentIdentityColor(label string) string {
	switch label {
	case "Claude":
		return "#D97757"
	case "Codex":
		return "#10B981"
	case "Hermes":
		return "#22D3EE"
	case "Grok":
		return "#A78BFA"
	case "Cursor":
		return "#E879F9"
	case "Copilot":
		return "#79C0FF"
	case "Cline":
		return "#FBBF24"
	case "CodeBuddy":
		return "#2DD4BF"
	case "Windsurf":
		return "#38BDF8"
	case "Aider":
		return "#A3E635"
	case "OpenClaw":
		return "#FB7185"
	default:
		return ""
	}
}

// agentDisplayName renders the agent's name in the popup: when the agent's
// session has a title it is shown as "Title (Name)", otherwise just the
// name. The title comes from the connector (Grok's generated_title, Claude's
// session name) and falls back to the plain name when unknown.
//
// Hermes with a known profile renders as "Hermes - <profile>" (or
// "Title (Hermes - <profile>)") so multi-home installs are distinguishable.
func agentDisplayName(r Row) string {
	name := agentFullName(r.Label)
	if r.Label == "Hermes" && r.Profile != "" {
		name = "Hermes - " + r.Profile
	}
	if r.Title == "" {
		return name
	}
	return r.Title + " (" + name + ")"
}
