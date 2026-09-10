import os
from pathlib import Path
import subprocess
import tempfile
import unittest


BOOTSTRAP = Path(__file__).resolve().parents[1] / "tart-bootstrap.sh"


class TartMtuTests(unittest.TestCase):
    def run_bootstrap(self, mtu=None, route="interface: en7", sudo_status=0):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            commands = {
                "route": f"printf '%s\\n' '{route}'",
                "sudo": f'echo "sudo:$*"; exit {sudo_status}',
                "opencode": 'echo "opencode:$*"',
                "git": "exit 0",
            }
            for name, body in commands.items():
                path = root / name
                path.write_text("#!/bin/sh\n" + body + "\n")
                path.chmod(0o755)
            args = ["sh", str(BOOTSTRAP), "4096", "test-password", "test-user"]
            if mtu is not None:
                args.append(mtu)
            return subprocess.run(
                args, input="test-key\n", text=True, capture_output=True,
                env={**os.environ, "HOME": directory,
                     "PATH": f"{directory}:/usr/bin:/bin"}, timeout=5,
            )

    def test_default_and_numeric_overrides(self):
        for value in (None, "1280", "1400", "1500"):
            with self.subTest(value=value):
                result = self.run_bootstrap(value)
                self.assertEqual(result.returncode, 0, result.stderr)
                expected = f"sudo:-n ifconfig en7 mtu {value or '1280'}"
                self.assertIn(expected, result.stdout)
                self.assertLess(result.stdout.index(expected),
                                result.stdout.index("opencode:serve"))

    def test_auto_skips_network_configuration(self):
        result = self.run_bootstrap("auto", route="", sudo_status=1)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("sudo:", result.stdout)

    def test_invalid_values_fail_before_server_start(self):
        for value in ("", "1279", "1501", "9000", "01280", "abc", "1280;id"):
            with self.subTest(value=value):
                result = self.run_bootstrap(value)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("TART_MTU must be", result.stderr)
                self.assertNotIn("opencode:", result.stdout)

    def test_missing_route(self):
        result = self.run_bootstrap(route="")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Cannot determine", result.stderr)

    def test_sudo_failure(self):
        result = self.run_bootstrap(sudo_status=1)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Cannot apply TART_MTU", result.stderr)
        self.assertNotIn("opencode:", result.stdout)


if __name__ == "__main__":
    unittest.main()
