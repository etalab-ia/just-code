"""Real python-dotenv compatibility tests, isolated from host credentials."""

import io
import os
import unittest
from unittest.mock import patch

from dotenv import dotenv_values


class ProjectSecretLoaderTests(unittest.TestCase):
    def test_opaque_markers_survive_all_quote_modes(self):
        markers = [
            "__JUST_CODE_SECRET_COMPONENT_A_API_KEY__",
            "__JUST_CODE_SECRET_COMPONENT_B_API_KEY__",
        ]
        with patch.dict(os.environ, {}, clear=True):
            for marker in markers:
                for quote in ["", "'", '"']:
                    for interpolate in [False, True]:
                        with self.subTest(marker=marker, quote=quote, interpolate=interpolate):
                            values = dotenv_values(
                                stream=io.StringIO(f"API_KEY={quote}{marker}{quote}\n"),
                                interpolate=interpolate,
                            )
                            self.assertEqual(values["API_KEY"], marker)

    def test_dollar_behavior_depends_on_bracing(self):
        with patch.dict(os.environ, {}, clear=True):
            literal = dotenv_values(stream=io.StringIO("API_KEY=$MSB_JUST_CODE_SECRET_A\n"))
            expanded = dotenv_values(stream=io.StringIO("API_KEY=${MSB_JUST_CODE_SECRET_A}\n"))
        self.assertEqual(literal["API_KEY"], "$MSB_JUST_CODE_SECRET_A")
        self.assertEqual(expanded["API_KEY"], "")


if __name__ == "__main__":
    unittest.main()


class GuestReadableEncodingTests(unittest.TestCase):
    """The single-quoted literal is the guest-readable delivery encoding.

    The Go encoder emits this form (see
    internal/justcode/project_secret_encoding_loaders_test.go); these cases
    prove python-dotenv reads it back exactly, with and without interpolation.
    """

    VALUES = {
        "PLAIN": "plainvalue",
        "MULTILINE": "-----BEGIN KEY-----\nAAAA\n-----END KEY-----",
        "TRAILING": "trailing   ",
        "LEADING": "   leading",
        "HASH": "hash # inside",
        "DQUOTE": 'has " quote',
        "BACKSLASH": "back\\slash",
        "EMPTY": "",
    }

    def test_single_quoted_values_round_trip(self):
        with patch.dict(os.environ, {}, clear=True):
            for name, value in self.VALUES.items():
                for interpolate in [False, True]:
                    with self.subTest(name=name, interpolate=interpolate):
                        values = dotenv_values(
                            stream=io.StringIO(f"{name}='{value}'\n"),
                            interpolate=interpolate,
                        )
                        self.assertEqual(values[name], value)
