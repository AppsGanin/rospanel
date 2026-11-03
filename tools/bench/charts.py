#!/usr/bin/env python3
"""Render the capacity charts from tools/bench/results-2026-11.json.

Charts are committed as SVG (for the docs site) and PNG (for the GitHub report);
the numbers live in the JSON, so a rerun edits one file and regenerates both.
"""

from __future__ import annotations

import json
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402
from matplotlib.ticker import FuncFormatter  # noqa: E402

ROOT = Path(__file__).resolve().parents[2]
DATA = json.loads((ROOT / "tools" / "bench" / "results-2026-11.json").read_text())
OUT = ROOT / "docs" / "img" / "bench"

INDIGO = "#4f46e5"
LIGHT = "#a5b4fc"
GREEN = "#16a34a"
AMBER = "#d97706"
RED = "#dc2626"
SLATE = "#475569"
GRID = "#e2e8f0"

plt.rcParams.update(
    {
        "font.family": "DejaVu Sans",
        "font.size": 11,
        "figure.facecolor": "white",
        "axes.facecolor": "white",
        "axes.edgecolor": "#cbd5e1",
        "axes.labelcolor": SLATE,
        "text.color": "#0f172a",
        "xtick.color": SLATE,
        "ytick.color": SLATE,
    }
)


def save(fig: plt.Figure, name: str) -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    fig.savefig(OUT / f"{name}.svg", bbox_inches="tight")
    fig.savefig(OUT / f"{name}.png", bbox_inches="tight", dpi=200)
    plt.close(fig)


def title(ax: plt.Axes, main: str, sub: str) -> None:
    ax.set_title(main, loc="left", fontsize=15, fontweight="bold", pad=18)
    ax.text(
        0.0,
        1.02,
        sub,
        transform=ax.transAxes,
        fontsize=10,
        color=SLATE,
        va="bottom",
    )


def latency() -> None:
    rows = DATA["latency_20k"]
    labels = [r["label"] for r in rows]
    p50 = [r["p50"] for r in rows]
    p95 = [r["p95"] for r in rows]
    y = range(len(rows))

    fig, ax = plt.subplots(figsize=(9.8, 4.6))
    h = 0.34
    b1 = ax.barh([i - h / 2 for i in y], p50, height=h, color=INDIGO, label="p50")
    b2 = ax.barh([i + h / 2 for i in y], p95, height=h, color=LIGHT, label="p95")
    for bars in (b1, b2):
        for bar in bars:
            value = bar.get_width()
            ax.text(
                value + 6,
                bar.get_y() + bar.get_height() / 2,
                f"{value:.0f} мс",
                va="center",
                fontsize=9,
                color=SLATE,
            )
    ax.set_yticks(list(y))
    ax.set_yticklabels(labels)
    ax.invert_yaxis()
    ax.set_xlim(0, 505)
    ax.set_xlabel("миллисекунды")
    ax.xaxis.grid(True, color=GRID, linewidth=0.9)
    ax.set_axisbelow(True)
    for side in ("top", "right"):
        ax.spines[side].set_visible(False)
    ax.legend(loc="lower right", frameon=False, ncols=2)
    title(
        ax,
        "Задержки при 20 000 пользователей",
        "полный микс: 11 подписок/с, 1 страница/с, 0,3 правки/с, 0,05 созданий/с · 0 ошибок",
    )
    save(fig, "latency-20k")


def memory() -> None:
    rows = DATA["memory"]
    labels = [r["label"] for r in rows]
    panel = [r["panel"] for r in rows]
    xray = [r["xray"] for r in rows]
    x = range(len(rows))

    fig, ax = plt.subplots(figsize=(8.6, 4.7))
    width = 0.46
    ax.bar(x, panel, width, color=INDIGO, label="панель")
    ax.bar(x, xray, width, bottom=panel, color=GREEN, label="Xray")
    for i, (p, r) in enumerate(zip(panel, xray, strict=True)):
        ax.text(i, p / 2, f"{p} МБ", ha="center", va="center", color="white", fontsize=10, fontweight="bold")
        ax.text(i, p + r / 2, f"{r} МБ", ha="center", va="center", color="white", fontsize=10, fontweight="bold")

    ax.axhline(620, color=AMBER, linestyle=(0, (4, 3)), linewidth=1.4)
    ax.text(-0.48, 626, "cgroup 620 МБ", color=AMBER, fontsize=9, ha="left", va="bottom")
    ax.axhline(961, color=RED, linestyle=(0, (4, 3)), linewidth=1.4)
    ax.text(1.06, 967, "RAM 961 МБ", color=RED, fontsize=9, ha="left", va="bottom")

    ax.annotate(
        "50k + полный микс\nOOM в стенде (620 МБ)",
        xy=(1.0, 600),
        xytext=(0.30, 830),
        fontsize=9,
        color=RED,
        ha="left",
        arrowprops={"arrowstyle": "->", "color": RED, "linewidth": 1.1},
    )

    ax.set_xticks(list(x))
    ax.set_xticklabels([f"{r['label']}\nитого {r['panel'] + r['xray']} МБ" for r in rows])
    ax.set_xlim(-0.5, 1.75)
    ax.set_ylim(0, 1030)
    ax.set_ylabel("мегабайты")
    ax.yaxis.grid(True, color=GRID, linewidth=0.9)
    ax.set_axisbelow(True)
    for side in ("top", "right"):
        ax.spines[side].set_visible(False)
    ax.legend(loc="upper center", bbox_to_anchor=(0.5, -0.14), frameon=False, ncols=2)
    title(ax, "Память — это и есть потолок 1 ГБ", "RSS процессов на тестовом мастере, МБ")
    save(fig, "memory")


def write_rate() -> None:
    rows = DATA["write"]
    labels = [r["label"] for r in rows]
    values = [r["value"] for r in rows]
    colors = ["#0ea5e9", INDIGO, AMBER, GREEN]
    x = range(len(rows))

    fig, ax = plt.subplots(figsize=(8.6, 4.3))
    bars = ax.bar(x, values, width=0.55, color=colors)
    ax.set_yscale("log")
    ax.set_ylim(100, 150000)
    ax.yaxis.set_major_formatter(FuncFormatter(lambda v, _: f"{v:,.0f}".replace(",", " ")))
    for bar, value in zip(bars, values, strict=True):
        ax.text(
            bar.get_x() + bar.get_width() / 2,
            value * 1.15,
            f"{value:,}".replace(",", " "),
            ha="center",
            va="bottom",
            fontsize=10,
            fontweight="bold",
            color="#0f172a",
        )
    ax.set_xticks(list(x))
    ax.set_xticklabels(labels)
    ax.set_ylabel("операций в секунду (лог. шкала)")
    ax.yaxis.grid(True, color=GRID, linewidth=0.9)
    ax.set_axisbelow(True)
    for side in ("top", "right"):
        ax.spines[side].set_visible(False)
    title(ax, "Потолок записи SQLite", "тот же диск, WAL, synchronous=FULL, строки по 256 байт")
    save(fig, "write")


def main() -> None:
    latency()
    memory()
    write_rate()
    print(f"charts: {OUT}")


if __name__ == "__main__":
    main()
