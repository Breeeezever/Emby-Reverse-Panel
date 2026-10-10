from pathlib import Path

from mkdocs.structure.files import File


REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
IMAGE_PREFIX = "https://raw.githubusercontent.com/snnabb/Meridian/v1.13.0/docs/images/"


def on_files(files, config):
    # Explicit homepage URLs avoid historical custom-domain redirect caches.
    homepage = files.get_file_from_path("index.md")
    if homepage is not None:
        homepage.url = "index.html"
    # Reuse the sanitized README assets without duplicating them in the source.
    for image in sorted((REPOSITORY_ROOT / "docs" / "images").glob("*.webp")):
        files.append(
            File.generated(config, f"assets/images/{image.name}", content=image.read_bytes())
        )
    files.append(
        File.generated(
            config,
            "assets/favicon.svg",
            content=(REPOSITORY_ROOT / "web" / "static" / "favicon.svg").read_bytes(),
        )
    )
    return files


def on_page_markdown(markdown, **kwargs):
    return markdown.replace(IMAGE_PREFIX, "assets/images/")
