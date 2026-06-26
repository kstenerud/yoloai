// ABOUTME: OpenCode plugin that mirrors turn state into yoloai's agent-status.json
// ABOUTME: so OpenCode is hook-authoritative: session.status busy → active, idle → idle.

// OpenCode auto-loads plugins from ~/.config/opencode/plugins/. This one
// subscribes to the event stream and reuses status-monitor.py's --write-status
// CLI (so the agent-status.json schema stays single-sourced) rather than
// re-deriving status. The session.status event carries the authoritative turn
// state ({status:{type:"busy"|"idle"}}); message.updated/session.idle are NOT
// used because message.updated also fires *after* the turn completes, which would
// stick the status at "active".
export const YoloaiStatus = async ({ $ }) => {
  const dir = process.env.YOLOAI_DIR || "/yoloai";
  const write = (status) =>
    $`python3 ${dir}/bin/status-monitor.py --write-status ${status} ${dir}/agent-status.json`
      .nothrow()
      .quiet();
  return {
    event: async ({ event }) => {
      if (event?.type !== "session.status") return;
      const type = event.properties?.status?.type;
      if (type === "idle") await write("idle");
      else if (type === "busy") await write("active");
    },
  };
};
