// yoloAI status hook for pi.
//
// Writes ${YOLOAI_DIR}/agent-status.json on agent_start (active) / agent_end
// (idle) so the in-container status monitor's HookDetector can report idle
// with high confidence. Mirrors the Claude Code Stop/PreToolUse hook pattern.

import type { ExtensionAPI } from "@mariozechner/pi-coding-agent";
import { appendFileSync, mkdirSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

const YOLOAI_DIR = process.env.YOLOAI_DIR ?? "/yoloai";
const STATUS_FILE = `${YOLOAI_DIR}/agent-status.json`;
const HOOKS_LOG = `${YOLOAI_DIR}/logs/agent-hooks.jsonl`;

function writeStatus(status: "active" | "idle"): void {
	const ts = Math.floor(Date.now() / 1000);
	try {
		writeFileSync(
			STATUS_FILE,
			JSON.stringify({ status, exit_code: null, timestamp: ts }),
		);
	} catch {
		// best-effort; outside a yoloAI sandbox this directory may not exist
	}
	try {
		mkdirSync(dirname(HOOKS_LOG), { recursive: true });
		appendFileSync(
			HOOKS_LOG,
			`${JSON.stringify({
				ts: new Date().toISOString(),
				level: "info",
				event: `hook.${status}`,
				msg: `pi extension: ${status}`,
				status,
			})}\n`,
		);
	} catch {
		// best-effort
	}
}

export default function (pi: ExtensionAPI): void {
	pi.on("agent_start", () => {
		writeStatus("active");
	});
	pi.on("agent_end", () => {
		writeStatus("idle");
	});
}
