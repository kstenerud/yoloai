# ABOUTME: Tests for the rule-1 gate — that it fires on a removed user-visible
# ABOUTME: name, stays quiet on a move, and knows where its registries live.

from __future__ import annotations

import sys
from pathlib import Path

_REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(_REPO / "scripts"))

from check_breaking_changes import (  # noqa: E402
    CONFIG_KEY,
    CONFIG_KEYS_FILE,
    FLAG_DECL,
    _vanished,
    config_keys_at,
    flags_at,
)


# --- the extractors ----------------------------------------------------------


def test_config_keys_are_extracted_from_the_registry() -> None:
    keys = set(CONFIG_KEY.findall('var knownSettings = []knownSetting{\n\t{"os", "linux"},\n\t{"tart.image", ""},\n}'))
    assert keys == {"os", "tart.image"}


def test_a_flag_declaration_is_found_in_every_cobra_spelling() -> None:
    for line, want in (
        ('rootCmd.PersistentFlags().Bool("debug", false, "x")', "debug"),
        ('cmd.Flags().StringVar(&x, "agent", "", "x")', "agent"),
        ('rootCmd.PersistentFlags().CountP("verbose", "v", "x")', "verbose"),
        ('cmd.Flags().StringSliceVarP(&m, "mount", "m", nil, "x")', "mount"),
    ):
        assert FLAG_DECL.findall(line) == [want], line


def test_reading_a_flag_is_not_declaring_one() -> None:
    """The bug this allowlist exists for. With `\\w+` as the verb, GetBool matches,
    so a flag renamed at its declaration stays in the set via its readers and the
    gate goes silently blind — which is exactly what it did before being probed."""
    for line in (
        'debug, _ := cmd.Flags().GetBool("debug")',
        'v, _ := cmd.Flags().GetCount("verbose")',
        'cmd.Flags().Changed("agent")',
        'cmd.Flags().Lookup("json")',
    ):
        assert FLAG_DECL.findall(line) == [], line


# --- the registries are where this thinks they are ---------------------------


def test_the_registries_are_where_this_thinks() -> None:
    """Pins both oracles. _vanished stays silent when its head set is empty, so a
    relocated registry would quietly retire the gate rather than fire 20 false
    removals; this is what makes that silence safe — moving either registry fails
    a test, at the person doing the moving. Same split as D122's research root."""
    assert (_REPO / CONFIG_KEYS_FILE).is_file(), f"{CONFIG_KEYS_FILE} moved; update CONFIG_KEYS_FILE"
    assert len(config_keys_at("HEAD")) > 5, "config-key registry yields almost nothing — shape changed"
    assert len(flags_at("HEAD")) > 20, "CLI flag scan yields almost nothing — internal/cli/ moved or the spelling changed"


# --- the decision ------------------------------------------------------------


def test_a_removed_name_is_reported() -> None:
    assert _vanished({"backend", "agent"}, {"agent"}) == {"backend"}


def test_a_name_that_merely_moved_is_not_removed() -> None:
    """Sets are tree-wide, so a declaration moving between files is a non-event.
    A line-oriented diff check would fire on every refactor."""
    assert _vanished({"agent", "model"}, {"model", "agent"}) == set()


def test_an_empty_head_set_is_a_lost_oracle_not_a_mass_deletion() -> None:
    assert _vanished({"os", "agent", "model"}, set()) == set()


def test_an_added_name_is_not_a_break() -> None:
    assert _vanished({"agent"}, {"agent", "new_key"}) == set()
