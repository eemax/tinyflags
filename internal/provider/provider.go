package provider

import (
	"context"
	"fmt"

	"github.com/eemax/tinyflags/internal/core"
)

type Provider interface {
	Complete(ctx context.Context, req core.CompletionRequest) (core.CompletionResponse, error)
}

type Registry struct {
	providers map[string]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}}
}

func (r *Registry) Register(name string, p Provider) {
	if r.providers == nil {
		r.providers = map[string]Provider{}
	}
	r.providers[name] = p
}

func (r *Registry) Get(name string) (Provider, bool) {
	if r == nil {
		return nil, false
	}
	p, ok := r.providers[name]
	return p, ok
}

func (r *Registry) MustGet(name string) (Provider, error) {
	p, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", name)
	}
	return p, nil
}
