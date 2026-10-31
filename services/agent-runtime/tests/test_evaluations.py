"""Evaluation rule engine tests."""

from __future__ import annotations

import pytest

from app.engine.evaluations import EvaluationError, evaluate_rules


def test_contains_any() -> None:
    rules = [{"type": "contains_any", "values": ["revenue", "profit"]}]
    assert evaluate_rules("Revenue grew 14%", rules)[0].passed
    assert not evaluate_rules("Nothing relevant here", rules)[0].passed


def test_contains_all_and_not_contains() -> None:
    rules = [
        {"type": "contains_all", "values": ["Q3", "14%"]},
        {"type": "not_contains", "values": ["confidential"]},
    ]
    results = evaluate_rules("Q3 revenue grew 14% per the report", rules)
    assert all(r.passed for r in results)
    results = evaluate_rules("Q3 grew 14%; confidential addendum follows", rules)
    assert results[0].passed and not results[1].passed


def test_regex_and_min_length() -> None:
    rules = [
        {"type": "regex", "pattern": r"\d{1,3}%"},
        {"type": "min_length", "value": 10},
    ]
    assert all(r.passed for r in evaluate_rules("growth of 14% year over year", rules))
    results = evaluate_rules("short", rules)
    assert not results[0].passed and not results[1].passed


def test_json_valid() -> None:
    rules = [{"type": "json_valid"}]
    assert evaluate_rules('{"answer": 42}', rules)[0].passed
    assert not evaluate_rules("not json", rules)[0].passed


def test_unknown_rule_type_fails_loudly() -> None:
    with pytest.raises(EvaluationError):
        evaluate_rules("x", [{"type": "vibes"}])


def test_invalid_regex_fails_loudly() -> None:
    with pytest.raises(EvaluationError):
        evaluate_rules("x", [{"type": "regex", "pattern": "("}])
