package usage

import (
	"context"

	openbindings "github.com/openbindings/openbindings-go"
)

const FormatToken = "usage@^2.0.0"

const DefaultSourceName = "usage"

// Provider implements BindingExecutor and InterfaceCreator for usage-spec KDL.
type Provider struct{}

func New() *Provider {
	return &Provider{}
}

func (p *Provider) Formats() []string {
	return []string{FormatToken}
}

func (p *Provider) ExecuteBinding(ctx context.Context, in *openbindings.BindingExecutionInput) (*openbindings.ExecuteOutput, error) {
	return executeBinding(ctx, in), nil
}

func (p *Provider) CreateInterface(ctx context.Context, in *openbindings.CreateInput) (*openbindings.Interface, error) {
	if len(in.Sources) == 0 {
		return nil, &openbindings.ExecuteError{Code: "no_sources", Message: "no sources provided"}
	}
	src := in.Sources[0]

	params := convertParams{
		toFormat:  "openbindings@" + openbindings.MaxTestedVersion,
		inputPath: src.Location,
	}
	if src.Content != nil {
		if s, ok := src.Content.(string); ok {
			params.content = s
		}
	}

	iface, err := convertToInterface(params)
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
