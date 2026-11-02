#!/usr/bin/env python3
"""Validate the portable priority/effort backlog contract (no dependencies)."""
from pathlib import Path
import re

root = Path(__file__).resolve().parents[1]
text = (root / "TODO.md").read_text()
lanes = re.findall(r"^## (P1|P2|P3|P\?|Done)$", text, re.M)
assert lanes == ["P1", "P2", "P3", "P?", "Done"], lanes
lane = None
for line in text.splitlines():
    if line.startswith("## "):
        lane = line[3:]
    if line.startswith("- [ ]"):
        effort = r"\?/([SML]|XL|\?)" if lane == "P?" else r"([SML]|XL)"
        assert re.match(r"^- \[ \] \*\*\[" + effort + r"\] .+\*\* — .+", line), line
    for path in re.findall(r"\]\((backlog/[^)]+)\)", line):
        assert (root / path).is_file(), path
print("TODO format and research links: OK")
