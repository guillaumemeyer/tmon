// tmon-status.ts — tmon <-> prime-agent status bridge.
//
// Installed by `tmon hooks install prime` into ~/.prime/agent/extensions/.
// Prime Agent auto-discovers *.ts files there and hot-reloads them with
// /reload; no config merge and no trust ceremony are needed.
//
// This extension is a passive observer: it never blocks or modifies tool
// calls. On lifecycle events it writes one small JSON state file under the
// tmon hook state dir, keyed by a sha256 of the session file. The tmon
// prime connector joins those files to daemon-list rows by sessionFile,
// which gives the dashboard exact mid-turn tool names and turn boundaries.
//
// Prime Agent has no permission event, so a permission wait (a tool_call
// paused on ctx.ui.confirm in another extension) still reads "working"
// here — the daemon's own signals and tmon's pane heuristic cover that.
//
// Apply with /reload (or start a new session).

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, unlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";

// Install renders the state dir here as a JSON string literal, so the
// quotes live in the replacement, not in this template.
const STATE_DIR = __TMON_HOOK_STATE_DIR__;

export default function (pi: ExtensionAPI) {
  let sessionFile: string | undefined;

  const key = (s: string) => createHash("sha256").update(s).digest("hex");

  const write = (status: string, detail: string) => {
    if (!sessionFile) return;
    try {
      mkdirSync(STATE_DIR, { recursive: true });
      writeFileSync(
        join(STATE_DIR, key(sessionFile) + ".json"),
        JSON.stringify({ status, detail, sessionFile }),
      );
    } catch {
      // A state write must never break the agent.
    }
  };

  const clear = () => {
    if (!sessionFile) return;
    try {
      const p = join(STATE_DIR, key(sessionFile) + ".json");
      if (existsSync(p)) unlinkSync(p);
    } catch {
      // Best-effort cleanup.
    }
  };

  const bind = (ctx: { sessionManager: { getSessionFile(): string | null | undefined } }) => {
    sessionFile = ctx.sessionManager.getSessionFile() ?? undefined;
  };

  pi.on("session_start", async (_event, ctx) => {
    bind(ctx);
  });

  pi.on("agent_start", async (_event, ctx) => {
    bind(ctx);
    write("working", "active");
  });

  pi.on("tool_execution_start", async (event, ctx) => {
    bind(ctx);
    write("working", "tool:" + event.toolName);
  });

  pi.on("turn_end", async (_event, ctx) => {
    bind(ctx);
    write("idle", "turn-complete");
  });

  pi.on("agent_end", async (_event, ctx) => {
    bind(ctx);
    write("idle", "turn-complete");
  });

  pi.on("session_shutdown", async () => {
    clear();
  });
}
