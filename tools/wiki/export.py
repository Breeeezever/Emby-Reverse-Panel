import argparse
from pathlib import Path
from urllib.parse import urlsplit, urlunsplit

import markdown
import yaml
from markdown.extensions import Extension
from markdown.treeprocessors import Treeprocessor


ROOT = Path(__file__).resolve().parents[2]
WIKI_URL = "https://github.com/snnabb/Meridian/wiki/"
DOCS_URL = "https://snnabb.github.io/Meridian/index.html"


class CollectLinks(Treeprocessor):
    def run(self, root):
        self.md.wiki_links = [node.get("href") for node in root.iter("a")]


class WikiLinks(Extension):
    def extendMarkdown(self, md):
        md.treeprocessors.register(CollectLinks(md), "wiki-links", -1)


def links(text):
    parser = markdown.Markdown(extensions=["tables", "fenced_code", WikiLinks()])
    parser.convert(text)
    return parser.wiki_links


def pages(nav):
    for item in nav:
        for title, target in item.items():
            if isinstance(target, list):
                yield from pages(target)
            else:
                yield title, target


def page_name(source):
    return "Home" if source == "index.md" else Path(source).stem


def export(destination):
    config = yaml.safe_load((ROOT / "mkdocs.yml").read_text(encoding="utf-8"))
    navigation = config["nav"]
    sources = dict(pages(navigation))
    names = {source: page_name(source) for source in sources.values()}
    destination.mkdir(parents=True, exist_ok=True)

    for source, name in names.items():
        text = (ROOT / config["docs_dir"] / source).read_text(encoding="utf-8")
        for href in set(links(text)):
            url = urlsplit(href)
            if url.scheme or url.netloc or not url.path.endswith(".md"):
                continue
            if url.path not in names:
                raise ValueError(f"{source}: unknown Wiki page {href}")
            target = urlunsplit(("https", "github.com", f"/snnabb/Meridian/wiki/{names[url.path]}", url.query, url.fragment))
            literal = f"]({href})"
            if literal not in text:
                raise ValueError(f"{source}: unsupported link syntax for {href}")
            text = text.replace(literal, f"]({target})")
        if any(urlsplit(href).path.endswith(".md") and not urlsplit(href).scheme for href in links(text)):
            raise ValueError(f"{source}: unconverted local Markdown link")
        (destination / f"{name}.md").write_text(text, encoding="utf-8")

    sidebar = ["**Meridian Wiki**", ""]
    for item in navigation:
        for title, target in item.items():
            if isinstance(target, list):
                sidebar.extend(["", f"**{title}**", ""])
                sidebar.extend(f"- [{label}]({WIKI_URL}{names[source]})" for label, source in pages(target))
            else:
                sidebar.append(f"- [{title}]({WIKI_URL}{names[target]})")
    (destination / "_Sidebar.md").write_text("\n".join(sidebar) + "\n", encoding="utf-8")
    footer = f"[项目首页](https://github.com/snnabb/Meridian) · [文档站]({DOCS_URL}) · [反馈问题](https://github.com/snnabb/Meridian/issues)\n"
    (destination / "_Footer.md").write_text(footer, encoding="utf-8")
    print(f"Exported {len(names)} Wiki pages with sidebar and footer to {destination}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Export project documentation to a cloned GitHub Wiki.")
    parser.add_argument("destination", type=Path)
    export(parser.parse_args().destination)
