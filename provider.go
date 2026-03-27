package usage

import (
	"context"
	"strings"
	"sync"

	openbindings "github.com/openbindings/openbindings-go"
	usagelib "github.com/openbindings/usage-go/usage"
)

const FormatToken = "usage@^2.0.0"

const DefaultSourceName = "usage"

// Provider implements BindingExecutor, InterfaceCreator, and ContextInfoProvider
// for usage-spec KDL.
type Provider struct {
	mu        sync.RWMutex
	specCache map[string]*usagelib.Spec
}

func New() *Provider {
	return &Provider{
		specCache: make(map[string]*usagelib.Spec),
	}
}

// cachedLoadSpec loads a usage spec, caching by location within a process.
// When content is provided, the cache is bypassed and updated with the fresh parse.
func (p *Provider) cachedLoadSpec(location string, content any) (*usagelib.Spec, error) {
	if location != "" && content == nil {
		p.mu.RLock()
		if spec, ok := p.specCache[location]; ok {
			p.mu.RUnlock()
			return spec, nil
		}
		p.mu.RUnlock()
	}

	spec, err := loadSpec(location, content)
	if err != nil {
		return nil, err
	}

	if location != "" {
		p.mu.Lock()
		p.specCache[location] = spec
		p.mu.Unlock()
	}
	return spec, nil
}

// GetContextInfo describes the context needed for a CLI binding.
func (p *Provider) GetContextInfo(_ context.Context, source openbindings.ExecuteSource, _ string) (*openbindings.ContextInfoResult, error) {
	spec, err := p.cachedLoadSpec(source.Location, source.Content)
	if err != nil {
		return nil, err
	}

	meta := spec.Meta()
	binName := meta.Bin
	if binName == "" {
		binName = meta.Name
	}
	if binName == "" {
		if strings.HasPrefix(source.Location, "exec:") {
			binName = strings.TrimPrefix(source.Location, "exec:")
			if idx := strings.IndexByte(binName, ' '); idx > 0 {
				binName = binName[:idx]
			}
		}
	}
	if binName == "" {
		return nil, nil
	}

	key := "exec:" + binName

	description := meta.Name
	if description == "" {
		description = binName
	}

	return &openbindings.ContextInfoResult{
		Key:         key,
		Required:    false,
		Description: description,
	}, nil
}

func (p *Provider) Formats() []string {
	return []string{FormatToken}
}

func (p *Provider) ExecuteBinding(ctx context.Context, in *openbindings.BindingExecutionInput) (*openbindings.ExecuteOutput, error) {
	return executeBindingCached(ctx, in, p.cachedLoadSpec), nil
}

func (p *Provider) CreateInterface(ctx context.Context, in *openbindings.CreateInput) (*openbindings.Interface, error) {
	if len(in.Sources) == 0 {
		return nil, &openbindings.ExecuteError{Code: "no_sources", Message: "no sources provided"}
	}
	src := in.Sources[0]

	spec, err := p.cachedLoadSpec(src.Location, src.Content)
	if err != nil {
		return nil, err
	}

	iface, err := convertToInterfaceWithSpec(spec, src.Location)
	if err != nil {
		return nil, err
	}

	if in.Name != "" {
		iface.Name = in.Name
	}
	if in.Version != "" {
		iface.Version = in.Version
	}
	if in.Description != "" {
		iface.Description = in.Description
	}

	return &iface, nil
}
