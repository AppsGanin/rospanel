#!/usr/bin/env python3
"""Build the MkDocs source tree from the single source of truth.

The READMEs and docs/ are written for GitHub. MkDocs needs one directory, its own
file names and links that resolve inside the site, so this script copies them into
.site_src/ and rewrites only the links that point at repository files. Nothing is
translated or edited: the pages stay byte-for-byte the READMEs apart from links,
which is the only way the docs cannot drift from the product again.
"""

from __future__ import annotations

import posixpath
import re
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / ".site_src"
GITHUB = "https://github.com/AppsGanin/rospanel"

README_PAGES = {
    "README.md": "index.md",
    "README-RU.md": "ru.md",
    "README-ZH.md": "zh.md",
    "README-AR.md": "ar.md",
    "README-FA.md": "fa.md",
}

# Repository files that must stay on GitHub: MkDocs has no page for them.
GITHUB_FILES = {
    "install.sh",
    "LICENSE",
    "examples/plugins",
    "docs/plugins-design.md",
}

LINK = re.compile(r"\]\(([^)\s]+)(\s+\"[^\"]*\")?\)")
IMG_SRC = re.compile(r'src="(docs/img/[^"]+)"')


def clean_anchor(anchor: str) -> str:
    """GitHub keeps the emoji's variation selector in an anchor; MkDocs drops it.

    `#️-where-to-get-a-server` and `#-adding-a-node` both point at headings MkDocs
    slugs as `where-to-get-a-server` and `adding-a-node`.
    """
    return anchor.lstrip("\ufe0f\u200d ")


def site_path(target: str) -> str | None:
    """Map one repository-relative link to a path inside the site.

    None means the link is already absolute or belongs to the repository, and must
    be left alone (or pointed at GitHub by the caller).
    """
    base, _, anchor = target.partition("#")
    base = base.removeprefix("./")
    anchor = clean_anchor(anchor)
    tail = f"#{anchor}" if anchor else ""
    if base.startswith("docs/img/"):
        return "img/" + base[len("docs/img/") :] + tail
    if base.startswith("img/"):
        return base + tail
    if base.startswith("../img/"):
        return "img/" + base[len("../img/") :] + tail
    if base == "docs/api.md":
        return "api.md" + tail
    if base in ("docs/plugins/README.md", "docs/plugins/README-RU.md"):
        name = "index.md" if base.endswith("README.md") else "index-ru.md"
        return f"plugins/{name}{tail}"
    if base.startswith("docs/plugins/"):
        name = base[len("docs/plugins/") :]
        if name.endswith("-RU.md"):
            name = name[: -len("-RU.md")] + "-ru.md"
        return "plugins/" + name + tail
    if base == "docs/grafana/README.md":
        return "grafana/index.md" + tail
    if base == "docs/grafana/rospanel.json":
        return "grafana/rospanel.json"
    if base in README_PAGES:
        return README_PAGES[base] + tail
    if base in ("docs/api.md", "docs/api", "docs/api/"):
        return "api/index.md" + tail
    if base in ("docs/benchmarks.md", "docs/benchmarks", "docs/benchmarks/"):
        return "benchmarks/index.md" + tail
    if base == "reference.md":
        return "api/reference.md" + tail
    if base in ("../api.md", "../api/README.md"):
        return "api/index.md" + tail
    if base in GITHUB_FILES:
        if base == "examples/plugins":
            return f"{GITHUB}/tree/main/{base}"
        return f"{GITHUB}/blob/main/{base}"
    if base and not base.startswith(("http://", "https://", "/", "#")):
        # Any other repository file (a fixture, a script) is not part of the site;
        # point at the repository instead of shipping a broken link.
        return f"{GITHUB}/blob/main/{base}"
    return None


def rewrite_links(text: str, page_dir: str) -> str:
    def repl(match: re.Match[str]) -> str:
        target = match.group(1)
        title = match.group(2) or ""
        if target.startswith(("http://", "https://", "mailto:", "/")):
            return match.group(0)
        if target.startswith("#"):
            return f"](#{clean_anchor(target[1:])}){title}"
        # Sibling links in docs/plugins/ ("api.md", "tutorial.md") are relative to
        # that folder in the repository, but the site path needs the prefix.
        if page_dir == "plugins" and "/" not in target and target.endswith(".md"):
            target = "docs/plugins/" + target
        site = site_path(target)
        if site is None:
            return match.group(0)
        if site.startswith("http"):
            return f"]({site}){title}"
        base, sep, anchor = site.partition("#")
        rel = posixpath.relpath(base, page_dir or ".")
        if rel == ".":
            rel = ""
        if sep:
            rel = f"{rel}#{anchor}"
        return f"]({rel}){title}"

    text = LINK.sub(repl, text)
    return IMG_SRC.sub(lambda m: f'src="{site_path(m.group(1))}"', text)


ALERT = re.compile(r"^> \[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*$")


def github_alerts(text: str) -> str:
    """Turn GitHub's `> [!NOTE]` blocks into Material admonitions.

    A block is the alert line plus the quoted lines under it. Anything else is left
    alone, so a plain quote keeps rendering as a quote.
    """
    lines = text.splitlines()
    out: list[str] = []
    i = 0
    while i < len(lines):
        match = ALERT.match(lines[i])
        if not match:
            out.append(lines[i])
            i += 1
            continue
        out.append(f"!!! {match.group(1).lower()}")
        i += 1
        while i < len(lines) and lines[i].startswith(">"):
            body = lines[i][1:].lstrip()
            out.append(f"    {body}" if body else "")
            i += 1
        out.append("")
    return "\n".join(out)


def url_depth(dst: Path) -> int:
    """How many directories deep the built page sits, for relative assets.

    MkDocs turns `ru.md` into `/ru/`, `benchmarks.md` into `/benchmarks/` and
    `plugins/api.md` into `/plugins/api/`; a raw `<img src="img/...">` is not
    rewritten by MkDocs, so it has to be prefixed by hand or it 404s.
    """
    rel = dst.relative_to(OUT)
    if dst.name == "index.md":
        return len(rel.parent.parts)
    return len(rel.parent.parts) + 1


def write_page(src: Path, dst: Path, page_dir: str = "") -> None:
    text = src.read_text(encoding="utf-8")
    text = rewrite_links(text, page_dir)
    # The READMEs open with a raw <div align="center">; without markdown="1"
    # Python-Markdown treats everything under it as raw HTML and the page shows
    # literal `# Heading` and `![badge](...)` instead of rendering them.
    text = text.replace('<div align="center">', '<div align="center" markdown="1">')
    text = github_alerts(text)
    prefix = "../" * url_depth(dst)
    if prefix:
        # Markdown image targets are rewritten by MkDocs itself; only raw HTML
        # <img src> needs the prefix.
        text = text.replace('src="img/', f'src="{prefix}img/')
    dst.parent.mkdir(parents=True, exist_ok=True)
    dst.write_text(text, encoding="utf-8")


def main() -> None:
    if OUT.exists():
        shutil.rmtree(OUT)
    OUT.mkdir(parents=True)

    for src_name, dst_name in README_PAGES.items():
        write_page(ROOT / src_name, OUT / dst_name)
        if dst_name in ("ar.md", "fa.md"):
            page = OUT / dst_name
            text = page.read_text(encoding="utf-8")
            page.write_text(
                text.replace('<div dir="rtl">', '<div dir="rtl" markdown="1">'),
                encoding="utf-8",
            )
    for src in sorted((ROOT / "docs" / "api").glob("*.md")):
        name = "index.md" if src.name == "README.md" else src.name
        write_page(src, OUT / "api" / name, page_dir="api")
    for src in sorted((ROOT / "docs" / "benchmarks").glob("*.md")):
        name = "index.md" if src.name == "README.md" else src.name
        write_page(src, OUT / "benchmarks" / name, page_dir="benchmarks")

    plugins = ROOT / "docs" / "plugins"
    for src in sorted(plugins.glob("*.md")):
        name = src.name
        if name == "README.md":
            dst = OUT / "plugins" / "index.md"
        elif name == "README-RU.md":
            dst = OUT / "plugins" / "index-ru.md"
        elif name.endswith("-RU.md"):
            dst = OUT / "plugins" / (name[: -len("-RU.md")] + "-ru.md")
        else:
            dst = OUT / "plugins" / name
        write_page(src, dst, page_dir="plugins")

    for src in sorted((ROOT / "docs" / "grafana").glob("README*.md")):
        name = "index.md" if src.name == "README.md" else src.stem.removeprefix("README-") + ".md"
        write_page(src, OUT / "grafana" / name, page_dir="grafana")
    shutil.copy2(ROOT / "docs" / "grafana" / "rospanel.json", OUT / "grafana" / "rospanel.json")

    img = ROOT / "docs" / "img"
    if img.is_dir():
        shutil.copytree(img, OUT / "img")

    css = ROOT / "docs" / "css"
    if css.is_dir():
        shutil.copytree(css, OUT / "css")

    js = ROOT / "docs" / "js"
    if js.is_dir():
        shutil.copytree(js, OUT / "js")

    swagger = ROOT / "internal" / "server" / "swaggerui"
    if swagger.is_dir():
        shutil.copytree(swagger, OUT / "swaggerui")

    shutil.copy2(ROOT / "docs" / "openapi.json", OUT / "openapi.json")

    print(f"docs source: {OUT}")


if __name__ == "__main__":
    main()
