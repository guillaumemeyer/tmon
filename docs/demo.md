# tmon demo GIF — shot list

The demo GIF embedded in the README hero is **one asciinema cast of the core
loop**, converted with `agg`. This document is the shot list — record it
yourself in a live tmux session; nothing here is automated.

**Target:** under 60 seconds, no audio, 100×30 (or your terminal's natural
size), output at `docs/demo.gif` (that's the path the README embeds).

## Setup

- tmux with tmon loaded and `@tmon-pane-tint on` — the pane glow reads
  beautifully on video and shows the feature off.
- Two panes: one running an agent that is actively working, one running an
  agent that can be made to block on a permission prompt (a `[y/N]`, a plan
  approval, a `PermissionRequest`).
- `asciinema` to record, `agg` to convert
  (`https://github.com/asciinema/agg`).

```bash
asciinema rec demo.cast
# ... perform the shots below, in real time ...
# exit asciinema (Ctrl-d or exit), then:
agg demo.cast docs/demo.gif --cols 100 --rows 30 --font-size 14
```

## Shots

| # | Time | What the viewer sees |
|---|------|----------------------|
| 1 | 0–8s | **Establish.** Both agents working. The status bar shows `🤖-⚡️2` and both panes carry the subtle green working tint. Let it sit — a couple of seconds of calm sells the "quiet monitor" idea. |
| 2 | 8–20s | **The block.** The second agent hits a permission prompt. Its pane glows the blocked tint; the status bar flips to `🤖-🚨1-⚡️1`. Hold on the moment for a beat — this is the feature. |
| 3 | 20–30s | **The unblock.** Approve the prompt (`y` / `Enter`). The pane tint clears to green again and the status bar returns to `🤖-⚡️2`. The loop is closed: block → spot → unblock → back to work. |
| 4 | 30–46s | **Dashboard tour.** `prefix a a`. Scroll through the agent list (`j`/`k`), pause on one with a context bar (`ctx 52.4k/200k (26%)`), then hit `/` and fuzzy-search for something (e.g. a project name) to show search. |
| 5 | 46–56s | **Jump & settle.** `Enter` on the selected agent to jump to its pane (the viewer sees the focused pane pop into view), then close the dashboard. End on the settled status bar. |

## Notes

- Keep narration-free: let the pane tint + status bar tell the story.
- If the blocked pane's prompt text is sensitive, use a toy prompt or a
  scratch project.
- Re-record until it's tight; the whole thing is under a minute, so redoing
  it is cheap.
