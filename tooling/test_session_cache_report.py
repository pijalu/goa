#!/usr/bin/env python3
"""Regression tests for the provider prompt-cache timeline report."""

import io
import json
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import session_cache_report as report


def token_stats(prompt, cached=None, predicted=0):
    timings = {"prompt_n": prompt, "predicted_n": predicted}
    if cached is not None:
        timings["cache_read_tokens"] = cached
    return {"Type": "token_stats", "timings": timings}


class SessionCacheReportTest(unittest.TestCase):
    def run_report(self, events):
        with tempfile.TemporaryDirectory() as tmp:
            session = Path(tmp) / "events.jsonl"
            session.write_text(
                "".join(json.dumps(event) + "\n" for event in events),
                encoding="utf-8",
            )
            stdout = io.StringIO()
            with redirect_stdout(stdout):
                rc = report.main([str(session), "--full"])
        self.assertEqual(rc, 0)
        return stdout.getvalue()

    def test_growth_distinguishes_current_hit_from_previous_prompt_reuse(self):
        output = self.run_report([
            token_stats(1000, predicted=10),
            token_stats(9000, cached=1000, predicted=20),
            token_stats(500, cached=9500, predicted=30),
        ])

        self.assertIn("prev_prompt%", output)
        self.assertRegex(
            output,
            r"(?m)^\s*2\s+2\s+9000\s+1000\s+10\.0%\s+100\.0%\s+20\s",
        )
        self.assertRegex(
            output,
            r"(?m)^\s*3\s+3\s+500\s+9500\s+95\.0%\s+95\.0%\s+30\s",
        )
        self.assertIn(
            "# weighted cache reuse: 50.0% "
            "(10500 cached / 21000 prompt tokens; 10500 fresh)",
            output,
        )

    def test_bust_reports_zero_previous_prompt_reuse(self):
        output = self.run_report([
            token_stats(1000),
            token_stats(1000, cached=1000),
            token_stats(2000, cached=0),
        ])

        self.assertRegex(
            output,
            r"(?m)^\s*3\s+3\s+2000\s+0\s+0\.0%\s+0\.0%\s+0\s+MISS\(cold\)",
        )

    def test_zero_previous_prompt_has_no_reuse_denominator(self):
        output = self.run_report([
            token_stats(0, cached=0),
            token_stats(100, cached=0),
        ])

        self.assertRegex(
            output,
            r"(?m)^\s*2\s+2\s+100\s+0\s+0\.0%\s+-\s+0\s+MISS\(cold\)",
        )
        self.assertIn(
            "# weighted cache reuse: 0.0% (0 cached / 100 prompt tokens; 100 fresh)",
            output,
        )


if __name__ == "__main__":
    unittest.main()
