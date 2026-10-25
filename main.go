package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atotto/clipboard"
	ignore "github.com/sabhiram/go-gitignore"
)

type arrayFlags []string

func (i *arrayFlags) String() string {
	return strings.Join(*i, ",")
}

func (i *arrayFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}

type stats struct {
	includedFiles []string
	ignoredGit    int
	ignoredBinary int
	ignoredCustom int
}

// gitFile represents a changed file with optional diff and change status.
type gitFile struct {
	path    string
	status  string // "A" added, "M" modified, "D" deleted, etc.
	isNew   bool
	deleted bool
	content string // already loaded (index for staged, filesystem for working)
	diff    string // empty if new file, no diff, or diff disabled
}

// Check for null bytes to identify binary files.
func isTextFile(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}

	if bytes.IndexByte(buf[:n], 0) != -1 {
		return false, nil
	}

	return true, nil
}

// Check whether a byte slice looks like binary content (null bytes).
func isBinaryContent(data []byte) bool {
	n := len(data)
	if n > 512 {
		n = 512
	}
	return bytes.IndexByte(data[:n], 0) != -1
}

// Count files inside a directory tree when skipped.
func countFilesDir(dirPath string) int {
	count := 0
	_ = filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		if !info.IsDir() {
			count++
		}
		return nil
	})
	return count
}

// Clean and normalize path for consistent pattern matching.
func normalizePath(path string) string {
	clean := filepath.ToSlash(filepath.Clean(path))
	clean = strings.TrimPrefix(clean, "./")
	return clean
}

// Check if path matches any custom exclude pattern.
func matchesCustomExcludes(path string, isDir bool, customExcludes []string) bool {
	normPath := normalizePath(path)
	baseName := filepath.Base(normPath)

	for _, pattern := range customExcludes {
		normPattern := normalizePath(pattern)
		normPattern = strings.TrimSuffix(normPattern, "/")

		// Exact match or base name match.
		if normPath == normPattern || baseName == normPattern {
			return true
		}

		// Glob match on relative path or base name.
		if matched, _ := filepath.Match(normPattern, normPath); matched {
			return true
		}
		if matched, _ := filepath.Match(normPattern, baseName); matched {
			return true
		}

		// Directory prefix match.
		if strings.HasPrefix(normPath, normPattern+"/") {
			return true
		}
	}
	return false
}

// Format relative path and read file content into the provided builder.
func appendFileDump(builder *strings.Builder, relPath string, st *stats, dryRun bool) {
	cleanPath := filepath.ToSlash(relPath)
	if !strings.HasPrefix(cleanPath, "./") && cleanPath != "." {
		cleanPath = "./" + cleanPath
	}

	isText, err := isTextFile(relPath)
	if err != nil || !isText {
		if dryRun && st != nil {
			st.ignoredBinary++
		}
		return
	}

	if dryRun {
		if st != nil {
			st.includedFiles = append(st.includedFiles, cleanPath)
		}
		return
	}

	content, err := os.ReadFile(relPath)
	if err != nil {
		return
	}

	builder.WriteString(fmt.Sprintf("%s:\n```\n%s\n```\n\n", cleanPath, string(content)))
}

// Recursively process a target directory using gitignore and custom exclude rules.
func walkDir(root string, gitIgnore *ignore.GitIgnore, showAll bool, customExcludes []string, builder *strings.Builder, st *stats, dryRun bool) {
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		relPath, err := filepath.Rel(".", path)
		if err != nil || relPath == "." {
			return nil
		}

		slashPath := filepath.ToSlash(relPath)

		// Always skip .git directory.
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}

		// Apply custom exclude rules first.
		if matchesCustomExcludes(relPath, info.IsDir(), customExcludes) {
			if info.IsDir() {
				if dryRun && st != nil {
					st.ignoredCustom += countFilesDir(path)
				}
				return filepath.SkipDir
			}
			if dryRun && st != nil {
				st.ignoredCustom++
			}
			return nil
		}

		// Apply gitignore rules only if showAll is false.
		if !showAll && gitIgnore != nil {
			matchPath := slashPath
			if info.IsDir() {
				matchPath += "/"
			}

			if gitIgnore.MatchesPath(matchPath) {
				if info.IsDir() {
					if dryRun && st != nil {
						st.ignoredGit += countFilesDir(path)
					}
					return filepath.SkipDir
				}
				if dryRun && st != nil {
					st.ignoredGit++
				}
				return nil
			}
		}

		if !info.IsDir() {
			appendFileDump(builder, relPath, st, dryRun)
		}

		return nil
	})
}

// Process a single file or directory path argument.
func processPath(path string, gitIgnore *ignore.GitIgnore, showAll bool, customExcludes []string, builder *strings.Builder, st *stats, dryRun bool) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}

	if matchesCustomExcludes(path, info.IsDir(), customExcludes) {
		if dryRun {
			if info.IsDir() {
				st.ignoredCustom += countFilesDir(path)
			} else {
				st.ignoredCustom++
			}
		}
		return
	}

	if info.IsDir() {
		walkDir(path, gitIgnore, showAll, customExcludes, builder, st, dryRun)
	} else {
		if !showAll && gitIgnore != nil && gitIgnore.MatchesPath(filepath.ToSlash(path)) {
			if dryRun {
				st.ignoredGit++
			}
			return
		}
		appendFileDump(builder, path, st, dryRun)
	}
}

// Expand wildcard pattern recursively across directory tree.
func processGlobPattern(pattern string, gitIgnore *ignore.GitIgnore, showAll bool, customExcludes []string, builder *strings.Builder, st *stats, dryRun bool) {
	cleanPattern := filepath.ToSlash(pattern)
	basePattern := filepath.Base(cleanPattern)

	_ = filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(".", path)
		if err != nil {
			return nil
		}

		slashPath := filepath.ToSlash(relPath)
		baseName := filepath.Base(slashPath)

		matchedPath, _ := filepath.Match(cleanPattern, slashPath)
		matchedBase, _ := filepath.Match(basePattern, baseName)

		if matchedPath || matchedBase {
			processPath(relPath, gitIgnore, showAll, customExcludes, builder, st, dryRun)
		}

		return nil
	})
}

// --- Git changed-files helpers ---

// isGitRepo reports whether the current directory is inside a git work tree.
func isGitRepo() bool {
	out, err := runGit("rev-parse", "--is-inside-work-tree")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "true"
}

// requireGitRepo prints a fatal error and exits if not inside a git work tree.
func requireGitRepo() {
	if !isGitRepo() {
		fmt.Fprintln(os.Stderr, "Error: not inside a git repository. Git-related flags (-c/--changed) require a git environment.")
		os.Exit(1)
	}
}

// runGit executes a git command in the current directory and returns stdout.
func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, errBuf.String())
	}
	return out.String(), nil
}

// gitShowFile returns the content of a file in the index (staged version).
func gitShowFile(path string) (string, error) {
	return runGit("show", ":"+path)
}

// parseNameStatus parses `git diff --name-status` output into a status map.
func parseNameStatus(out string) map[string]string {
	statuses := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: "<status>\t<path>" or "<status>\t<old>\t<new>" for renames.
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		status := fields[0]
		path := fields[len(fields)-1]
		// Normalize rename statuses R100 -> R, C75 -> C.
		if len(status) > 1 {
			status = status[:1]
		}
		statuses[path] = status
	}
	return statuses
}

// getStatuses returns a map of path -> status code for staged or working tree.
func getStatuses(cached bool, paths []string) (map[string]string, error) {
	args := []string{"diff", "--name-status"}
	if cached {
		args = append(args, "--cached")
	}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := runGit(args...)
	if err != nil {
		return nil, err
	}
	return parseNameStatus(out), nil
}

// listGitChanged returns the changed files (relative paths) for staged or working tree,
// filtered by optional path arguments.
func listGitChanged(cached bool, paths []string) ([]string, error) {
	args := []string{"diff", "--name-only"}
	if cached {
		args = append(args, "--cached")
	}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := runGit(args...)
	if err != nil {
		return nil, err
	}

	var files []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		files = append(files, line)
	}

	// Also include untracked files when looking at working tree.
	if !cached {
		untrackedArgs := []string{"ls-files", "--others", "--exclude-standard"}
		if len(paths) > 0 {
			untrackedArgs = append(untrackedArgs, "--")
			untrackedArgs = append(untrackedArgs, paths...)
		}
		if uOut, err := runGit(untrackedArgs...); err == nil {
			for _, line := range strings.Split(uOut, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || seen[line] {
					continue
				}
				seen[line] = true
				files = append(files, line)
			}
		}
	}

	return files, nil
}

// getDiff returns the diff for a single file (staged or working).
func getDiff(path string, cached bool) (string, error) {
	args := []string{"diff"}
	if cached {
		args = append(args, "--cached")
	}
	args = append(args, "--", path)
	return runGit(args...)
}

// collectGitChanged builds the ordered list of gitFile entries for a section.
// It also updates stats with ignored binary/custom counts.
// When noDiff is true, diffs are not computed nor attached.
func collectGitChanged(cached bool, paths []string, customExcludes []string, st *stats, dryRun bool, noDiff bool) ([]gitFile, error) {
	files, err := listGitChanged(cached, paths)
	if err != nil {
		return nil, err
	}

	statuses, err := getStatuses(cached, paths)
	if err != nil {
		statuses = map[string]string{}
	}

	var result []gitFile
	for _, f := range files {
		norm := filepath.ToSlash(f)
		if matchesCustomExcludes(norm, false, customExcludes) {
			if dryRun && st != nil {
				st.ignoredCustom++
			}
			continue
		}

		status := statuses[norm]
		gf := gitFile{path: norm, status: status}

		switch status {
		case "A":
			// New file: dump content (from index if staged, filesystem if working),
			// no diff needed.
			gf.isNew = true
			var content string
			if cached {
				content, err = gitShowFile(norm)
			} else {
				var data []byte
				data, err = os.ReadFile(norm)
				content = string(data)
			}
			if err != nil {
				continue
			}
			if isBinaryContent([]byte(content)) {
				if dryRun && st != nil {
					st.ignoredBinary++
				}
				continue
			}
			gf.content = content
			gf.diff = "" // no diff for new files
		case "D":
			// Deleted file: only diff, no content.
			gf.deleted = true
			if noDiff {
				// Without diff a deleted file has nothing to show → skip entirely.
				if dryRun && st != nil {
					st.ignoredBinary++
				}
				continue
			}
			diff, derr := getDiff(norm, cached)
			if derr != nil {
				continue
			}
			gf.diff = strings.TrimRight(diff, "\n")
			if gf.diff == "" {
				continue
			}
		default:
			// Modified / renamed / etc.: content + diff.
			var content string
			if cached {
				// Coerenza staged: contenuto dall'index, non dal filesystem.
				content, err = gitShowFile(norm)
			} else {
				var data []byte
				data, err = os.ReadFile(norm)
				content = string(data)
			}
			if err != nil {
				continue
			}
			if isBinaryContent([]byte(content)) {
				if dryRun && st != nil {
					st.ignoredBinary++
				}
				continue
			}
			gf.content = content
			if !noDiff {
				diff, derr := getDiff(norm, cached)
				if derr == nil {
					gf.diff = strings.TrimRight(diff, "\n")
				}
			}
		}

		result = append(result, gf)
	}
	return result, nil
}

// appendGitSection writes a full section (header + files + diffs) to the builder.
func appendGitSection(builder *strings.Builder, title string, files []gitFile, st *stats, dryRun bool, noDiff bool) {
	if len(files) == 0 {
		return
	}

	if dryRun {
		for _, gf := range files {
			st.includedFiles = append(st.includedFiles, "./"+gf.path)
		}
		return
	}

	builder.WriteString(fmt.Sprintf("=== %s ===\n\n", title))
	for _, gf := range files {
		if gf.deleted {
			// Only diff for deleted files (and only if diffs are enabled).
			if !noDiff && gf.diff != "" {
				builder.WriteString(fmt.Sprintf("./%s (deleted, diff):\n```diff\n%s\n```\n\n", gf.path, gf.diff))
			}
			continue
		}

		builder.WriteString(fmt.Sprintf("./%s:\n```\n%s\n```\n\n", gf.path, gf.content))
		if !noDiff && gf.diff != "" {
			builder.WriteString(fmt.Sprintf("./%s (diff):\n```diff\n%s\n```\n\n", gf.path, gf.diff))
		}
	}
}

// printSummary prints the header info (working dir, command), the list of included
// files, and the summary counters. The label ("Files to dump:" vs "Files dumped:")
// depends on dryRun. It always prints the header, even if there are no files.
func printSummary(st *stats, stagedCount, workingCount int, dryRun bool, extraCounters func()) {
	if cwd, err := os.Getwd(); err == nil {
		fmt.Printf("Working directory: %s\n", cwd)
	}
	fmt.Printf("Command:           %s\n\n", strings.Join(os.Args, " "))

	label := "Files dumped:"
	if dryRun {
		label = "Files to dump:"
	}

	if len(st.includedFiles) > 0 {
		fmt.Println(label)
		for _, file := range st.includedFiles {
			fmt.Printf("  %s\n", file)
		}
		fmt.Println()
	} else {
		fmt.Println(label)
		fmt.Println("  (none)")
		fmt.Println()
	}

	fmt.Printf("Summary:\n")
	fmt.Printf("  Files included:                  %d\n", len(st.includedFiles))
	if extraCounters != nil {
		extraCounters()
	} else {
		fmt.Printf("  Files ignored (.gitignore):      %d\n", st.ignoredGit)
	}
	fmt.Printf("  Files ignored (custom -x):       %d\n", st.ignoredCustom)
	fmt.Printf("  Files ignored (non-text/binary): %d\n", st.ignoredBinary)
}

func main() {
	var excludeFlags arrayFlags

	allFlag := flag.Bool("all", false, "")
	flag.BoolVar(allFlag, "a", false, "")

	dryRunFlag := flag.Bool("dry-run", false, "")
	flag.BoolVar(dryRunFlag, "d", false, "")

	changedFlag := flag.Bool("changed", false, "")
	flag.BoolVar(changedFlag, "c", false, "")

	noDiffFlag := flag.Bool("no-diff", false, "")
	flag.BoolVar(noDiffFlag, "n", false, "")

	flag.Var(&excludeFlags, "exclude", "")
	flag.Var(&excludeFlags, "x", "")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "dump - dump text files to stdout and clipboard, skipping binaries and honoring .gitignore.\n\n")
		fmt.Fprintf(os.Stderr, "Usage: dump [options] [path...]\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fmt.Fprintf(os.Stderr, "  -a, --all        Include all files, ignoring .gitignore rules\n")
		fmt.Fprintf(os.Stderr, "  -c, --changed    Dump only files changed in git (staged + working tree, with diffs)\n")
		fmt.Fprintf(os.Stderr, "  -n, --no-diff    With -c: don't attach diffs (content only)\n")
		fmt.Fprintf(os.Stderr, "  -d, --dry-run    List targeted files and show summary without dumping content\n")
		fmt.Fprintf(os.Stderr, "  -x, --exclude    Exclude files, directories, or patterns (can be specified multiple times)\n")
		fmt.Fprintf(os.Stderr, "  -h, --help       Show this help message\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  dump\n")
		fmt.Fprintf(os.Stderr, "      Dumps every text file in the current tree (respecting .gitignore).\n")
		fmt.Fprintf(os.Stderr, "      For example, if the tree contains three text files, it prints the\n")
		fmt.Fprintf(os.Stderr, "      working directory and command, a list of those three files, a summary,\n")
		fmt.Fprintf(os.Stderr, "      and then the full content of each file with its relative path as header.\n\n")
		fmt.Fprintf(os.Stderr, "  dump -a\n")
		fmt.Fprintf(os.Stderr, "      Same as above, but also include files ignored by .gitignore.\n\n")
		fmt.Fprintf(os.Stderr, "  dump src/ main.go\n")
		fmt.Fprintf(os.Stderr, "      Dump only the given paths (recursively for directories).\n\n")
		fmt.Fprintf(os.Stderr, "  dump '*.go'\n")
		fmt.Fprintf(os.Stderr, "      Dump files matching the glob pattern across the tree.\n\n")
		fmt.Fprintf(os.Stderr, "  dump -x \"*.json\" -x \"node_modules/\"\n")
		fmt.Fprintf(os.Stderr, "      Dump everything except JSON files and the node_modules directory.\n\n")
		fmt.Fprintf(os.Stderr, "  dump -d\n")
		fmt.Fprintf(os.Stderr, "      Dry-run: print working directory, command, list of files that would\n")
		fmt.Fprintf(os.Stderr, "      be dumped, and summary counters. No content is printed.\n\n")
		fmt.Fprintf(os.Stderr, "  dump -c\n")
		fmt.Fprintf(os.Stderr, "      Dump files changed in git: STAGED section first (content from the\n")
		fmt.Fprintf(os.Stderr, "      index + staged diff), then WORKING TREE section (content from disk\n")
		fmt.Fprintf(os.Stderr, "      + unstaged diff). New files have no diff; deleted files show only\n")
		fmt.Fprintf(os.Stderr, "      their diff.\n\n")
		fmt.Fprintf(os.Stderr, "  dump -c -n\n")
		fmt.Fprintf(os.Stderr, "      Same as -c but without diffs: only full file contents, no diff blocks.\n\n")
		fmt.Fprintf(os.Stderr, "  dump -c src/\n")
		fmt.Fprintf(os.Stderr, "      Like -c but limited to changes under src/ (pathspec passed to git).\n\n")
		fmt.Fprintf(os.Stderr, "  dump -c -d\n")
		fmt.Fprintf(os.Stderr, "      Dry-run for git mode: list changed files and counters (staged/working,\n")
		fmt.Fprintf(os.Stderr, "      excluded, binary) without printing any content or diff.\n")
	}

	flag.Parse()

	args := flag.Args()

	// --- Git changed-files mode ---
	if *changedFlag {
		// (d) Fatal error if not inside a git repo.
		requireGitRepo()

		var st stats
		var builder strings.Builder

		staged, err := collectGitChanged(true, args, excludeFlags, &st, *dryRunFlag, *noDiffFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading staged changes: %v\n", err)
			os.Exit(1)
		}
		working, err := collectGitChanged(false, args, excludeFlags, &st, *dryRunFlag, *noDiffFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading working tree changes: %v\n", err)
			os.Exit(1)
		}

		// Always print the summary header first (in both dry-run and normal mode).
		printSummary(&st, len(staged), len(working), *dryRunFlag, func() {
			fmt.Printf("  Files included (staged):         %d\n", len(staged))
			fmt.Printf("  Files included (working):        %d\n", len(working))
		})
		if *noDiffFlag {
			fmt.Printf("  Diffs:                           disabled (-n)\n")
		}
		fmt.Println()

		if *dryRunFlag {
			return
		}

		appendGitSection(&builder, "STAGED", staged, &st, *dryRunFlag, *noDiffFlag)
		appendGitSection(&builder, "WORKING TREE", working, &st, *dryRunFlag, *noDiffFlag)

		output := builder.String()
		if output == "" {
			fmt.Fprintln(os.Stderr, "No git changes to dump.")
			return
		}
		fmt.Print(output)
		if err := clipboard.WriteAll(output); err != nil {
			fmt.Fprintf(os.Stderr, "Error copying to clipboard: %v\n", err)
		}
		return
	}

	// `--no-diff` only makes sense with `-c`.
	if *noDiffFlag {
		fmt.Fprintln(os.Stderr, "Error: -n/--no-diff can only be used together with -c/--changed.")
		os.Exit(1)
	}

	var builder strings.Builder
	var st stats

	// Load .gitignore if present and not overridden by -a/--all flag.
	var gitIgnore *ignore.GitIgnore
	if !*allFlag {
		if _, err := os.Stat(".gitignore"); err == nil {
			gitIgnore, _ = ignore.CompileIgnoreFile(".gitignore")
		}
	}

	// Target explicit items if arguments are provided.
	if len(args) > 0 {
		for _, arg := range args {
			if strings.ContainsAny(arg, "*?") {
				processGlobPattern(arg, gitIgnore, *allFlag, excludeFlags, &builder, &st, *dryRunFlag)
			} else {
				processPath(arg, gitIgnore, *allFlag, excludeFlags, &builder, &st, *dryRunFlag)
			}
		}
	} else {
		walkDir(".", gitIgnore, *allFlag, excludeFlags, &builder, &st, *dryRunFlag)
	}

	// Always print the summary header first (in both dry-run and normal mode).
	printSummary(&st, 0, 0, *dryRunFlag, nil)
	fmt.Println()

	if *dryRunFlag {
		return
	}

	output := builder.String()

	// Print output to terminal.
	fmt.Print(output)

	// Copy output to system clipboard.
	if output != "" {
		if err := clipboard.WriteAll(output); err != nil {
			fmt.Fprintf(os.Stderr, "Error copying to clipboard: %v\n", err)
		}
	}
}
