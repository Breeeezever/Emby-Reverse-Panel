import argparse
import json
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import unquote, urljoin, urlsplit
from xml.etree import ElementTree

import yaml


ROOT = Path(__file__).resolve().parents[2]


class PageLinks(HTMLParser):
    def __init__(self, text):
        super().__init__(convert_charrefs=True)
        self.links = []
        self.ids = set()
        self.canonical = None
        self.feed(text)

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if "id" in attrs:
            self.ids.add(attrs["id"])
        if tag == "link" and attrs.get("rel") == "canonical":
            self.canonical = attrs.get("href")
        for attr in ("href", "src"):
            if attrs.get(attr):
                self.links.append(attrs[attr])


def check(site):
    config = yaml.safe_load((ROOT / "mkdocs.yml").read_text(encoding="utf-8"))
    base_url = config["site_url"]
    base = urlsplit(base_url)
    documents = {
        path: PageLinks(path.read_text(encoding="utf-8"))
        for path in site.rglob("*.html")
    }
    failures = []
    checked = 0

    def target(url, source):
        nonlocal checked
        parsed = urlsplit(url)
        if parsed.hostname == "biuo.me" or (parsed.hostname or "").endswith(".biuo.me"):
            failures.append(f"{source}: obsolete domain in {url}")
            return None
        if parsed.netloc != base.netloc:
            return None
        if parsed.scheme != base.scheme or not parsed.path.startswith(base.path):
            failures.append(f"{source}: link escapes the HTTPS project site: {url}")
            return None
        if parsed.path == base.path:
            failures.append(f"{source}: homepage must use index.html to avoid cached redirects: {url}")
            return None
        relative = unquote(parsed.path[len(base.path):])
        path = site / relative
        if relative.endswith("/"):
            path /= "index.html"
        if not path.is_file():
            failures.append(f"{source}: missing link target {url}")
            return None
        if parsed.fragment and path in documents and unquote(parsed.fragment) not in documents[path].ids:
            failures.append(f"{source}: missing heading {url}")
        checked += 1
        return path

    for path, document in documents.items():
        page_url = document.canonical or urljoin(base_url, path.relative_to(site).as_posix())
        if document.canonical:
            target(document.canonical, path.relative_to(site))
        for link in document.links:
            target(urljoin(page_url, link), path.relative_to(site))

    sitemap = ElementTree.parse(site / "sitemap.xml")
    pages = sitemap.findall(".//{http://www.sitemaps.org/schemas/sitemap/0.9}loc")
    for page in pages:
        target(page.text, "sitemap.xml")
    if not pages:
        failures.append("sitemap.xml: no documentation pages")

    search = json.loads((site / "search" / "search_index.json").read_text(encoding="utf-8"))
    for document in search["docs"]:
        target(urljoin(base_url, document["location"]), "search_index.json")
    if failures:
        raise SystemExit("\n".join(failures))
    print(f"Checked {len(pages)} documentation pages and {checked} links, including navigation, headings, assets, sitemap and search.")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Validate a built Meridian documentation site.")
    parser.add_argument("site", type=Path)
    check(parser.parse_args().site)
