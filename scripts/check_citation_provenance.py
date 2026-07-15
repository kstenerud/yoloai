#!/usr/bin/env python3
# ABOUTME: PostToolUse hook. Refuses a citation of a research doc that nothing in
# ABOUTME: the session ever opened, because context is flat: it carries no bit
# ABOUTME: separating "I read this" from "something told me what it says".
"""Check that a research doc being cited was actually opened this session.

Every agent error in the session that motivated this had one shape: a summary was
in context and the source was not. An agent wrote a confident claim into a finding
citing a research doc it had never opened, having read only a one-line description
in a decision that cited it. The finding was wrong. Nothing mechanical caught it.

A rule can't fix that, because the failure's defining property is that it feels
like knowledge — there is no moment of doubt for a "check your sources" rule to
fire on. But provenance is observable *outside* the agent: either the session
opened the file or it didn't, and the transcript says which.

Hence the load-bearing asymmetry here: only tool *inputs* count as opening a file,
never tool *results*. Reaching for a path is an act; having its content arrive in
your context because some other file quoted it is not. That is exactly the
distinction the original defect turned on, so scanning results would make this
hook agree with the mistake it exists to catch.

Scoped to `research/*.md` path citations on purpose. Sampling 200 commits: 3 add a
research path, 101 add a D<n>/DF<n> citation and 19 of those are pure moves. A gate
that fires on every other commit and is wrong a fifth of the time gets switched
off, and a disabled gate is worse than none. D<n>/DF<n> provenance stays uncovered
and review-caught; see docs/contributors/design/plans/agent-error-gates.md.
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path
from typing import Any

# A research citation, whether written bare ("research/x.md") or fully qualified
# ("docs/contributors/design/research/x.md"). Only the basename is captured, so
# the two spellings compare equal.
RESEARCH_CITATION = re.compile(r"(?:[\w./-]*/)?research/([\w.-]+\.md)")

# Tools whose input means the session went and looked. Write/Edit are excluded
# deliberately and not merely as an oversight guard: writing a path is not reading
# it, and without this the Edit currently being checked would satisfy the check by
# containing the very citation that triggered it.
READ_TOOLS = frozenset({"Read", "Grep", "Glob", "Bash", "Task", "Agent", "WebFetch"})

WRITE_TOOLS = ("Write", "Edit")

# Where research docs live. A citation naming no file here is not a citation: it
# is prose about citations, and this hook's first live firing was against exactly
# that — the `research/x.md` placeholder in D122's own text, which explains the
# hook. You cannot fail to read a file that does not exist, so there is nothing to
# establish and the hook must stay quiet. test_the_research_root_exists pins this
# path, so moving the corpus fails a test rather than silently retiring the check.
RESEARCH_ROOT = Path("docs/contributors/design/research")


def cited_research_docs(text: str, root: Path | None = None) -> set[str]:
    """Return the basenames of every research doc cited in text.

    With a root, only names that resolve to a real file there survive: a citation
    of a doc that does not exist cannot be a claim about its contents.
    """
    names = {m.group(1) for m in RESEARCH_CITATION.finditer(text)}
    if root is None:
        return names
    return {name for name in names if (root / name).is_file()}


def read_tool_inputs(transcript_path: Path) -> str | None:
    """Concatenate the inputs of every read-ish tool call in the transcript.

    Returns one blob rather than a parsed set of paths: a path can reach a tool
    input as a bare name, an absolute path, or a fragment of a shell command, and
    a substring test over the basename catches all three. Over-matching here is
    the safe direction — it yields a false pass, where under-matching yields a
    false accusation, and an accusation is what gets a hook disabled.

    None means the transcript could not be read at all, which is distinct from an
    empty blob: an unreadable transcript is no evidence either way, while a
    readable one with no reads in it is evidence of not having looked.
    """
    chunks: list[str] = []
    try:
        with transcript_path.open(encoding="utf-8") as f:
            for line in f:
                entry = _load(line)
                if entry is None:
                    continue
                message = entry.get("message")
                if not isinstance(message, dict):
                    continue
                content = message.get("content")
                if not isinstance(content, list):
                    continue
                for block in content:
                    if not isinstance(block, dict) or block.get("type") != "tool_use":
                        continue
                    if block.get("name") not in READ_TOOLS:
                        continue
                    chunks.append(json.dumps(block.get("input", {})))
    except OSError:
        return None
    return "\n".join(chunks)


def _load(line: str) -> dict[str, Any] | None:
    try:
        entry = json.loads(line)
    except json.JSONDecodeError:
        return None
    return entry if isinstance(entry, dict) else None


def unread_citations(payload: dict[str, Any]) -> list[str]:
    """Return the research docs this edit cites that the session never opened."""
    if payload.get("tool_name") not in WRITE_TOOLS:
        return []
    tool_input = payload.get("tool_input")
    if not isinstance(tool_input, dict):
        return []

    added = tool_input.get("content") or tool_input.get("new_string") or ""
    prior = tool_input.get("old_string") or ""
    if not isinstance(added, str) or not isinstance(prior, str):
        return []

    cwd = payload.get("cwd")
    root = (Path(cwd) if isinstance(cwd, str) and cwd else Path.cwd()) / RESEARCH_ROOT

    # Only citations this edit introduces. A reflow that carries an existing
    # citation across, or a move between files, is not a new claim about a source.
    cited = cited_research_docs(added, root) - cited_research_docs(prior, root)

    # Authoring a research doc is not citing one.
    target = tool_input.get("file_path")
    if isinstance(target, str):
        cited -= {Path(target).name}
    if not cited:
        return []

    transcript = payload.get("transcript_path")
    if not isinstance(transcript, str) or not transcript:
        return []
    blob = read_tool_inputs(Path(transcript))
    if blob is None:
        # No evidence either way. This hook may only ever complain about something
        # it can positively establish, so an unreadable transcript means silence.
        return []
    return sorted(doc for doc in cited if doc not in blob)


def reason(unread: list[str]) -> str:
    docs = ", ".join(unread)
    plural = "those files" if len(unread) > 1 else "that file"
    return (
        f"You just cited {docs}, but nothing in this session opened {plural}. "
        f"Every agent error this hook was built from had that exact shape: a summary "
        f"was in context and the source was not, and the claim felt like knowledge "
        f"because a summary reads exactly like one.\n\n"
        f"Open {plural} and check that it says what you claimed. Then either keep the "
        f"citation, correct the claim, or drop the citation — all three are fine. "
        f"Citing a source you have not read is not.\n\n"
        f"If you did consult it another way (a subagent read it, it was open in an "
        f"earlier session), say so explicitly and carry on."
    )


def main() -> int:
    payload = _load(sys.stdin.read())
    if payload is None:
        return 0
    unread = unread_citations(payload)
    if unread:
        print(json.dumps({"decision": "block", "reason": reason(unread)}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
