# dump

A lightweight CLI that dumps text files from a directory tree into a single
Markdown document and copies it to your clipboard — ready to paste into an
LLM chat.

It automatically skips binary files, respects `.gitignore`, supports custom
glob exclusions, and can dump only git-changed files (staged and working tree)
alongside their diffs.

## Why

Copy-pasting context from a codebase into an LLM is tedious. `dump` walks the
target tree, filters out noise (binaries, ignored files, custom excludes), and
formats everything into one clean Markdown payload:

~~~~text
Working directory: /path/to/project
Command:           dump src/

Files dumped:
  ./src/main.go
  ./src/utils.go

Summary:
  Files included:                  2
  Files ignored (.gitignore):      4
  Files ignored (custom -x):       0
  Files ignored (non-text/binary): 1

./src/main.go:
```
package main
...
```
~~~~

## Installation

### Using `go install`

```bash
go install [github.com/Nonlodico34/dump@latest](https://github.com/Nonlodico34/dump@latest)
```

### Build from source

```bash
git clone [https://github.com/Nonlodico34/dump.git](https://github.com/Nonlodico34/dump.git)
cd dump
go build -o dump .
```

On Windows, you can also use the included script to build and deploy `dump.exe`
to `C:\Tools`:

```powershell
.\build.ps1
```

## Usage

```text
dump [options] [path...]
```

Every invocation prints a summary header (working directory, command, targeted
file list, and filter counters) followed by the file contents, and copies the
entire output to the system clipboard.

### Options

| Flag | Description |
| :--- | :--- |
| `-a`, `--all` | Include all files, ignoring `.gitignore` rules |
| `-c`, `--changed` | Dump only files changed in git (staged + working tree, with diffs) |
| `-n`, `--no-diff` | Used with `-c`: omit diff blocks and dump full file contents only |
| `-d`, `--dry-run` | Print targeted files and summary counters without dumping contents |
| `-x`, `--exclude` | Exclude files, directories, or glob patterns (repeatable) |
| `-h`, `--help` | Show help message and usage examples |

### Examples

```bash
# Dump all tracked text files in the current directory
dump

# Preview which files would be included without dumping content
dump -d

# Dump specific directories or files
dump src/ main.go

# Dump files matching a glob pattern across the tree
dump '*.go'

# Exclude specific patterns or folders
dump -x "*.json" -x "node_modules/"

# Dump staged and unstaged git changes with their diffs
dump -c

# Dump git-changed files without diff blocks, scoped to a subdirectory
dump -c -n src/

# Dry-run in git mode
dump -c -d
```
