package validate

import (
	"regexp"
	"strings"

	"github.com/rophy/git-mcp/internal/toolerr"
)

var (
	hexSHARegex  = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	refNameRegex = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._/-]*$`)
	allHexRegex  = regexp.MustCompile(`^[0-9a-f]+$`)
)

func Ref(ref string) error {
	if ref == "" {
		return toolerr.Errorf(toolerr.InvalidInput, "ref is empty")
	}
	if hexSHARegex.MatchString(ref) {
		return nil
	}
	if allHexRegex.MatchString(ref) {
		return toolerr.Errorf(toolerr.InvalidInput, "ref looks like a hex SHA but has invalid length: %q", ref)
	}
	if !refNameRegex.MatchString(ref) {
		return toolerr.Errorf(toolerr.InvalidInput, "ref contains invalid characters: %q", ref)
	}
	if strings.Contains(ref, "..") {
		return toolerr.Errorf(toolerr.InvalidInput, "ref contains '..': %q", ref)
	}
	if strings.Contains(ref, "@{") {
		return toolerr.Errorf(toolerr.InvalidInput, "ref contains '@{': %q", ref)
	}
	if strings.Contains(ref, "^{") {
		return toolerr.Errorf(toolerr.InvalidInput, "ref contains '^{': %q", ref)
	}
	if strings.HasPrefix(ref, "-") {
		return toolerr.Errorf(toolerr.InvalidInput, "ref starts with '-': %q", ref)
	}
	if strings.HasSuffix(ref, ".lock") {
		return toolerr.Errorf(toolerr.InvalidInput, "ref ends with '.lock': %q", ref)
	}
	return nil
}

func Repo(repo string) error {
	if repo == "" {
		return toolerr.Errorf(toolerr.InvalidInput, "repo is empty")
	}
	parts := strings.Split(repo, "/")
	if len(parts) < 3 {
		return toolerr.Errorf(toolerr.InvalidInput, "repo must be host/owner/name: %q", repo)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return toolerr.Errorf(toolerr.InvalidInput, "repo contains invalid segment: %q", repo)
		}
		if strings.HasPrefix(part, "-") {
			return toolerr.Errorf(toolerr.InvalidInput, "repo segment starts with '-': %q", repo)
		}
		if !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(part) {
			return toolerr.Errorf(toolerr.InvalidInput, "repo segment contains invalid characters: %q", repo)
		}
	}
	return nil
}

func Path(path string) error {
	if path == "" {
		return nil
	}
	if strings.HasPrefix(path, "/") {
		return toolerr.Errorf(toolerr.InvalidInput, "path is absolute: %q", path)
	}
	if strings.HasPrefix(path, "-") {
		return toolerr.Errorf(toolerr.InvalidInput, "path must not start with -: %q", path)
	}
	if strings.ContainsRune(path, 0) {
		return toolerr.Errorf(toolerr.InvalidInput, "path contains null byte")
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == ".." {
			return toolerr.Errorf(toolerr.InvalidInput, "path contains '..': %q", path)
		}
		if part == ".git" {
			return toolerr.Errorf(toolerr.InvalidInput, "path accesses .git: %q", path)
		}
	}
	return nil
}

func Pattern(pattern string) error {
	if pattern == "" {
		return toolerr.Errorf(toolerr.InvalidInput, "pattern is empty")
	}
	if strings.HasPrefix(pattern, "-") {
		return toolerr.Errorf(toolerr.InvalidInput, "pattern must not start with -")
	}
	return nil
}
