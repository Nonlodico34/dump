package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
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

// Check if path matches any custom exclude pattern.
func matchesCustomExcludes(path string, isDir bool, customExcludes []string) bool {
	slashPath := filepath.ToSlash(path)
	baseName := filepath.Base(slashPath)

	for _, pattern := range customExcludes {
		cleanPattern := filepath.ToSlash(pattern)

		// Check pattern against full path or base name
		if matched, _ := filepath.Match(cleanPattern, slashPath); matched {
			return true
		}
		if matched, _ := filepath.Match(cleanPattern, baseName); matched {
			return true
		}

		// Check directory prefix or substring match
		if strings.HasPrefix(slashPath, strings.TrimSuffix(cleanPattern, "/")) {
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

func main() {
	var excludeFlags arrayFlags

	allFlag := flag.Bool("all", false, "")
	flag.BoolVar(allFlag, "a", false, "")

	dryRunFlag := flag.Bool("dry-run", false, "")
	flag.BoolVar(dryRunFlag, "d", false, "")

	flag.Var(&excludeFlags, "exclude", "")
	flag.Var(&excludeFlags, "x", "")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: dump [options] [path...]\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fmt.Fprintf(os.Stderr, "  -a, --all        Include all files, ignoring .gitignore rules\n")
		fmt.Fprintf(os.Stderr, "  -d, --dry-run    List targeted files and show summary without dumping content\n")
		fmt.Fprintf(os.Stderr, "  -x, --exclude    Exclude files, directories, or patterns (can be specified multiple times)\n")
		fmt.Fprintf(os.Stderr, "  -h, --help       Show this help message\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  dump -d\n")
		fmt.Fprintf(os.Stderr, "  dump -x \"*.json\" -x \"node_modules/\"\n")
	}

	flag.Parse()

	args := flag.Args()
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
			info, err := os.Stat(arg)
			if err != nil {
				continue
			}

			if matchesCustomExcludes(arg, info.IsDir(), excludeFlags) {
				if *dryRunFlag {
					if info.IsDir() {
						st.ignoredCustom += countFilesDir(arg)
					} else {
						st.ignoredCustom++
					}
				}
				continue
			}

			if info.IsDir() {
				walkDir(arg, gitIgnore, *allFlag, excludeFlags, &builder, &st, *dryRunFlag)
			} else {
				if !*allFlag && gitIgnore != nil && gitIgnore.MatchesPath(filepath.ToSlash(arg)) {
					if *dryRunFlag {
						st.ignoredGit++
					}
					continue
				}
				appendFileDump(&builder, arg, &st, *dryRunFlag)
			}
		}
	} else {
		walkDir(".", gitIgnore, *allFlag, excludeFlags, &builder, &st, *dryRunFlag)
	}

	if *dryRunFlag {
		if len(st.includedFiles) > 0 {
			fmt.Println("Files to dump:")
			for _, file := range st.includedFiles {
				fmt.Printf("  %s\n", file)
			}
			fmt.Println()
		}

		fmt.Printf("Summary:\n")
		fmt.Printf("  Files included:                  %d\n", len(st.includedFiles))
		fmt.Printf("  Files ignored (.gitignore):      %d\n", st.ignoredGit)
		fmt.Printf("  Files ignored (custom -x):       %d\n", st.ignoredCustom)
		fmt.Printf("  Files ignored (non-text/binary): %d\n", st.ignoredBinary)
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
