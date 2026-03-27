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

// Provider implements BindingExecutor and InterfaceCreator for usage-spec KDL.
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

func (p *Provider) Formats() []string {
	return []string{FormatToken}
}

func (p *Provider) ExecuteBinding(ctx context.Context, in *openbindings.BindingExecutionInput) (*openbindings.ExecuteOutput, error) {
	if in.Store != nil {
		key := resolveUsageKey(in.Source.Location, in.Source.Content, p.cachedLoadSpec)
		if key != "" {
			if stored, err := in.Store.Get(ctx, key); err == nil && len(stored) > 0 {
				if len(in.Context) == 0 {
					in.Context = stored
				} else {
					merged := make(map[string]any, len(stored)+len(in.Context))
					for k, v := range stored {
						merged[k] = v
					}
					for k, v := range in.Context {
						merged[k] = v
					}
					in.Context = merged
				}
			}
		}
	}

	return executeBindingCached(ctx, in, p.cachedLoadSpec), nil
}

func resolveUsageKey(location string, content any, loader func(string, any) (*usagelib.Spec, error)) string {
	spec, err := loader(location, content)
	if err != nil {
		return ""
	}
	meta := spec.Meta()
	binName := meta.Bin
	if binName == "" {
		binName = meta.Name
	}
	if binName == "" {
		if strings.HasPrefix(location, "exec:") {
			binName = strings.TrimPrefix(location, "exec:")
			if idx := strings.IndexByte(binName, ' '); idx > 0 {
				binName = binName[:idx]
			}
		}
	}
	if binName == "" {
		return ""
	}
	return "exec:" + binName
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
