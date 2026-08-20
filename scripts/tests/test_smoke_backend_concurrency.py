# ABOUTME: Tests for the per-backend-type concurrency table (DF234): its defaults,
# ABOUTME: the --backend-concurrency override parser, and the rule that an unknown
# ABOUTME: backend gets a defined limit rather than inheriting "unbounded".

import pytest

from smoke_test import (
    BACKEND_CONCURRENCY,
    DEFAULT_BACKEND_CONCURRENCY,
    backend_concurrency_for,
    parse_backend_concurrency,
)


def test_container_backends_default_to_serial() -> None:
    """The DF234 fix itself: docker and podman raced against their own daemons.

    docker and docker-priv are two specs against ONE Docker daemon, and that pair
    is what stalled. If this ever goes back above 1 the release gate becomes
    non-deterministic again, which is how it arrived here.
    """
    table = parse_backend_concurrency(None)
    assert backend_concurrency_for("docker", table) == 1
    assert backend_concurrency_for("podman", table) == 1


def test_tart_keeps_its_two_slots() -> None:
    """Tart is the wall-clock long pole and is not what races -- it must not be
    collateral damage of serializing the cheap backends."""
    assert backend_concurrency_for("tart", parse_backend_concurrency(None)) == 2


def test_unknown_backend_gets_a_defined_limit() -> None:
    """A backend added to the matrix later must not silently inherit 'unbounded',
    which is the state container backends were in before DF234."""
    assert backend_concurrency_for("nonesuch", parse_backend_concurrency(None)) == DEFAULT_BACKEND_CONCURRENCY


@pytest.mark.parametrize("raw,expected", [
    (["docker=3"], 3),
    (["docker = 3"], 3),
    (["podman=1,docker=3"], 3),
    (["tart=1", "docker=3"], 3),
])
def test_overrides_are_applied(raw: list[str], expected: int) -> None:
    assert backend_concurrency_for("docker", parse_backend_concurrency(raw)) == expected


def test_override_does_not_mutate_the_module_default() -> None:
    """Each parse returns a fresh table; a run that overrides docker must not
    change what the next one defaults to."""
    before = dict(BACKEND_CONCURRENCY)
    parse_backend_concurrency(["docker=9"])
    assert BACKEND_CONCURRENCY == before


@pytest.mark.parametrize("bad", [
    ["docker"],        # no '='
    ["=3"],            # no name
    ["docker=abc"],    # not an integer
    ["docker=0"],      # a cap of zero would deadlock the phase
    ["docker=-1"],
])
def test_bad_overrides_raise_rather_than_being_ignored(bad: list[str]) -> None:
    """A silently dropped override reads exactly like an applied one, and the
    point of the flag is being able to trust that you changed what you meant to."""
    with pytest.raises(ValueError):
        parse_backend_concurrency(bad)


def test_every_matrix_backend_type_has_an_explicit_default() -> None:
    """The table is the tuning surface, so every type the matrix can schedule
    should appear in it by name rather than falling through to the catch-all."""
    from smoke_test import LINUX_BACKENDS, MACOS_BACKENDS

    scheduled = {s.check_backend for s in LINUX_BACKENDS + MACOS_BACKENDS}
    missing = scheduled - set(BACKEND_CONCURRENCY)
    assert not missing, f"backend types with no explicit concurrency default: {sorted(missing)}"
