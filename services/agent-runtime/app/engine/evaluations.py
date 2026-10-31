"""Evaluation engine: dataset-driven, rule-based agent evaluation.

Runs execute the agent's model loop against each dataset case and score
the output with deterministic rules:

* ``contains_any``      — output contains at least one of the values
* ``contains_all``      — output contains every value
* ``not_contains``      — output contains none of the values
* ``regex``             — output matches the pattern
* ``min_length``        — output length reaches the minimum
* ``json_valid``        — output parses as a JSON object

Rule scores are objective and deterministic. LLM-as-judge evaluation is
deliberately NOT implemented here: probabilistic scores must never be
presented as ground truth, and the platform labels any future judge
output accordingly (see docs). Each case passes when all its rules pass;
the run's pass rate is the share of passing cases.
"""

from __future__ import annotations

import json
import re
import time
from dataclasses import dataclass
from typing import Any

from app.providers.base import Message, ModelRequest

MAX_CASES_PER_RUN = 50


@dataclass(slots=True)
class RuleResult:
    rule_type: str
    passed: bool
    detail: str


@dataclass(slots=True)
class CaseResult:
    case_id: str
    case_name: str
    passed: bool
    score: float
    output: str
    rules: list[RuleResult]


class EvaluationError(Exception):
    """A dataset-level failure (bad rules, missing dataset, ...)."""


def evaluate_rules(output: str, rules: list[dict[str, Any]]) -> list[RuleResult]:
    """Apply every rule to the output. Unknown rule types fail loudly."""
    results: list[RuleResult] = []
    for rule in rules or []:
        rule_type = str(rule.get("type", ""))
        if rule_type == "contains_any":
            values = [str(v) for v in rule.get("values", [])]
            hit = next((v for v in values if v.lower() in output.lower()), None)
            results.append(RuleResult(rule_type, hit is not None,
                                      f"matched {hit!r}" if hit else f"none of {values} present"))
        elif rule_type == "contains_all":
            values = [str(v) for v in rule.get("values", [])]
            missing = [v for v in values if v.lower() not in output.lower()]
            results.append(RuleResult(rule_type, not missing,
                                      "all present" if not missing else f"missing {missing}"))
        elif rule_type == "not_contains":
            values = [str(v) for v in rule.get("values", [])]
            present = [v for v in values if v.lower() in output.lower()]
            results.append(RuleResult(rule_type, not present,
                                      "none present" if not present else f"found {present}"))
        elif rule_type == "regex":
            pattern = str(rule.get("pattern", ""))
            try:
                matched = re.search(pattern, output) is not None
            except re.error as exc:
                raise EvaluationError(f"invalid regex {pattern!r}: {exc}") from exc
            results.append(RuleResult(rule_type, matched,
                                      "matched" if matched else "no match"))
        elif rule_type == "min_length":
            minimum = int(rule.get("value", 1))
            results.append(RuleResult(rule_type, len(output) >= minimum,
                                      f"length {len(output)} >= {minimum}"
                                      if len(output) >= minimum else
                                      f"length {len(output)} < {minimum}"))
        elif rule_type == "json_valid":
            try:
                parsed = json.loads(output)
                ok = isinstance(parsed, (dict, list))
            except json.JSONDecodeError:
                ok = False
            results.append(RuleResult(rule_type, ok, "valid JSON" if ok else "not valid JSON"))
        else:
            raise EvaluationError(f"unknown rule type: {rule_type!r}")
    return results


class EvaluationRunner:
    """Executes one evaluation run against a published agent version."""

    def __init__(self, db: Any, provider_factory: Any) -> None:
        self._db = db
        self._provider_factory = provider_factory

    async def run(
        self,
        org_id: str,
        dataset_id: str,
        agent_id: str,
        agent_version_id: str,
    ) -> dict[str, Any]:
        """Execute the run; returns a summary with the run id."""
        async with self._db.org_tx(org_id) as conn:
            run_id = await conn.fetchval(
                """
                INSERT INTO evaluation_runs
                    (organization_id, dataset_id, agent_id, agent_version_id, status)
                VALUES ($1, $2, $3, $4, 'running')
                RETURNING id
                """,
                org_id, dataset_id, agent_id, agent_version_id,
            )
            dataset = await conn.fetchrow(
                "SELECT name FROM evaluation_datasets WHERE id = $1 AND deleted_at IS NULL",
                dataset_id,
            )
            if dataset is None:
                await conn.execute(
                    "UPDATE evaluation_runs SET status = 'failed', error = $2::jsonb WHERE id = $1",
                    run_id, json.dumps({"code": "dataset_missing", "message": "dataset not found"}),
                )
                raise EvaluationError("dataset not found")
            cases = await conn.fetch(
                "SELECT id, name, input, rules FROM evaluation_cases "
                "WHERE dataset_id = $1 ORDER BY position, created_at LIMIT $2",
                dataset_id, MAX_CASES_PER_RUN,
            )
            version = await conn.fetchrow(
                "SELECT configuration, status FROM agent_versions WHERE id = $1",
                agent_version_id,
            )
            if version is None or version["status"] != "published":
                await conn.execute(
                    "UPDATE evaluation_runs SET status = 'failed', error = $2::jsonb WHERE id = $1",
                    run_id, json.dumps({"code": "version_not_published",
                                        "message": "agent version must be published"}),
                )
                raise EvaluationError("agent version must be published")
            configuration = json.loads(version["configuration"])
            model_cfg = configuration.get("model", {})
            provider_type = str(model_cfg.get("provider_type", "mock"))
            model_id = str(model_cfg.get("model_id", ""))
            provider = self._provider_factory(org_id, provider_type, model_id)

        started = time.monotonic()
        results: list[CaseResult] = []
        try:
            for case in cases:
                rules = json.loads(case["rules"] or "[]")
                evaluate_rules("warmup", rules)  # validate rules before running
                case_input = case["input"]
                if isinstance(case_input, str):
                    case_input = json.loads(case_input)
                messages = [
                    Message(role="system", content=str(configuration.get("instructions", ""))),
                    Message(role="user", content=str(case_input.get("query", ""))),
                ]
                response = await provider.complete(
                    ModelRequest(model=model_id, messages=tuple(messages),
                                 temperature=model_cfg.get("temperature"),
                                 max_tokens=model_cfg.get("max_tokens"))
                )
                rule_results = evaluate_rules(response.content, rules)
                passed = all(r.passed for r in rule_results) if rule_results else False
                score = (sum(1 for r in rule_results if r.passed) / len(rule_results)
                         if rule_results else 0.0)
                results.append(CaseResult(
                    case_id=str(case["id"]), case_name=str(case["name"]),
                    passed=passed, score=score, output=response.content,
                    rules=rule_results,
                ))
        except EvaluationError as exc:
            async with self._db.org_tx(org_id) as conn:
                await conn.execute(
                    "UPDATE evaluation_runs SET status = 'failed', error = $2::jsonb,"
                    " completed_at = now() WHERE id = $1",
                    run_id, json.dumps({"code": "evaluation_error", "message": str(exc)}),
                )
            raise

        passed_cases = sum(1 for r in results if r.passed)
        pass_rate = (passed_cases / len(results)) if results else 0.0
        async with self._db.org_tx(org_id) as conn:
            for r in results:
                await conn.execute(
                    """
                    INSERT INTO evaluation_results
                        (organization_id, run_id, case_id, passed, score, output, rule_results)
                    VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
                    """,
                    org_id, run_id, r.case_id, r.passed, r.score, r.output,
                    json.dumps([{"type": x.rule_type, "passed": x.passed, "detail": x.detail}
                                for x in r.rules]),
                )
            await conn.execute(
                """
                UPDATE evaluation_runs SET status = 'completed',
                    pass_rate = $2, total_cases = $3, passed_cases = $4, completed_at = now()
                WHERE id = $1
                """,
                run_id, pass_rate, len(results), passed_cases,
            )

        # Regression comparison against the previous completed run.
        async with self._db.org_tx(org_id) as conn:
            previous = await conn.fetchrow(
                """
                SELECT pass_rate, id FROM evaluation_runs
                WHERE dataset_id = $1 AND agent_version_id = $2
                  AND status = 'completed' AND id <> $3
                ORDER BY started_at DESC LIMIT 1
                """,
                dataset_id, agent_version_id, run_id,
            )
        regression = None
        if previous is not None and previous["pass_rate"] is not None:
            delta = float(pass_rate) - float(previous["pass_rate"])
            regression = {
                "previous_run_id": str(previous["id"]),
                "previous_pass_rate": float(previous["pass_rate"]),
                "delta": round(delta, 4),
                "regressed": delta < -0.05,
            }

        return {
            "run_id": str(run_id),
            "dataset": str(dataset["name"]),
            "total_cases": len(results),
            "passed_cases": passed_cases,
            "pass_rate": round(pass_rate, 4),
            "duration_ms": int((time.monotonic() - started) * 1000),
            "regression": regression,
            "cases": [
                {
                    "case_id": r.case_id, "name": r.case_name, "passed": r.passed,
                    "score": round(r.score, 4),
                    "rules": [{"type": x.rule_type, "passed": x.passed, "detail": x.detail}
                              for x in r.rules],
                }
                for r in results
            ],
        }
