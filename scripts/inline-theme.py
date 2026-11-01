#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
# SPDX-License-Identifier: Apache-2.0 OR MIT
"""Inline the render-blocking head assets, and let the CSP allow them by hash.

theme-init.js decides light or dark before the first paint, so it cannot be
deferred: a deferred theme script is a page that flashes white and then turns
dark, which is worse than either. Left external it is 711 bytes that cost a full
network round trip on the critical path — around 300ms of blocked rendering to
deliver less than a kilobyte.

Inlining removes the request. The content policy allows it by hash rather than
by 'unsafe-inline', so exactly this script may run and nothing else: the hash is
computed here, from the file, at build time, which means it cannot drift from
what is actually inlined.

Two things are inlined. The theme bootstrap, above. And the page-specific
stylesheets — the workspace and the playground each load one, and on those two
pages it is a second render-blocking request for a few kilobytes that only that
page uses. Merging them into the shared stylesheet would make every other page
carry them; inlining puts them where they are needed and nowhere else.

    python3 scripts/inline-theme.py web/public web/_layouts/theme-init.js
"""

from __future__ import annotations

import base64
import hashlib
import re
import sys
from pathlib import Path


# Stylesheets loaded by one page each. The shared stylesheet stays external:
# it is on every page, so it is worth a request that caches across the site.
PAGE_STYLESHEETS = ("workspace.css", "playground.css")

# Pages where the shared stylesheet is inlined too.
#
# On these the visitor usually arrives cold, and the stylesheet is a whole
# network round trip standing between the document and the first paint. Inlining
# it removes that round trip at the cost of not caching across pages — worth it
# for a landing page, not worth it for the 2,845 message pages, which are small,
# numerous, and would each carry their own copy.
ENTRY_PAGES = {
    "index.html", "faq/index.html", "docs/index.html", "deadline/index.html",
    "conformance/index.html", "contact/index.html", "workspace/index.html",
    "playground/index.html", "messages/index.html",
}


def inline_shared_stylesheet(html: str, root: Path) -> str:
    css_file = root / "site.css"
    if not css_file.is_file():
        return html

    link = re.compile(r'<link rel="stylesheet" href="[^"]*site\.css" />')
    if not link.search(html):
        return html

    css = css_file.read_text(encoding="utf-8")
    digest = base64.b64encode(hashlib.sha256(css.encode("utf-8")).digest()).decode()
    token = f"\'sha256-{digest}\'"

    html = link.sub(lambda _: f"<style>{css}</style>", html, count=1)
    return re.sub(
        r"style-src [^;]*",
        lambda m: m.group(0) if token in m.group(0) else m.group(0) + " " + token,
        html, count=1)


def inline_page_stylesheet(html: str, root: Path) -> str:
    """Replace a single-page stylesheet link with its contents."""
    for name in PAGE_STYLESHEETS:
        link = re.compile(r'<link rel="stylesheet" href="[^"]*' + re.escape(name) + r'" />')
        if not link.search(html):
            continue

        css_file = root / name
        if not css_file.is_file():
            continue

        css = css_file.read_text(encoding="utf-8")
        digest = base64.b64encode(hashlib.sha256(css.encode("utf-8")).digest()).decode()
        token = f"\'sha256-{digest}\'"

        html = link.sub(lambda _: f"<style>{css}</style>", html, count=1)
        html = re.sub(
            r"style-src [^;]*",
            lambda m: m.group(0) if token in m.group(0) else m.group(0) + " " + token,
            html, count=1)
    return html


def main() -> int:
    if len(sys.argv) < 3:
        print("usage: inline-theme.py <site-dir> <theme-init.js>", file=sys.stderr)
        return 2

    root, script = Path(sys.argv[1]), Path(sys.argv[2])
    if not root.is_dir() or not script.is_file():
        print("site directory or script missing", file=sys.stderr)
        return 2

    source = script.read_text(encoding="utf-8")
    digest = base64.b64encode(hashlib.sha256(source.encode("utf-8")).digest()).decode()
    token = f"'sha256-{digest}'"

    # ssg 0.0.70 fingerprints the script (theme-init.<hash>.js) and adds
    # integrity and crossorigin attributes; older releases emitted the bare
    # name with no attributes. Either tag is replaced whole, attributes and all:
    # the inlined copy is allowed by its CSP hash, not by SRI.
    tag = re.compile(
        r'<script src="[^"]*theme-init(?:\.[0-9a-f]+)?\.js"[^>]*></script>')
    inline = f"<script>{source}</script>"

    changed = 0
    for page in root.rglob("*.html"):
        html = page.read_text(encoding="utf-8")
        if not tag.search(html):
            continue

        html = tag.sub(lambda _: inline, html, count=1)

        # Widen script-src by exactly this hash. Without it the browser blocks
        # the script it just inlined, and every visitor gets the default theme.
        def widen(m: re.Match) -> str:
            directive = m.group(0)
            return directive if token in directive else directive + " " + token

        new_html, n = re.subn(r"script-src [^;]*", widen, html, count=1)
        if n != 1:
            print(f"{page}: no script-src to widen", file=sys.stderr)
            return 1

        new_html = inline_page_stylesheet(new_html, root)
        if str(page.relative_to(root)) in ENTRY_PAGES:
            new_html = inline_shared_stylesheet(new_html, root)
        page.write_text(new_html, encoding="utf-8")
        changed += 1

    if not changed:
        print("no pages referenced theme-init.js", file=sys.stderr)
        return 1

    print(f"theme: inlined into {changed} page(s), allowed by sha256-{digest[:12]}…")
    return 0


if __name__ == "__main__":
    sys.exit(main())
