"""Tool executor: the seam between the agent loop and the tool gateway.

The gateway validates every model-generated argument against the tool's
input schema before execution and enforces permissions (milestone 08/09
add policy decisions and approvals). The calculator ships as the first
real builtin tool: pure, deterministic, no network access.
"""

from __future__ import annotations

import ast
import json
import operator
from dataclasses import dataclass
from typing import Any, Protocol, cast

# Hard bounds applied to every tool execution.
MAX_ARGUMENTS_BYTES = 8 * 1024
TOOL_TIMEOUT_SECONDS = 15.0


@dataclass(slots=True, frozen=True)
class ToolResult:
    ok: bool
    output: dict[str, Any]
    error_code: str | None = None


class ToolExecutor(Protocol):
    async def execute(self, name: str, arguments: dict[str, Any]) -> ToolResult: ...


# ------------------------------------------------------------------
# Calculator builtin
# ------------------------------------------------------------------

_SAFE_OPS = {
    ast.Add: operator.add,
    ast.Sub: operator.sub,
    ast.Mult: operator.mul,
    ast.Div: operator.truediv,
    ast.FloorDiv: operator.floordiv,
    ast.Mod: operator.mod,
    ast.Pow: operator.pow,
    ast.USub: operator.neg,
    ast.UAdd: operator.pos,
}


def evaluate_expression(expression: str) -> float | int:
    """Evaluate a numeric arithmetic expression safely.

    Only numeric literals and +, -, *, /, //, %, ** and unary +/- are
    accepted. Names, calls, attributes and subscripts are rejected, so no
    arbitrary code can run. Deep expressions are depth-limited.
    """

    def _eval(node: ast.expr, depth: int) -> float | int:
        if depth > 24:
            raise ValueError("expression nesting too deep")
        if isinstance(node, ast.Expression):
            return _eval(node.body, depth + 1)
        if isinstance(node, ast.Constant):
            if isinstance(node.value, (int, float)) and not isinstance(node.value, bool):
                return node.value
            raise ValueError("only numeric literals are allowed")
        if isinstance(node, ast.BinOp):
            op = _SAFE_OPS.get(type(node.op))
            if op is None:
                raise ValueError(f"operator not allowed: {type(node.op).__name__}")
            left = _eval(node.left, depth + 1)
            right = _eval(node.right, depth + 1)
            if isinstance(node.op, ast.Pow) and abs(right) > 1000:
                raise ValueError("exponent too large")
            result = op(left, right)  # type: ignore[operator]
            if isinstance(result, complex):  # e.g. (-8) ** 0.5
                raise ValueError("expression produced a complex result")
            return cast("float | int", result)
        if isinstance(node, ast.UnaryOp):
            op = _SAFE_OPS.get(type(node.op))
            if op is None:
                raise ValueError("unary operator not allowed")
            return cast("float | int", op(_eval(node.operand, depth + 1)))  # type: ignore[operator]
        raise ValueError(f"unsupported expression element: {type(node).__name__}")

    try:
        tree = ast.parse(expression, mode="eval")
    except SyntaxError as exc:
        raise ValueError(f"invalid expression: {exc.msg}") from exc
    result = _eval(tree.body, 0)
    if isinstance(result, float) and result.is_integer():
        return int(result)
    return result


class CalculatorTool:
    """Deterministic arithmetic builtin."""

    name = "calculator"
    description = "Evaluate an arithmetic expression."
    input_schema: dict[str, Any] = {
        "type": "object",
        "properties": {"expression": {"type": "string"}},
        "required": ["expression"],
        "additionalProperties": False,
    }

    async def execute(self, arguments: dict[str, Any]) -> ToolResult:
        expression = arguments.get("expression")
        if not isinstance(expression, str) or len(expression) > 500:
            return ToolResult(False, {}, "invalid_arguments")
        try:
            value = evaluate_expression(expression)
        except ValueError as exc:
            return ToolResult(False, {"error": str(exc)}, "evaluation_failed")
        return ToolResult(True, {"expression": expression, "result": value})


def validate_arguments(arguments: str, schema: dict[str, Any] | None) -> dict[str, Any]:
    """Parse and schema-check model-generated arguments.

    Returns the parsed dict; raises ToolArgumentError when the payload is
    malformed or violates the schema. Model output is never trusted.
    """
    if len(arguments.encode()) > MAX_ARGUMENTS_BYTES:
        raise ToolArgumentError("arguments exceed the size limit")
    try:
        parsed = json.loads(arguments or "{}")
    except json.JSONDecodeError as exc:
        raise ToolArgumentError(f"arguments are not valid JSON: {exc}") from exc
    if not isinstance(parsed, dict):
        raise ToolArgumentError("arguments must be a JSON object")
    if schema:
        _check_object(parsed, schema, root=True)
    return parsed


class ToolArgumentError(Exception):
    """Model-generated arguments failed validation."""


def _check_type(value: Any, expected: str) -> bool:
    return {
        "object": lambda v: isinstance(v, dict),
        "array": lambda v: isinstance(v, list),
        "string": lambda v: isinstance(v, str),
        "integer": lambda v: isinstance(v, int) and not isinstance(v, bool),
        "number": lambda v: isinstance(v, (int, float)) and not isinstance(v, bool),
        "boolean": lambda v: isinstance(v, bool),
        "null": lambda v: v is None,
    }.get(expected, lambda _v: True)(value)


def _check_object(obj: dict[str, Any], schema: dict[str, Any], *, root: bool = False) -> None:
    properties = schema.get("properties", {})
    required = schema.get("required", [])
    for key in required:
        if key not in obj:
            raise ToolArgumentError(f"missing required argument: {key}")
    if schema.get("additionalProperties") is False:
        for key in obj:
            if key not in properties:
                raise ToolArgumentError(f"unexpected argument: {key}")
    for key, value in obj.items():
        prop = properties.get(key)
        if not isinstance(prop, dict):
            continue
        expected = prop.get("type")
        if expected and not _check_type(value, expected):
            raise ToolArgumentError(f"argument {key} must be of type {expected}")
        if expected == "object" and isinstance(value, dict):
            _check_object(value, prop)
        if expected == "array" and isinstance(value, list):
            item_schema = prop.get("items")
            if isinstance(item_schema, dict) and item_schema.get("type") == "object":
                for item in value:
                    if isinstance(item, dict):
                        _check_object(item, item_schema)
        if expected == "string" and isinstance(value, str) and len(value) > 20_000:
            raise ToolArgumentError(f"argument {key} exceeds the length limit")
