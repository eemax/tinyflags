package provider_test

import (
	"context"
	"testing"

	"github.com/eemax/tinyflags/internal/core"
	"github.com/eemax/tinyflags/internal/provider"
)

type fakeProvider struct{}

func (f *fakeProvider) Complete(ctx context.Context, req core.CompletionRequest) (core.CompletionResponse, error) {
	return core.CompletionResponse{}, nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register("test", &fakeProvider{})

	p, ok := reg.Get("test")
	if !ok || p == nil {
		t.Fatal("expected provider to be registered")
	}

	_, ok = reg.Get("missing")
	if ok {
		t.Fatal("expected false for unregistered provider")
	}
}

func TestRegistryMustGet(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register("test", &fakeProvider{})

	p, err := reg.MustGet("test")
	if err != nil || p == nil {
		t.Fatalf("MustGet returned error: %v", err)
	}

	_, err = reg.MustGet("missing")
	if err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestNilRegistryGet(t *testing.T) {
	var reg *provider.Registry
	_, ok := reg.Get("test")
	if ok {
		t.Fatal("expected false for nil registry")
	}
}
