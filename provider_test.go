package usage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	openbindings "github.com/openbindings/openbindings-go"
)

func TestGetContextInfo_WithBin(t *testing.T) {
	kdl := `name "My CLI"
bin "mycli"
`
	dir := t.TempDir()
	specPath := filepath.Join(dir, "mycli.kdl")
	if err := os.WriteFile(specPath, []byte(kdl), 0644); err != nil {
		t.Fatal(err)
	}

	p := New()
	result, err := p.GetContextInfo(context.Background(), openbindings.ExecuteSource{
		Location: specPath,
	}, "")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Key != "exec:mycli" {
		t.Errorf("expected key 'exec:mycli', got %q", result.Key)
	}
	if result.Required {
		t.Error("usage context should not be required")
	}
	if result.Description != "My CLI" {
		t.Errorf("expected description 'My CLI', got %q", result.Description)
	}
}

func TestGetContextInfo_FallbackToName(t *testing.T) {
	kdl := `name "kubectl"
`
	dir := t.TempDir()
	specPath := filepath.Join(dir, "kubectl.kdl")
	if err := os.WriteFile(specPath, []byte(kdl), 0644); err != nil {
		t.Fatal(err)
	}

	p := New()
	result, err := p.GetContextInfo(context.Background(), openbindings.ExecuteSource{
		Location: specPath,
	}, "")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Key != "exec:kubectl" {
		t.Errorf("expected key 'exec:kubectl' (from name fallback), got %q", result.Key)
	}
}

func TestGetContextInfo_NoBinOrName(t *testing.T) {
	kdl := `about "No identity"
`
	dir := t.TempDir()
	specPath := filepath.Join(dir, "noname.kdl")
	if err := os.WriteFile(specPath, []byte(kdl), 0644); err != nil {
		t.Fatal(err)
	}

	p := New()
	result, err := p.GetContextInfo(context.Background(), openbindings.ExecuteSource{
		Location: specPath,
	}, "")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result when no binary name can be determined, got key=%q", result.Key)
	}
}

func TestGetContextInfo_ContentString(t *testing.T) {
	kdl := `name "docker"
bin "docker"
`
	p := New()
	result, err := p.GetContextInfo(context.Background(), openbindings.ExecuteSource{
		Content: kdl,
	}, "")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Key != "exec:docker" {
		t.Errorf("expected key 'exec:docker', got %q", result.Key)
	}
}
