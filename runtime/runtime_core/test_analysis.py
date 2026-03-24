from __future__ import annotations

import importlib.util
from pathlib import Path
import unittest


def load_analysis_module():
    module_path = Path(__file__).with_name("analysis.py")
    spec = importlib.util.spec_from_file_location("runtime_runtime_core_analysis_test_target", module_path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"failed to load analysis module from {module_path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


SAFE_EXEC_BUILTINS = load_analysis_module().SAFE_EXEC_BUILTINS


class SafeExecBuiltinsTest(unittest.TestCase):
    def test_common_functional_builtins_are_available(self) -> None:
        exec_env = {"__builtins__": SAFE_EXEC_BUILTINS.copy()}
        code = """
result = {
    "mapped": list(map(str, [1, 2, 3])),
    "filtered": list(filter(lambda x: x % 2 == 1, [1, 2, 3, 4])),
    "reversed": list(reversed(["a", "b", "c"])),
    "iter_next": next(iter(["x", "y"])),
    "callable": callable(str),
    "repr": repr({"a": 1}),
}
"""
        exec(code, exec_env, exec_env)

        self.assertEqual(exec_env["result"]["mapped"], ["1", "2", "3"])
        self.assertEqual(exec_env["result"]["filtered"], [1, 3])
        self.assertEqual(exec_env["result"]["reversed"], ["c", "b", "a"])
        self.assertEqual(exec_env["result"]["iter_next"], "x")
        self.assertTrue(exec_env["result"]["callable"])
        self.assertEqual(exec_env["result"]["repr"], "{'a': 1}")

    def test_common_exception_classes_are_available(self) -> None:
        exec_env = {"__builtins__": SAFE_EXEC_BUILTINS.copy()}
        code = """
messages = []
for exc_type in [ValueError, RuntimeError, TypeError, KeyError, IndexError]:
    try:
        raise exc_type(exc_type.__name__)
    except Exception as exc:
        messages.append(str(exc))
"""
        exec(code, exec_env, exec_env)
        self.assertEqual(
            exec_env["messages"],
            ["ValueError", "RuntimeError", "TypeError", "'KeyError'", "IndexError"],
        )

    def test_open_remains_unavailable(self) -> None:
        exec_env = {"__builtins__": SAFE_EXEC_BUILTINS.copy()}
        with self.assertRaises(NameError):
            exec("open('blocked.txt', 'w')", exec_env, exec_env)

    def test_allowed_stdlib_modules_can_be_imported(self) -> None:
        exec_env = {"__builtins__": SAFE_EXEC_BUILTINS.copy()}
        code = """
import difflib

result = difflib.get_close_matches("CD3D", ["CD3E", "TRAC", "CD3D"], n=2, cutoff=0.6)
"""
        exec(code, exec_env, exec_env)

        self.assertEqual(exec_env["result"], ["CD3D", "CD3E"])

    def test_disallowed_modules_raise_import_error(self) -> None:
        exec_env = {"__builtins__": SAFE_EXEC_BUILTINS.copy()}
        with self.assertRaisesRegex(ImportError, "import 'subprocess' is not allowed"):
            exec("import subprocess", exec_env, exec_env)


if __name__ == "__main__":
    unittest.main()
