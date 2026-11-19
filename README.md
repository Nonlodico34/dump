# dump

A small CLI that dumps text files from a directory tree into a single
Markdown document and copies it to your clipboard — ready to paste into an
LLM chat.

It skips binaries, honors `.gitignore`, and can also dump just the files
changed in git (staged and working tree) with their diffs.

## Why

Copy-pasting a project into an LLM is tedious. `dump` walks the tree, filters
noise (binaries, ignored files, custom excludes), and produces one clean
Markdown blob:
