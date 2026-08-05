# tmon demo GIF

The demo GIF embedded in the README hero is produced with
[VHS](https://github.com/charmbracelet/vhs) from the tape in this directory.

**Output:** `docs/demo/demo.gif` (path the README embeds).

## Record

Prerequisites: `vhs` (plus `ttyd` and `ffmpeg`), a Chromium-family browser
(for go-rod frame capture), `tmux`, and the agents the tape launches
(`grok`, `claude`, `hermes`). tmon must be loaded in your tmux config
(`prefix a a` opens the dashboard; the tape assumes prefix `C-a`).

**Browser note:** VHS auto-downloads Chromium on linux/amd64. On **aarch64**
that download often fails. Prefer a system `chromium`/`google-chrome`, or
install Playwright’s binary (`npx playwright install chromium`).
`demo.sh` will pick up `~/.cache/ms-playwright/**/chrome-linux/chrome`
automatically when nothing is on `PATH`.

If Chromium fails with “No usable sandbox” (common on Ubuntu 23.10+ with
AppArmor userns restrictions), `demo.sh` sets `VHS_NO_SANDBOX=1` for you.
You can also export that yourself when invoking `vhs` directly.

From the repo root:

```bash
./docs/demo/demo.sh
```

Or invoke VHS directly (Chromium must already be on `PATH`):

```bash
vhs docs/demo/demo.tape
```

## What the tape does

| Step | What the viewer sees |
|------|----------------------|
| Setup | Session `"tmon demo"` with windows **Grok Build**, **Claude Code**, and **Hermes** — three agent panes each |
| Prompts | One Grok agent gets a `/plan` request (open plan in approval mode); one Claude agent is asked to clarify first |
| Dashboard | `prefix a a` opens the tmon popup over the fleet |

See [`demo.tape`](demo.tape) for the full script.

## Manual shot list (optional)

If you prefer a live asciinema capture of the core loop instead of VHS:

- tmux with tmon loaded (pane border status on by default)

- Agents that can work and block (permission / plan approval)
- Convert with [`agg`](https://github.com/asciinema/agg)

```bash
asciinema rec demo.cast
# ... perform the shots in real time ...
agg demo.cast docs/demo/demo.gif --cols 100 --rows 30 --font-size 14
```
