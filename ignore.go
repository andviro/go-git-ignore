/*
ignore is a library which returns a new ignorer object which can
test against various paths. This is particularly useful when trying
to filter files based on a .gitignore document

The rules for parsing the input file are the same as the ones listed
in the Git docs here: http://git-scm.com/docs/gitignore

The summarized version of the same has been copied here:

    1. A blank line matches no files, so it can serve as a separator
       for readability.
    2. A line starting with # serves as a comment. Put a backslash ("\")
       in front of the first hash for patterns that begin with a hash.
    3. Trailing spaces are ignored unless they are quoted with backslash ("\").
    4. An optional prefix "!" which negates the pattern; any matching file
       excluded by a previous pattern will become included again. It is not
       possible to re-include a file if a parent directory of that file is
       excluded. Git doesn’t list excluded directories for performance reasons,
       so any patterns on contained files have no effect, no matter where they
       are defined. Put a backslash ("\") in front of the first "!" for
       patterns that begin with a literal "!", for example, "\!important!.txt".
    5. If the pattern ends with a slash, it is removed for the purpose of the
       following description, but it would only find a match with a directory.
       In other words, foo/ will match a directory foo and paths underneath it,
       but will not match a regular file or a symbolic link foo (this is
       consistent with the way how pathspec works in general in Git).
    6. If the pattern does not contain a slash /, Git treats it as a shell glob
       pattern and checks for a match against the pathname relative to the
       location of the .gitignore file (relative to the toplevel of the work
       tree if not from a .gitignore file).
    7. Otherwise, Git treats the pattern as a shell glob suitable for
       consumption by fnmatch(3) with the FNM_PATHNAME flag: wildcards in the
       pattern will not match a / in the pathname. For example,
       "Documentation/*.html" matches "Documentation/git.html" but not
       "Documentation/ppc/ppc.html" or "tools/perf/Documentation/perf.html".
    8. A leading slash matches the beginning of the pathname. For example,
       "/*.c" matches "cat-file.c" but not "mozilla-sha1/sha1.c".
    9. Two consecutive asterisks ("**") in patterns matched against full
       pathname may have special meaning:
        i.   A leading "**" followed by a slash means match in all directories.
             For example, "** /foo" matches file or directory "foo" anywhere,
             the same as pattern "foo". "** /foo/bar" matches file or directory
             "bar" anywhere that is directly under directory "foo".
        ii.  A trailing "/**" matches everything inside. For example, "abc/**"
             matches all files inside directory "abc", relative to the location
             of the .gitignore file, with infinite depth.
        iii. A slash followed by two consecutive asterisks then a slash matches
             zero or more directories. For example, "a/** /b" matches "a/b",
             "a/x/b", "a/x/y/b" and so on.
        iv.  Other consecutive asterisks are considered invalid. */
package ignore

import (
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	Match = iota
	NonMatch
	Negation
)

// An IgnoreParser is an interface which exposes two methods:
//   MatchesPath() - Returns true if the path is targeted by the patterns compiled in the GitIgnore structure
type IgnoreParser interface {
	IncludesPath(f string) bool
	IgnoresPath(f string) bool
	MatchesPath(f string) bool
}

// GitIgnore is a struct which contains a slice of regexp.Regexp
// patterns
type GitIgnore struct {
	basePath string
	patterns []*regexp.Regexp // List of regexp patterns which this ignore file applies
	negate   []bool           // List of booleans which determine if the pattern is negated
}

// This function pretty much attempts to mimic the parsing rules
// listed above at the start of this file
func getPatternFromLine(line string) (*regexp.Regexp, bool) {
	// Trim OS-specific carriage returns.
	line = strings.TrimRight(line, "\r")

	// Strip comments [Rule 2]
	if regexp.MustCompile(`^#`).MatchString(line) {
		return nil, false
	}

	// Trim string [Rule 3]
	// TODO: Hanlde [Rule 3], when the " " is escaped with a \
	line = strings.Trim(line, " ")

	// Exit for no-ops and return nil which will prevent us from
	// appending a pattern against this line
	if line == "" {
		return nil, false
	}

	// TODO: Handle [Rule 4] which negates the match for patterns leading with "!"
	negatePattern := false
	if string(line[0]) == "!" {
		negatePattern = true
		line = line[1:]
	}

	// Handle [Rule 2, 4], when # or ! is escaped with a \
	// Handle [Rule 4] once we tag negatePattern, strip the leading ! char
	if regexp.MustCompile(`^(\#|\!)`).MatchString(line) {
		line = line[1:]
	}

	// Handle [Rule 8], strip leading / and enforce path checking if its present
	if regexp.MustCompile(`^/`).MatchString(line) {
		line = "^" + line[1:]
	}

	// If we encounter a foo/*.blah in a folder, prepend the ^ char
	if regexp.MustCompile(`([^\/+])/.*\*\.`).MatchString(line) {
		line = "^" + line
	}

	// Handle escaping the "." char
	line = regexp.MustCompile(`\.`).ReplaceAllString(line, `\.`)

	// Handle "**" usage (and special case when it is followed by a /)
	line = regexp.MustCompile(`\*\*(/|)`).ReplaceAllString(line, `(.+|)`)

	// Handle escaping the "*" char
	line = regexp.MustCompile(`\*`).ReplaceAllString(line, `([^\/]+)`)

	// Temporary regex
	expr := line + "(|/.+)$"
	pattern, _ := regexp.Compile(expr)

	return pattern, negatePattern
}

// CompileIgnoreLines accepts a variadic set of strings, and returns a GitIgnore object which
// converts and appends the lines in the input to regexp.Regexp patterns
// held within the GitIgnore objects "patterns" field
func CompileIgnoreLines(lines ...string) (*GitIgnore, error) {
	g := new(GitIgnore)
	for _, line := range lines {
		pattern, negatePattern := getPatternFromLine(line)
		if pattern != nil {
			g.patterns = append(g.patterns, pattern)
			g.negate = append(g.negate, negatePattern)
		}
	}
	return g, nil
}

// CompileIgnoreFile accepts a ignore file as the input, parses the lines out of the file
// and invokes the CompileIgnoreLines method. Note that the location
// of a .gitignore file is taken into account for relative filename matching.
func CompileIgnoreFile(fpath string) (*GitIgnore, error) {
	buffer, err := ioutil.ReadFile(fpath)
	if err != nil {
		return nil, err
	}
	s := strings.Split(string(buffer), "\n")
	res, err := CompileIgnoreLines(s...)
	if err != nil {
		return nil, err
	}
	res.basePath = filepath.Dir(fpath)
	return res, nil
}

// MatchesPath is an interface function for the IgnoreParser interface.
// It returns true if the given GitIgnore structure would target a given
// path string "f"
func (g GitIgnore) MatchesPath(f string) int {
	// Replace OS-specific path separator.
	f = filepath.ToSlash(f)

	// Make file path relative to location of .gitignore file if possible
	relFp, err := filepath.Rel(g.basePath, f)
	if err == nil {
		f = relFp
	}

	matchesPath := NonMatch
	for idx, pattern := range g.patterns {
		if pattern.MatchString(f) {
			// If this is a regular target (not negated with a gitignore exclude "!" etc)
			if !g.negate[idx] {
				matchesPath = Match
				// Negated pattern, and matchesPath is already set
			} else if matchesPath == Match {
				matchesPath = Negation
			}
		}
	}
	return matchesPath
}
