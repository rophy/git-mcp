package validate

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	hexSHARegex  = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	refNameRegex = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._/-]*$`)
	allHexRegex  = regexp.MustCompile(`^[0-9a-f]+$`)
)

func Ref(ref string) error {
	if ref == "" {
		return fmt.Errorf("ref is empty")
	}
	if hexSHARegex.MatchString(ref) {
		return nil
	}
	if allHexRegex.MatchString(ref) {
		return fmt.Errorf("ref looks like a hex SHA but has invalid length: %q", ref)
	}
	if !refNameRegex.MatchString(ref) {
		return fmt.Errorf("ref contains invalid characters: %q", ref)
	}
	if strings.Contains(ref, "..") {
		return fmt.Errorf("ref contains '..': %q", ref)
	}
	if strings.Contains(ref, "@{") {
		return fmt.Errorf("ref contains '@{': %q", ref)
	}
	if strings.Contains(ref, "^{") {
		return fmt.Errorf("ref contains '^{': %q", ref)
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("ref starts with '-': %q", ref)
	}
	if strings.HasSuffix(ref, ".lock") {
		return fmt.Errorf("ref ends with '.lock': %q", ref)
	}
	return nil
}

func Repo(repo string) error {
	if repo == "" {
		return fmt.Errorf("repo is empty")
	}
	parts := strings.Split(repo, "/")
	if len(parts) < 3 {
		return fmt.Errorf("repo must be host/owner/name: %q", repo)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("repo contains invalid segment: %q", repo)
		}
		if strings.HasPrefix(part, "-") {
			return fmt.Errorf("repo segment starts with '-': %q", repo)
		}
		if !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(part) {
			return fmt.Errorf("repo segment contains invalid characters: %q", repo)
		}
	}
	return nil
}

func Path(path string) error {
	if path == "" {
		return nil
	}
	if strings.HasPrefix(path, "/") {
		return fmt.Errorf("path is absolute: %q", path)
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == ".." {
			return fmt.Errorf("path contains '..': %q", path)
		}
		if part == ".git" {
			return fmt.Errorf("path accesses .git: %q", path)
		}
	}
	return nil
}

func Pattern(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("pattern is empty")
	}
	return nil
}
