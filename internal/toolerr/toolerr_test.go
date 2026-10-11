package toolerr_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/rophy/git-mcp/internal/toolerr"
	"github.com/stretchr/testify/assert"
)

func TestCodeOf(t *testing.T) {
	err := toolerr.New(toolerr.RefNotFound, errors.New("ref x not found"))
	assert.Equal(t, toolerr.RefNotFound, toolerr.CodeOf(err))
	assert.Equal(t, "ref x not found", err.Error())

	wrapped := fmt.Errorf("context: %w", err)
	assert.Equal(t, toolerr.RefNotFound, toolerr.CodeOf(wrapped))

	assert.Equal(t, toolerr.Internal, toolerr.CodeOf(errors.New("plain")))
}

func TestErrorf(t *testing.T) {
	err := toolerr.Errorf(toolerr.InvalidInput, "bad %s", "path")
	assert.Equal(t, toolerr.InvalidInput, toolerr.CodeOf(err))
	assert.EqualError(t, err, "bad path")
}
