#!/usr/bin/env python3
"""Generate Traio macOS app icons: AppIcon (release) and AppIcon-Debug."""

from __future__ import annotations

import math
import os
from pathlib import Path

try:
    from PIL import Image, ImageDraw, ImageFont
except ImportError:
    raise SystemExit("pip install pillow") from None

ROOT = Path(__file__).resolve().parents[1]
ASSETS = ROOT / "flutter" / "macos" / "Runner" / "Assets.xcassets"

# Traio theme colors
BG = (13, 13, 15)
SURFACE = (22, 22, 26)
BORDER = (42, 42, 46)
PRIMARY = (108, 108, 255)
UP = (61, 214, 140)
WARN = (245, 197, 66)
TEXT = (236, 236, 240)

SIZES = [16, 32, 64, 128, 256, 512, 1024]


def rounded_rect(draw: ImageDraw.ImageDraw, box, radius, fill, outline=None, width=1):
    draw.rounded_rectangle(box, radius=radius, fill=fill, outline=outline, width=width)


def draw_production(draw: ImageDraw.ImageDraw, size: int):
    m = size * 0.08
    rounded_rect(draw, (m, m, size - m, size - m), size * 0.18, SURFACE, BORDER, max(1, size // 128))

    # Candlestick motif
    cx = size * 0.5
    base_y = size * 0.68
    w = max(2, size // 32)
    # green candle
    x1 = cx - size * 0.12
    body_top = size * 0.42
    body_bot = size * 0.62
    draw.line([(x1, size * 0.30), (x1, size * 0.72)], fill=UP, width=w)
    draw.rectangle([x1 - w * 2, body_top, x1 + w * 2, body_bot], fill=UP)
    # purple candle
    x2 = cx + size * 0.10
    draw.line([(x2, size * 0.34), (x2, size * 0.70)], fill=PRIMARY, width=w)
    draw.rectangle([x2 - w * 2, size * 0.48, x2 + w * 2, size * 0.66], fill=PRIMARY)

    # T letter
    font_size = int(size * 0.28)
    try:
        font = ImageFont.truetype("/System/Library/Fonts/Supplemental/Avenir Next Bold.ttf", font_size)
    except OSError:
        font = ImageFont.load_default()
    text = "T"
    bbox = draw.textbbox((0, 0), text, font=font)
    tw, th = bbox[2] - bbox[0], bbox[3] - bbox[1]
    draw.text((cx - tw / 2, size * 0.18 - th / 2), text, fill=TEXT, font=font)


def draw_debug(draw: ImageDraw.ImageDraw, size: int):
    draw_production(draw, size)

    # Orange debug ring
    m = size * 0.05
    ring = max(2, size // 64)
    rounded_rect(
        draw,
        (m, m, size - m, size - m),
        size * 0.20,
        fill=None,
        outline=WARN,
        width=ring * 2,
    )

    # DEV badge
    badge_h = size * 0.22
    badge_w = size * 0.42
    bx1 = size - badge_w - size * 0.04
    by1 = size - badge_h - size * 0.04
    rounded_rect(draw, (bx1, by1, size - size * 0.04, size - size * 0.04), size * 0.06, WARN)
    label = "DEV"
    fs = max(8, int(size * 0.11))
    try:
        font = ImageFont.truetype("/System/Library/Fonts/Supplemental/Avenir Next Bold.ttf", fs)
    except OSError:
        font = ImageFont.load_default()
    bbox = draw.textbbox((0, 0), label, font=font)
    tw, th = bbox[2] - bbox[0], bbox[3] - bbox[1]
    draw.text((bx1 + (badge_w - tw) / 2, by1 + (badge_h - th) / 2 - size * 0.01), label, fill=BG, font=font)


def render_icon(size: int, debug: bool) -> Image.Image:
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)
    rounded_rect(draw, (0, 0, size, size), size * 0.22, BG)
    if debug:
        draw_debug(draw, size)
    else:
        draw_production(draw, size)
    return img


def write_appiconset(name: str, debug: bool):
    folder = ASSETS / f"{name}.appiconset"
    folder.mkdir(parents=True, exist_ok=True)

    mapping = {
        16: "app_icon_16.png",
        32: "app_icon_32.png",
        64: "app_icon_64.png",
        128: "app_icon_128.png",
        256: "app_icon_256.png",
        512: "app_icon_512.png",
        1024: "app_icon_1024.png",
    }

    prefix = "debug_" if debug else ""
    for size in SIZES:
        img = render_icon(size, debug)
        filename = prefix + mapping[size]
        img.save(folder / filename, format="PNG")

    contents = {
        "images": [
            {"size": "16x16", "idiom": "mac", "filename": prefix + "app_icon_16.png", "scale": "1x"},
            {"size": "16x16", "idiom": "mac", "filename": prefix + "app_icon_32.png", "scale": "2x"},
            {"size": "32x32", "idiom": "mac", "filename": prefix + "app_icon_32.png", "scale": "1x"},
            {"size": "32x32", "idiom": "mac", "filename": prefix + "app_icon_64.png", "scale": "2x"},
            {"size": "128x128", "idiom": "mac", "filename": prefix + "app_icon_128.png", "scale": "1x"},
            {"size": "128x128", "idiom": "mac", "filename": prefix + "app_icon_256.png", "scale": "2x"},
            {"size": "256x256", "idiom": "mac", "filename": prefix + "app_icon_256.png", "scale": "1x"},
            {"size": "256x256", "idiom": "mac", "filename": prefix + "app_icon_512.png", "scale": "2x"},
            {"size": "512x512", "idiom": "mac", "filename": prefix + "app_icon_512.png", "scale": "1x"},
            {"size": "512x512", "idiom": "mac", "filename": prefix + "app_icon_1024.png", "scale": "2x"},
        ],
        "info": {"version": 1, "author": "xcode"},
    }

    import json

    (folder / "Contents.json").write_text(json.dumps(contents, indent=2) + "\n")
    print(f"generated {folder}")


def main():
    write_appiconset("AppIcon", debug=False)
    write_appiconset("AppIcon-Debug", debug=True)


if __name__ == "__main__":
    main()
