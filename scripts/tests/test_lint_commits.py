# ABOUTME: Tests for the commit-message gate. Each rule is proved to REJECT its
# ABOUTME: bad case and ACCEPT the real shapes this repo's history actually uses.
"""Tests for scripts/lint_commits.py.

The negative cases matter more than the positive ones here: a lint that cannot
fail is decoration. Every rule gets an example that must be rejected, drawn from
the mistakes actually observed (PR #36's bodyless `fix(config):`, the bare
`Claude` trailer, the tool's default robot footer).

The acceptance cases are drawn from real commits so the gate cannot quietly
start rejecting the conventions it is supposed to describe.
"""

from __future__ import annotations

from lint_commits import Commit, lint


def _rules(c: Commit) -> set[str]:
    return {p.rule for p in lint([c])}


# --- subject shape -----------------------------------------------------------

def test_accepts_the_real_conventional_subjects() -> None:
    for subject in (
        "fix(config): reject unknown config keys",
        "docs: add AGENTS.md as the contract",
        "feat(github): implement the issue workflow",
        "refactor(auth): one shared auth-presence policy",
        "perf(tart): skip Homebrew auto-update during base provisioning",
        "build(deps): bump goreleaser-action from 6 to 7",
        "fix(config)!: a breaking change marks the type with a bang",
        "ci(release): attest provenance via actions/attest",
    ):
        c = Commit("abc1234", subject, "A body explaining what was wrong.\n")
        assert not _rules(c), f"should accept: {subject}"


def test_rejects_a_type_outside_the_set() -> None:
    # `harden:` appears twice in history and predates the settled set.
    c = Commit("abc1234", "harden: disable core.fsmonitor", "body\n")
    assert "subject-shape" in _rules(c)


def test_rejects_the_superseded_capitalized_convention() -> None:
    # Pre-June-2026 style, still documented in two archive plans — and PR #18,
    # a real outside contribution, is titled exactly this way.
    c = Commit("abc1234", "Feature: add pi agent support", "body\n")
    assert "subject-shape" in _rules(c)


def test_rejects_a_trailing_period() -> None:
    c = Commit("abc1234", "fix(config): reject unknown keys.", "body\n")
    assert "trailing-period" in _rules(c)


def test_accepts_gits_own_revert_subject() -> None:
    # git writes this itself; rejecting it would mean hand-editing a correct message.
    c = Commit("abc1234", 'Revert "fix(config): reject unknown config keys"', "")
    assert not _rules(c)


# --- body --------------------------------------------------------------------

def test_rejects_a_code_commit_with_no_body() -> None:
    # This is PR #36's commit: one of only two bodyless code commits in 437.
    c = Commit("95e5af43", "fix(config): reject unknown config keys", "")
    assert "missing-body" in _rules(c)


def test_trailers_alone_are_not_a_body() -> None:
    c = Commit("abc1234", "feat(x): add a thing",
               "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>\n")
    assert "missing-body" in _rules(c)


def test_docs_commits_may_be_subject_only() -> None:
    # All 9 legitimately bodyless commits in history are trivial doc edits.
    c = Commit("abc1234", "docs: fix a typo", "")
    assert not _rules(c)


# --- trailers ----------------------------------------------------------------

def test_rejects_the_tools_default_robot_footer() -> None:
    c = Commit("abc1234", "fix(x): a fix",
               "Body.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)\n")
    assert "robot-footer" in _rules(c)


def test_rejects_a_bare_claude_trailer() -> None:
    c = Commit("abc1234", "fix(x): a fix",
               "Body.\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n")
    assert "bare-claude-trailer" in _rules(c)


def test_accepts_any_named_model_without_an_allowlist() -> None:
    # The model set is open: Fable 5 and Sonnet 5 both appear in history, so a
    # hardcoded allowlist would reject the next model on the day it ships.
    for model in ("Claude Opus 4.8", "Claude Sonnet 4.5", "Claude Fable 5",
                  "Claude Sonnet 5", "Claude Opus 5.1"):
        c = Commit("abc1234", "fix(x): a fix",
                   f"Body.\n\nCo-Authored-By: {model} <noreply@anthropic.com>\n")
        assert not _rules(c), f"should accept model: {model}"


def test_a_missing_trailer_is_fine() -> None:
    # Humans and dependabot legitimately omit it; presence is not the rule.
    c = Commit("abc1234", "fix(x): a fix", "A human wrote this.\n")
    assert not _rules(c)
