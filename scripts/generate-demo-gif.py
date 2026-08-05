#!/usr/bin/env python3
"""Render docs/demo.gif from the real console output in docs/demo-output.txt.

The GIF is a compact, readable terminal session:
  1. type the command
  2. print the exact kdiag console report (no abbreviated copy)
  3. scroll like a terminal when the report exceeds the viewport
"""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "docs" / "demo.gif"
DEMO_TXT = ROOT / "docs" / "demo-output.txt"

W, H = 1080, 720
BG = (13, 17, 23)
TITLE_BG = (22, 27, 34)
BORDER = (48, 54, 61)
DOT_R, DOT_Y, DOT_G = (255, 95, 86), (255, 189, 46), (39, 201, 63)

PROMPT = "~/code/kdiag"
CMD = "kdiag inspect service payment -n kdiag-demo"

# Approximate terminal colors used by internal/report/console.go (no-color=false).
C_DEFAULT = (201, 209, 217)
C_BOLD = (240, 246, 252)
C_DIM = (139, 148, 158)
C_RED = (255, 123, 114)
C_YELLOW = (210, 153, 34)
C_CYAN = (121, 192, 255)
C_GREEN = (63, 185, 80)
C_PROMPT_PATH = (139, 148, 158)
C_PROMPT_DOLLAR = (63, 185, 80)
C_CMD = (230, 237, 243)

MARGIN_X = 20
MARGIN_Y = 52
BOTTOM_MARGIN = 18
FONT_SIZE = 15
LINE_H = 20


def ensure_demo_output() -> str:
    """Load the console report used for the GIF.

    Prefer an existing docs/demo-output.txt (ideally captured from a live
    Kind/cluster run). Only fall back to the fixture-based TestRenderDemo
    when the file is missing or --fixture is passed.
    """
    use_fixture = "--fixture" in sys.argv
    def looks_like_report(text: str) -> bool:
        return "Health:" in text and ("Kind:" in text or "Name:" in text)

    if DEMO_TXT.exists() and not use_fixture:
        text = DEMO_TXT.read_text()
        if looks_like_report(text):
            return text

    env = os.environ.copy()
    env["KDIAG_DEMO"] = "1"
    proc = subprocess.run(
        ["go", "test", "./internal/diag", "-run", "TestRenderDemo", "-v"],
        cwd=ROOT,
        env=env,
        capture_output=True,
        text=True,
        check=False,
    )
    out = proc.stdout + proc.stderr
    lines = out.splitlines()
    start = next(
        (i for i, ln in enumerate(lines) if ln.startswith("Kind:")),
        None,
    )
    if start is None:
        print(out, file=sys.stderr)
        raise SystemExit("failed to capture demo output from go test")
    end = next(
        (i for i in range(start, len(lines)) if lines[i].startswith("--- PASS")),
        len(lines),
    )
    text = "\n".join(lines[start:end]).rstrip() + "\n"
    DEMO_TXT.write_text(text)
    return text


def font(size: int = FONT_SIZE) -> ImageFont.ImageFont:
    for path in (
        "/System/Library/Fonts/Supplemental/Menlo.ttc",
        "/System/Library/Fonts/Menlo.ttc",
        "/Library/Fonts/SF-Mono-Regular.otf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
    ):
        if os.path.exists(path):
            try:
                return ImageFont.truetype(path, size=size)
            except OSError:
                continue
    return ImageFont.load_default()


def colorize_line(line: str) -> list[tuple[str, tuple[int, int, int]]]:
    """Best-effort coloring mirroring console.go, for a realistic look."""
    s = line
    if s.startswith("Kind:"):
        pre, _, rest = s.partition(":")
        return [(pre + ":", C_DIM), (rest, C_BOLD)]
    if s.startswith("Namespace:") or s.startswith("Name:") or s.startswith("Kubernetes:"):
        pre, _, rest = s.partition(":")
        return [(pre + ":", C_DIM), (rest, C_DEFAULT)]
    if s.startswith("Health:"):
        if "DEGRADED" in s:
            pre, _, _ = s.partition("DEGRADED")
            return [(pre, C_DIM), ("DEGRADED", C_RED)]
        if "HEALTHY" in s:
            pre, _, _ = s.partition("HEALTHY")
            return [(pre, C_DIM), ("HEALTHY", C_GREEN)]
    if s in ("Root cause candidates", "Causal chain:", "Evidence:", "Recommendations:") or s.startswith(
        "Propagated symptoms"
    ) or s.startswith("Other findings"):
        return [(s, C_BOLD)]
    if " CRITICAL " in s or s.lstrip().startswith("- CRITICAL"):
        return _paint_severity(s, "CRITICAL", C_RED)
    if " WARNING " in s or s.lstrip().startswith("- WARNING"):
        return _paint_severity(s, "WARNING", C_YELLOW)
    if s.strip().startswith("- ") and ":" in s:
        # Evidence line: "- source: value"
        indent, rest = s.split("- ", 1)
        if ": " in rest:
            src, _, val = rest.partition(": ")
            return [
                (indent + "- ", C_DEFAULT),
                (src, C_CYAN),
                (": ", C_DEFAULT),
                (val, C_DEFAULT),
            ]
    if "→" in s:
        # Causal chain step
        parts = s.split("→", 1)
        return [(parts[0], C_DEFAULT), ("→", C_CYAN), (parts[1], C_DEFAULT)]
    if s.strip().startswith("Pod/") or s.strip().startswith("Service/") or s.strip().startswith(
        "Deployment/"
    ):
        return [(s, C_DIM)]
    return [(s, C_DEFAULT)]


def _paint_severity(s: str, label: str, color: tuple[int, int, int]) -> list[tuple[str, tuple[int, int, int]]]:
    idx = s.find(label)
    if idx < 0:
        return [(s, C_DEFAULT)]
    return [
        (s[:idx], C_DEFAULT),
        (label, color),
        (s[idx + len(label) :], C_BOLD if "confidence" in s or "readiness" in s or "service-" in s or "pod-" in s or "deployment-" in s else C_DEFAULT),
    ]


def draw_chrome(draw: ImageDraw.ImageDraw, f: ImageFont.ImageFont) -> None:
    draw.rounded_rectangle((0, 0, W - 1, H - 1), radius=12, fill=BG, outline=BORDER)
    draw.rounded_rectangle((0, 0, W - 1, 36), radius=12, fill=TITLE_BG)
    draw.rectangle((0, 18, W - 1, 36), fill=TITLE_BG)
    for x, c in ((22, DOT_R), (42, DOT_Y), (62, DOT_G)):
        draw.ellipse((x - 6, 12, x + 6, 24), fill=c)
    draw.text((W // 2, 10), "Terminal — kdiag", fill=C_DIM, font=f, anchor="mt")


def measure(draw: ImageDraw.ImageDraw, text: str, f: ImageFont.ImageFont) -> int:
    bbox = draw.textbbox((0, 0), text, font=f)
    return bbox[2] - bbox[0]


def wrap_segments(
    draw: ImageDraw.ImageDraw,
    segments: list[tuple[str, tuple[int, int, int]]],
    f: ImageFont.ImageFont,
    max_width: int,
) -> list[list[tuple[str, tuple[int, int, int]]]]:
    """Wrap a colored line and preserve indentation on continuation rows."""
    rows: list[list[tuple[str, tuple[int, int, int]]]] = [[]]
    x = 0
    plain = "".join(text for text, _ in segments)
    leading = plain[: len(plain) - len(plain.lstrip())]
    continuation = leading + "  " if plain.strip() else ""

    def new_row() -> None:
        nonlocal x
        rows.append([])
        x = 0
        if continuation:
            rows[-1].append((continuation, C_DIM))
            x = measure(draw, continuation, f)

    for text, color in segments:
        for ch in text:
            w = measure(draw, ch, f)
            if x + w > max_width and x > 0:
                new_row()
            rows[-1].append((ch, color))
            x += w
    return rows


def render(
    f: ImageFont.ImageFont,
    cmd_typed: str,
    output_lines: list[str],
    cursor: bool,
) -> Image.Image:
    img = Image.new("RGB", (W, H), BG)
    draw = ImageDraw.Draw(img)
    draw_chrome(draw, f)

    rows: list[list[tuple[str, tuple[int, int, int]]]] = [
        [(PROMPT, C_PROMPT_PATH)],
        [("$ ", C_PROMPT_DOLLAR), (cmd_typed, C_CMD)],
    ]
    if cmd_typed == CMD:
        rows.append([])
        for line in output_lines:
            segs = colorize_line(line)
            rows.extend(wrap_segments(draw, segs, f, W - 2 * MARGIN_X))

    # Keep the visible terminal viewport bounded. Once output reaches the
    # bottom, older rows scroll away exactly as they would in a real terminal.
    visible_capacity = (H - MARGIN_Y - BOTTOM_MARGIN) // LINE_H
    visible_rows = rows[-visible_capacity:]

    y = MARGIN_Y
    for row in visible_rows:
        x = MARGIN_X
        for text, color in row:
            draw.text((x, y), text, fill=color, font=f)
            x += measure(draw, text, f)
        y += LINE_H

    if cursor and cmd_typed != CMD and len(rows) <= visible_capacity:
        # caret after partial command
        x = MARGIN_X + measure(draw, "$ ", f) + measure(draw, cmd_typed, f)
        y_cmd = MARGIN_Y + LINE_H
        draw.rectangle((x + 1, y_cmd + 2, x + 8, y_cmd + LINE_H - 2), fill=C_DEFAULT)

    return img


def main() -> None:
    report = ensure_demo_output()
    output_lines = report.splitlines()
    f = font()

    frames: list[Image.Image] = []
    durations: list[int] = []

    # 1) Brief idle prompt blink.
    for _ in range(2):
        frames.append(render(f, "", [], cursor=True))
        durations.append(200)
        frames.append(render(f, "", [], cursor=False))
        durations.append(200)

    # 2) Type the real command two characters at a time. This stays readable
    # without spending most of the animation on an empty terminal.
    steps = list(range(2, len(CMD) + 1, 2))
    if not steps or steps[-1] != len(CMD):
        steps.append(len(CMD))
    for i in steps:
        frames.append(render(f, CMD[:i], [], cursor=True))
        durations.append(38 if CMD[i - 1] != " " else 58)

    # Brief pause before "running"
    frames.append(render(f, CMD, [], cursor=False))
    durations.append(300)

    # 3) Reveal real report line-by-line (as a program would print)
    for n in range(1, len(output_lines) + 1):
        frames.append(render(f, CMD, output_lines[:n], cursor=False))
        # Slightly slower on section headers
        line = output_lines[n - 1]
        durations.append(
            90
            if line in ("",)
            else 70
            if line.startswith("Health:") or line.startswith("Root") or line.startswith("    Causal")
            else 45
        )

        # The causal chain is the product thesis. Hold it before the following
        # symptom list scrolls the root-cause section out of view.
        if n < len(output_lines) and output_lines[n].startswith("Propagated symptoms"):
            durations[-1] = 2200

    # 4) Hold the final frame
    frames.append(render(f, CMD, output_lines, cursor=False))
    durations.append(2600)

    # Use one compact palette for every frame. A per-frame adaptive palette is
    # larger and can make terminal colors shimmer between otherwise identical
    # frames. The first and final screens together contain the full theme.
    palette_seed = Image.new("RGB", (W, H * 2), BG)
    palette_seed.paste(frames[0], (0, 0))
    palette_seed.paste(frames[-1], (0, H))
    palette = palette_seed.quantize(colors=64, method=Image.Quantize.FASTOCTREE)
    frames = [frame.quantize(palette=palette, dither=Image.Dither.NONE) for frame in frames]

    OUT.parent.mkdir(parents=True, exist_ok=True)
    frames[0].save(
        OUT,
        save_all=True,
        append_images=frames[1:],
        duration=durations,
        loop=0,
        optimize=True,
    )
    print(f"wrote {OUT} ({OUT.stat().st_size // 1024} KiB, {len(frames)} frames)")
    print(f"source report lines: {len(output_lines)} (from {DEMO_TXT.relative_to(ROOT)})")
    print(f"duration: {sum(durations) / 1000:.1f}s; viewport: {W}x{H}")


if __name__ == "__main__":
    main()
