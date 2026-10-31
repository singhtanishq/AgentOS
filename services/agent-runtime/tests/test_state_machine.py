"""Execution state machine unit tests."""

from __future__ import annotations

import pytest

from app.core.state_machine import (
    CANCELLED,
    FAILED,
    QUEUED,
    RUNNING,
    SUCCEEDED,
    TERMINAL,
    TIMED_OUT,
    WAITING_APPROVAL,
    TransitionError,
    assert_transition,
    can_transition,
)


def test_queued_can_only_run_or_cancel() -> None:
    assert can_transition(QUEUED, RUNNING)
    assert can_transition(QUEUED, CANCELLED)
    assert not can_transition(QUEUED, SUCCEEDED)
    assert not can_transition(QUEUED, FAILED)


def test_running_paths() -> None:
    for status in (WAITING_APPROVAL, SUCCEEDED, FAILED, CANCELLED, TIMED_OUT):
        assert can_transition(RUNNING, status)
    assert not can_transition(RUNNING, QUEUED)


def test_waiting_approval_can_resume() -> None:
    assert can_transition(WAITING_APPROVAL, RUNNING)
    assert can_transition(WAITING_APPROVAL, CANCELLED)
    assert not can_transition(WAITING_APPROVAL, SUCCEEDED)


def test_terminal_states_are_absorbing() -> None:
    for terminal in TERMINAL:
        for target in (QUEUED, RUNNING, WAITING_APPROVAL, SUCCEEDED, FAILED, CANCELLED, TIMED_OUT):
            assert not can_transition(terminal, target)


def test_assert_transition_raises() -> None:
    with pytest.raises(TransitionError):
        assert_transition(SUCCEEDED, RUNNING)
    with pytest.raises(TransitionError):
        assert_transition(QUEUED, "made_up_state")
