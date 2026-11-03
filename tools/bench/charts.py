#!/usr/bin/env python3
"""Render the capacity charts from tools/bench/results-2026-11.json.

Charts are committed as SVG (for the docs site) and PNG (for the GitHub report);
the numbers live in the JSON, so a rerun edits one file and regenerates both.
Russian and English variants are produced; the other languages use the English
charts, because a CJK/Arabic-capable font is not guaranteed in CI.
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

INDIGO = "#0d4cd3"
LIGHT = "#6a98ff"
GREEN = "#16a34a"
AMBER = "#d97706"
RED = "#dc2626"
SLATE = "#5b6b7e"
GRID = "#dbe6f5"

plt.rcParams.update(
    {
        "font.family": "DejaVu Sans",
        "font.size": 11,
        "figure.facecolor": "white",
        "axes.facecolor": "white",
        "axes.edgecolor": "#c3d2e8",
        "axes.labelcolor": SLATE,
        "text.color": "#0a1b2e",
        "xtick.color": SLATE,
        "ytick.color": SLATE,
    }
)

TEXT = {
    "ru": {
        "latency_title": "Задержки при 20 000 пользователей",
        "latency_sub": "полный микс: 11 подписок/с, 1 страница/с, 0,3 правки/с, 0,05 созданий/с · 0 ошибок",
        "ms": "мс",
        "ms_axis": "миллисекунды",
        "memory_title": "Память — это и есть потолок 1 ГБ",
        "memory_sub": "RSS процессов на тестовом мастере, МБ",
        "panel": "панель",
        "xray": "Xray",
        "mb": "мегабайты",
        "cgroup": "cgroup 620 МБ",
        "ram": "RAM 961 МБ",
        "oom": "50k + полный микс\nOOM в стенде (620 МБ)",
        "total": "итого",
        "write_title": "Потолок записи SQLite",
        "write_sub": "тот же диск, WAL, synchronous=FULL, строки по 256 байт",
        "per_sec": "операций в секунду (лог. шкала)",
        "ops": {
            "Подписка": "Подписка",
            "Страница": "Страница",
            "Карточка": "Карточка",
            "Лимиты": "Лимиты",
            "Создание": "Создание",
            "20k, полный микс": "20k, полный микс",
            "50k, только ноды": "50k, только ноды",
            "fsync файла": "fsync файла",
            "SQLite FULL": "SQLite FULL",
            "SQLite NORMAL": "SQLite NORMAL",
            "одна транзакция": "одна транзакция",
        },
    },
    "en": {
        "latency_title": "Latency at 20,000 users",
        "latency_sub": "full mix: 11 subs/s, 1 page/s, 0.3 limit edits/s, 0.05 creates/s · 0 errors",
        "ms": "ms",
        "ms_axis": "milliseconds",
        "memory_title": "Memory is the 1 GB ceiling",
        "memory_sub": "process RSS on the test master, MB",
        "panel": "panel",
        "xray": "Xray",
        "mb": "megabytes",
        "cgroup": "cgroup 620 MB",
        "ram": "RAM 961 MB",
        "oom": "50k + full mix\nOOM in the stand (620 MB)",
        "total": "total",
        "write_title": "SQLite write ceiling",
        "write_sub": "same disk, WAL, synchronous=FULL, 256-byte rows",
        "per_sec": "operations per second (log scale)",
        "ops": {
            "Подписка": "Subscriptions",
            "Страница": "Users page",
            "Карточка": "User card",
            "Лимиты": "Limit edits",
            "Создание": "Creates",
            "20k, полный микс": "20k, full mix",
            "50k, только ноды": "50k, nodes only",
            "fsync файла": "file fsync",
            "SQLite FULL": "SQLite FULL",
            "SQLite NORMAL": "SQLite NORMAL",
            "одна транзакция": "one transaction",
        },
    },
}


def save(fig: plt.Figure, name: str) -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    fig.savefig(OUT / f"{name}.svg", bbox_inches="tight")
    fig.savefig(OUT / f"{name}.png", bbox_inches="tight", dpi=200)
    plt.close(fig)


def title(ax: plt.Axes, main: str, sub: str) -> None:
    ax.set_title(main, loc="left", fontsize=15, fontweight="bold", pad=18)
    ax.text(0.0, 1.02, sub, transform=ax.transAxes, fontsize=10, color=SLATE, va="bottom")


def latency(lang: str) -> None:
    t = TEXT[lang]
    rows = DATA["latency_20k"]
    labels = [t["ops"][r["label"]] for r in rows]
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
                f"{value:.0f} {t['ms']}",
                va="center",
                fontsize=9,
                color=SLATE,
            )
    ax.set_yticks(list(y))
    ax.set_yticklabels(labels)
    ax.invert_yaxis()
    ax.set_xlim(0, 515)
    ax.set_xlabel(t["ms_axis"])
    ax.xaxis.grid(True, color=GRID, linewidth=0.9)
    ax.set_axisbelow(True)
    for side in ("top", "right"):
        ax.spines[side].set_visible(False)
    ax.legend(loc="lower right", frameon=False, ncols=2)
    title(ax, t["latency_title"], t["latency_sub"])
    save(fig, f"latency-20k-{lang}")


def memory(lang: str) -> None:
    t = TEXT[lang]
    rows = DATA["memory"]
    panel = [r["panel"] for r in rows]
    xray = [r["xray"] for r in rows]
    x = range(len(rows))

    fig, ax = plt.subplots(figsize=(8.6, 4.7))
    width = 0.46
    ax.bar(x, panel, width, color=INDIGO, label=t["panel"])
    ax.bar(x, xray, width, bottom=panel, color=GREEN, label=t["xray"])
    for i, (p, r) in enumerate(zip(panel, xray, strict=True)):
        ax.text(
            i, p / 2, f"{p} {t['mb']}", ha="center", va="center", color="white", fontsize=10, fontweight="bold"
        )
        ax.text(
            i, p + r / 2, f"{r} {t['mb']}", ha="center", va="center", color="white", fontsize=10, fontweight="bold"
        )

    ax.axhline(620, color=AMBER, linestyle=(0, (4, 3)), linewidth=1.4)
    ax.text(-0.48, 626, t["cgroup"], color=AMBER, fontsize=9, ha="left", va="bottom")
    ax.axhline(961, color=RED, linestyle=(0, (4, 3)), linewidth=1.4)
    ax.text(1.06, 967, t["ram"], color=RED, fontsize=9, ha="left", va="bottom")
    ax.annotate(
        t["oom"],
        xy=(1.0, 600),
        xytext=(0.30, 830),
        fontsize=9,
        color=RED,
        ha="left",
        arrowprops={"arrowstyle": "->", "color": RED, "linewidth": 1.1},
    )

    ax.set_xticks(list(x))
    ax.set_xticklabels(
        [f"{t['ops'][r['label']]}\n{t['total']} {r['panel'] + r['xray']} {t['mb']}" for r in rows]
    )
    ax.set_xlim(-0.5, 1.75)
    ax.set_ylim(0, 1030)
    ax.set_ylabel(t["mb"])
    ax.yaxis.grid(True, color=GRID, linewidth=0.9)
    ax.set_axisbelow(True)
    for side in ("top", "right"):
        ax.spines[side].set_visible(False)
    ax.legend(loc="upper center", bbox_to_anchor=(0.5, -0.14), frameon=False, ncols=2)
    title(ax, t["memory_title"], t["memory_sub"])
    save(fig, f"memory-{lang}")


def write_rate(lang: str) -> None:
    t = TEXT[lang]
    rows = DATA["write"]
    labels = [t["ops"][r["label"]] for r in rows]
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
            color="#0a1b2e",
        )
    ax.set_xticks(list(x))
    ax.set_xticklabels(labels)
    ax.set_ylabel(t["per_sec"])
    ax.yaxis.grid(True, color=GRID, linewidth=0.9)
    ax.set_axisbelow(True)
    for side in ("top", "right"):
        ax.spines[side].set_visible(False)
    title(ax, t["write_title"], t["write_sub"])
    save(fig, f"write-{lang}")


def main() -> None:
    for lang in ("ru", "en"):
        latency(lang)
        memory(lang)
        write_rate(lang)
    print(f"charts: {OUT}")


if __name__ == "__main__":
    main()
