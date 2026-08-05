# tmon demo — multi-agent fleet + dashboard popup
# https://github.com/charmbracelet/vhs
#
# Requires: tmux, grok, claude, hermes, and tmon loaded in your tmux.conf
# (prefix a a opens the dashboard; this tape assumes prefix = C-a).
#
# Render:
#   vhs docs/demo/demo.tape

Output docs/demo/demo.gif
Output docs/demo/demo.mp4

Require tmux
Require grok
Require claude
Require hermes

Set Shell "bash"
Set FontSize 14
Set Width 1400
Set Height 900
Set TypingSpeed 40ms
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful
Set WindowBarSize 40
Set Padding 12
Set Margin 20
Set BorderRadius 8
Set Framerate 24
Set PlaybackSpeed 1

# ── Setup (hidden): fresh "tmon demo" session with 3×3 agent panes ───────────

Hide

Type@5ms `tmux kill-session -t "tmon demo" 2>/dev/null || true`
Enter
Sleep 300ms

# Window 1 — Grok Build (3 panes)
Type@5ms `tmux new-session -d -s "tmon demo" -n "Grok Build" -c "$HOME/code/tmon"`
Enter
Type@5ms `tmux split-window -t "tmon demo:Grok Build" -h -c "$HOME/code/tmon"`
Enter
Type@5ms `tmux split-window -t "tmon demo:Grok Build" -v -c "$HOME/code/tmon"`
Enter
Type@5ms `tmux select-layout -t "tmon demo:Grok Build" tiled`
Enter
Type@5ms `tmux list-panes -t "tmon demo:Grok Build" -F "#{pane_id}" | while read -r p; do tmux send-keys -t "$p" "grok" Enter; done`
Enter

# Window 2 — Claude Code (3 panes)
Type@5ms `tmux new-window -t "tmon demo" -n "Claude Code" -c "$HOME/code/tmon"`
Enter
Type@5ms `tmux split-window -t "tmon demo:Claude Code" -h -c "$HOME/code/tmon"`
Enter
Type@5ms `tmux split-window -t "tmon demo:Claude Code" -v -c "$HOME/code/tmon"`
Enter
Type@5ms `tmux select-layout -t "tmon demo:Claude Code" tiled`
Enter
Type@5ms `tmux list-panes -t "tmon demo:Claude Code" -F "#{pane_id}" | while read -r p; do tmux send-keys -t "$p" "claude" Enter; done`
Enter

# Window 3 — Hermes (3 panes)
Type@5ms `tmux new-window -t "tmon demo" -n "Hermes" -c "$HOME/code/tmon"`
Enter
Type@5ms `tmux split-window -t "tmon demo:Hermes" -h -c "$HOME/code/tmon"`
Enter
Type@5ms `tmux split-window -t "tmon demo:Hermes" -v -c "$HOME/code/tmon"`
Enter
Type@5ms `tmux select-layout -t "tmon demo:Hermes" tiled`
Enter
Type@5ms `tmux list-panes -t "tmon demo:Hermes" -F "#{pane_id}" | while read -r p; do tmux send-keys -t "$p" "hermes" Enter; done`
Enter

# Land on Grok Build so the attach view opens on the Grok fleet
Type@5ms `tmux select-window -t "tmon demo:Grok Build"`
Enter
Type@5ms `tmux select-pane -t "tmon demo:Grok Build.1"`
Enter

# Give the nine agents a moment to boot before we attach
Sleep 8s

Show

# ── Attach to the demo session ───────────────────────────────────────────────

Type@20ms `tmux attach -t "tmon demo"`
Enter
Sleep 1s

# ── Prompt one Grok agent in /plan mode (approval-ready plan) ────────────────

# Ensure we are on Grok Build, pane 1
Ctrl+A
Sleep 200ms
Type@10ms "1"
Sleep 400ms

Type@25ms "/plan Design a small status-bar improvement for tmon: show a short pulse animation when an agent flips from idle to working. Write a concrete plan with files, steps, and risks. When the plan is ready, open it in approval mode so I can review and approve it before any implementation."
Enter
Sleep 1s

# ── Prompt one Claude agent to ask clarifying questions ──────────────────────

Ctrl+A
Sleep 200ms
Type@10ms "2"
Sleep 400ms

Type@25ms "I want to redesign how tmon surfaces blocked agents in the dashboard. Before you propose anything or change code, ask me clarifying questions about scope, UX priorities, and constraints so we align first."
Enter

# ── Hold so work / blocks can register in the status bar ─────────────────────

Sleep 3s

# ── Open the tmon popup: prefix → a → a ──────────────────────────────────────
# (tmux prefix is C-a in this environment; @tmon-dashboard-key defaults to "a")

Ctrl+A
Sleep 250ms
Type@50ms "a"
Sleep 250ms
Type@50ms "a"

# Let the dashboard breathe so the fleet is visible in the recording
Sleep 6s

# ── Teardown (hidden) ────────────────────────────────────────────────────────

Hide
Escape
Sleep 200ms
# Detach if still attached, then kill the demo session
Ctrl+A
Sleep 200ms
Type@10ms "d"
Sleep 500ms
Type@5ms `tmux kill-session -t "tmon demo" 2>/dev/null || true`
Enter
Sleep 300ms
Show
