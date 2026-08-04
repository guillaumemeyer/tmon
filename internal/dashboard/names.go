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
