# ABOUTME: Tests for the citation-provenance hook. The false-accusation cases
# ABOUTME: matter most: a hook that cries wolf gets disabled, and a disabled hook
# ABOUTME: is worse than none.
"""Tests for scripts/check_citation_provenance.py.

The hook's whole value rests on one distinction — a tool *input* naming a file
means the session went and looked, a tool *result* containing the file's name
means something merely told it about the file — so that is what most of these
pin. The second group covers the ways a citation can appear without being a new
claim (a reflow, a move, authoring the doc itself); each is a false accusation,
and false accusations are how a hook earns its way out of settings.json.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from check_citation_provenance import RESEARCH_ROOT, cited_research_docs, unread_citations

_DOC = "docs/contributors/design/research/llm-shaped-repos.md"
_REPO = Path(__file__).resolve().parent.parent.parent


def _with_research_docs(tmp_path: Path, *names: str) -> Path:
    """Create a repo-shaped tmp dir whose research root holds the given docs."""
    root = tmp_path / RESEARCH_ROOT
    root.mkdir(parents=True, exist_ok=True)
    for name in names:
        (root / name).write_text("body\n", encoding="utf-8")
    return tmp_path


def _transcript(tmp_path: Path, tool_uses: list[tuple[str, dict[str, Any]]]) -> str:
    """Write a JSONL transcript containing the given (tool_name, input) calls."""
    path = tmp_path / "transcript.jsonl"
    lines = []
    for name, tool_input in tool_uses:
        lines.append(json.dumps({
            "message": {"content": [{"type": "tool_use", "name": name, "input": tool_input}]}
        }))
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")
    return str(path)


def _edit(transcript: str, new_string: str, old_string: str = "", file_path: str = "notes.md",
          cwd: str | None = None) -> dict[str, Any]:
    # Defaults to the real repo, where _DOC genuinely exists — the existence
    # filter is part of what these cases exercise, so faking it would hollow them.
    return {
        "tool_name": "Edit",
        "tool_input": {"file_path": file_path, "old_string": old_string, "new_string": new_string},
        "transcript_path": transcript,
        "cwd": cwd if cwd is not None else str(_REPO),
    }


# --- the extractor -----------------------------------------------------------

def test_matches_a_research_citation_however_it_is_spelled() -> None:
    assert cited_research_docs("see research/llm-shaped-repos.md") == {"llm-shaped-repos.md"}
    assert cited_research_docs(f"see `{_DOC}`") == {"llm-shaped-repos.md"}
    assert cited_research_docs("nothing to see here") == set()


def test_a_name_that_resolves_to_no_file_is_not_a_citation(tmp_path: Path) -> None:
    """Prose about citations is not a citation. The hook's first live firing was
    on `research/x.md` in D122's own text, which is the decision that explains the
    hook — a placeholder, naming nothing, unreadable by construction."""
    repo = _with_research_docs(tmp_path, "real-doc.md")
    root = repo / RESEARCH_ROOT
    text = "distinguish `research/x.md` from `research/real-doc.md`"
    assert cited_research_docs(text, root) == {"real-doc.md"}


def test_the_research_root_exists(tmp_path: Path) -> None:
    """Pin the corpus path. If research docs move, this fails loudly instead of
    the hook quietly matching nothing forever — DF94's failure mode, where a check
    nothing could trigger reported green for months."""
    assert (_REPO / RESEARCH_ROOT).is_dir(), f"{RESEARCH_ROOT} moved; re-point the hook"
    assert list((_REPO / RESEARCH_ROOT).glob("*.md")), "research corpus is empty"


def test_a_placeholder_citation_does_not_accuse(tmp_path: Path) -> None:
    t = _transcript(tmp_path, [])
    payload = _edit(t, "context carries no bit separating a read of `research/x.md` from a claim about it")
    assert unread_citations(payload) == []


# --- the load-bearing distinction --------------------------------------------

def test_an_unopened_citation_is_reported(tmp_path: Path) -> None:
    t = _transcript(tmp_path, [("Read", {"file_path": "/repo/decisions/working-notes.md"})])
    payload = _edit(t, f"The confused-deputy risk is documented in `{_DOC}`.")
    assert unread_citations(payload) == ["llm-shaped-repos.md"]


def test_reading_the_doc_clears_it(tmp_path: Path) -> None:
    t = _transcript(tmp_path, [("Read", {"file_path": f"/repo/{_DOC}"})])
    payload = _edit(t, f"The confused-deputy risk is documented in `{_DOC}`.")
    assert unread_citations(payload) == []


def test_a_tool_result_mentioning_the_doc_does_not_count_as_reading_it(tmp_path: Path) -> None:
    """The exact defect: a decision quotes the research doc's name, and that
    quote arrives in a tool RESULT. Context is flat, so the agent cannot feel the
    difference. The hook must."""
    path = tmp_path / "transcript.jsonl"
    path.write_text(json.dumps({
        "message": {"content": [
            {"type": "tool_use", "name": "Read", "input": {"file_path": "/repo/working-notes.md"}},
            {"type": "tool_result", "content": f"D42 — see {_DOC} for the confused-deputy analysis"},
        ]}
    }) + "\n", encoding="utf-8")
    payload = _edit(str(path), f"The confused-deputy risk is documented in `{_DOC}`.")
    assert unread_citations(payload) == ["llm-shaped-repos.md"]


def test_writing_the_citation_does_not_count_as_reading_it(tmp_path: Path) -> None:
    """Without excluding write tools, the Edit under test satisfies the check by
    containing the very citation that triggered it."""
    t = _transcript(tmp_path, [("Edit", {"file_path": "notes.md", "new_string": f"see {_DOC}"})])
    payload = _edit(t, f"The confused-deputy risk is documented in `{_DOC}`.")
    assert unread_citations(payload) == ["llm-shaped-repos.md"]


def test_grep_and_bash_count_as_going_to_look(tmp_path: Path) -> None:
    for name, tool_input in (
        ("Grep", {"pattern": "deputy", "path": _DOC}),
        ("Bash", {"command": f"sed -n '1,40p' {_DOC}"}),
        ("Task", {"prompt": f"Read {_DOC} and summarize the deputy section"}),
    ):
        t = _transcript(tmp_path, [(name, tool_input)])
        payload = _edit(t, f"documented in `{_DOC}`")
        assert unread_citations(payload) == [], f"{name} should count as looking"


# --- false accusations -------------------------------------------------------

def test_a_reflow_carrying_an_existing_citation_is_not_a_new_claim(tmp_path: Path) -> None:
    t = _transcript(tmp_path, [("Read", {"file_path": "/repo/other.md"})])
    payload = _edit(
        t,
        new_string=f"The confused-deputy risk is documented in `{_DOC}` (see there).",
        old_string=f"Documented in `{_DOC}`.",
    )
    assert unread_citations(payload) == []


def test_authoring_the_research_doc_is_not_citing_it(tmp_path: Path) -> None:
    t = _transcript(tmp_path, [("Read", {"file_path": "/repo/unrelated.md"})])
    payload = {
        "tool_name": "Write",
        "tool_input": {"file_path": f"/repo/{_DOC}", "content": "# LLM-shaped repos\n\nBody."},
        "transcript_path": t,
    }
    assert unread_citations(payload) == []


def test_a_non_write_tool_is_ignored(tmp_path: Path) -> None:
    t = _transcript(tmp_path, [])
    assert unread_citations({"tool_name": "Bash", "tool_input": {"command": f"cat {_DOC}"},
                             "transcript_path": t}) == []


def test_a_missing_transcript_stays_quiet(tmp_path: Path) -> None:
    payload = _edit(str(tmp_path / "nope.jsonl"), f"documented in `{_DOC}`")
    assert unread_citations(payload) == []


def test_an_edit_citing_nothing_stays_quiet(tmp_path: Path) -> None:
    t = _transcript(tmp_path, [])
    assert unread_citations(_edit(t, "A paragraph with no citations at all.")) == []
