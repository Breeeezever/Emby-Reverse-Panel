# Meridian UI fonts

The UI uses self-hosted, variable-weight WOFF2 subsets of **Inter** (Latin,
400–700, optical size 14) and **Noto Sans SC** (Chinese, 400–700). Both are
licensed under SIL OFL 1.1; their licenses are included in this directory.
No external font service is contacted when the panel loads.

Upstream sources (Google Fonts):

- https://github.com/google/fonts/tree/main/ofl/inter
- https://github.com/google/fonts/tree/main/ofl/notosanssc

Noto Sans SC includes the Chinese characters used in the panel's HTML and
JavaScript plus full-width punctuation. Other characters, including arbitrary
site names, fall back to the user's installed Chinese fonts. Inter includes
Latin-1 and common punctuation, preserving tabular numbers and font features.

To rebuild after adding UI copy, install `fonttools[woff]` in a local Python
environment and run from the repository root:

    python tools/build-ui-fonts.py /path/to/Inter.ttf /path/to/NotoSansSC.ttf

Use `Inter[opsz,wght].ttf` and `NotoSansSC[wght].ttf` from the respective upstream
sources. Input checksums for the current build:
29160a80ff49ddcab2c97711247e08b1fab27a484a329ce8b813d820dc559031  Inter.ttf
a3041811a78c361b1de50f953c805e0244951c21c5bd412f7232ef0d899af0da  NotoSansSC.ttf
