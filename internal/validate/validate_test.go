package validate_test

import (
	"testing"

	"github.com/rophy/git-mcp/internal/validate"
	"github.com/stretchr/testify/assert"
)

func TestValidateRef(t *testing.T) {
	valid := []string{"main", "v1.0.0", "feature/foo", "abc1234", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"}
	for _, r := range valid {
		assert.NoError(t, validate.Ref(r), "should be valid: %s", r)
	}

	invalid := []string{
		"",
		"sha:path",
		"main@{1}",
		"main^{/regex}",
		"abc", // too short for hex
		"--option",
		"../escape",
		"main..other",
		"-leading-dash",
		"trailing.lock",
	}
	for _, r := range invalid {
		assert.Error(t, validate.Ref(r), "should be invalid: %s", r)
	}
}

func TestValidateRepo(t *testing.T) {
	valid := []string{"github.com/org/repo", "gitlab.com/team/project"}
	for _, r := range valid {
		assert.NoError(t, validate.Repo(r), "should be valid: %s", r)
	}

	invalid := []string{
		"",
		"github.com/org/../escape",
		"/absolute/path",
		"github.com/org/repo/../../x",
		"no-host",
		"github.com/-leading/repo",
	}
	for _, r := range invalid {
		assert.Error(t, validate.Repo(r), "should be invalid: %s", r)
	}
}

func TestValidatePath(t *testing.T) {
	valid := []string{"src/main.go", "README.md", "pkg/util/helper.go"}
	for _, p := range valid {
		assert.NoError(t, validate.Path(p), "should be valid: %s", p)
	}

	invalid := []string{
		"/absolute/path",
		"../escape",
		"src/../escape",
		".git/config",
		".git/HEAD",
		"src/.git/x",
	}
	for _, p := range invalid {
		assert.Error(t, validate.Path(p), "should be invalid: %s", p)
	}
}
