package tools

import (
	"context"

	"github.com/rophy/git-mcp/internal/cache"
	"github.com/rophy/git-mcp/internal/validate"
)

type ResolveRefInput struct {
	Repo    string `json:"repo"`
	Ref     string `json:"ref"`
	Refresh bool   `json:"refresh"`
}

type ResolveRefOutput struct {
	SHA string `json:"sha"`
}

type ResolveRef struct {
	cache *cache.Manager
}

func NewResolveRef(cache *cache.Manager) *ResolveRef {
	return &ResolveRef{cache: cache}
}

func (h *ResolveRef) Execute(ctx context.Context, input ResolveRefInput) (*ResolveRefOutput, error) {
	if err := validate.Repo(input.Repo); err != nil {
		return nil, err
	}
	if err := validate.Ref(input.Ref); err != nil {
		return nil, err
	}

	sha, err := h.cache.Ensure(ctx, input.Repo, input.Ref, input.Refresh)
	if err != nil {
		return nil, err
	}

	return &ResolveRefOutput{SHA: sha}, nil
}
