package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/google/shlex"
	openbindings "github.com/openbindings/openbindings-go"
	"github.com/openbindings/usage-go/usage"
)

const ResolveArtifactTimeout = 5 * time.Second

type specLoader func(location string, content any) (*usage.Spec, error)

func executeBindingCached(ctx context.Context, input *openbindings.BindingExecutionInput, loader specLoader) *openbindings.ExecuteOutput {
	return executeBindingInternal(ctx, input, loader)
}

func executeBinding(ctx context.Context, input *openbindings.BindingExecutionInput) *openbindings.ExecuteOutput {
	return executeBindingInternal(ctx, input, func(loc string, content any) (*usage.Spec, error) {
		return loadSpec(loc, content)
	})
}

func executeBindingInternal(ctx context.Context, input *openbindings.BindingExecutionInput, loader specLoader) *openbindings.ExecuteOutput {
	start := time.Now()

	var binName string
	var args []string

	binary := metadataBinary(input.Options)

	if binary != "" {
		binName = binary
		var err error
		args, err = buildDirectArgsFromRef(input.Ref, input.Input)
		if err != nil {
			return openbindings.FailedOutput(start, "args_build_failed", err.Error())
		}
	} else {
		spec, err := loader(input.Source.Location, input.Source.Content)
		if err != nil {
			return openbindings.FailedOutput(start, "spec_load_failed", err.Error())
		}

		found, err := findCommand(spec, input.Ref)
		if err != nil {
			return openbindings.FailedOutput(start, "command_not_found", err.Error())
		}

		meta := spec.Meta()
		binName = meta.Bin
		if binName == "" {
			binName = meta.Name
		}
		if binName == "" {
			return openbindings.FailedOutput(start, "no_binary", "usage spec does not define a binary name (bin or name)")
		}

		args, err = buildCLIArgs(found.path, found.cmd, found.inheritedFlags, input.Input)
		if err != nil {
			return openbindings.FailedOutput(start, "args_build_failed", err.Error())
		}
	}

	output, status, err := runCLI(ctx, binName, args, input.Options)
	duration := time.Since(start).Milliseconds()

	if ctx.Err() != nil {
		return &openbindings.ExecuteOutput{
			DurationMs: duration,
			Error: &openbindings.ExecuteError{
				Code:    "cancelled",
				Message: "operation cancelled",
			},
		}
	}

	if err != nil {
		return &openbindings.ExecuteOutput{
			Output:     output,
			Status:     status,
			DurationMs: duration,
			Error: &openbindings.ExecuteError{
				Code:    "execution_failed",
				Message: err.Error(),
			},
		}
	}

	return &openbindings.ExecuteOutput{
		Output:     output,
		Status:     status,
		DurationMs: duration,
	}
}

// metadataBinary extracts the "binary" hint from execution options metadata.
func metadataBinary(opts *openbindings.ExecutionOptions) string {
	if opts == nil || opts.Metadata == nil {
		return ""
	}
	if b, ok := opts.Metadata["binary"].(string); ok {
		return b
	}
	return ""
}

func buildDirectArgsFromRef(ref string, input any) ([]string, error) {
	args, err := shlex.Split(ref)
	if err != nil {
		return nil, err
	}

	if input == nil {
		return args, nil
	}

	inputMap, ok := openbindings.ToStringAnyMap(input)
	if !ok {
		return args, nil
	}

	names := make([]string, 0, len(inputMap))
	for name := range inputMap {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		flagArgs, err := formatFlagWithDef(name, inputMap[name], usage.Flag{})
		if err != nil {
			return nil, fmt.Errorf("format flag %q: %w", name, err)
		}
		args = append(args, flagArgs...)
	}

	return args, nil
}

func loadSpec(location string, content any) (*usage.Spec, error) {
	// Prefer inline content when provided — avoids redundant disk reads when
	// callers (e.g. Sync) already have fresh bytes.
	if content != nil {
		switch c := content.(type) {
		case string:
			spec, err := usage.ParseKDL([]byte(c))
			if err != nil {
				return nil, fmt.Errorf("parse usage content: %w", err)
			}
			return spec, nil
		default:
			return nil, fmt.Errorf("unsupported content type %T (expected string)", content)
		}
	}

	if location != "" {
		if strings.HasPrefix(location, "exec:") {
			resolved, err := resolveCommandArtifact(location)
			if err != nil {
				return nil, fmt.Errorf("resolve cmd artifact: %w", err)
			}
			spec, err := usage.ParseKDL([]byte(resolved))
			if err != nil {
				return nil, fmt.Errorf("parse usage content from exec: %w", err)
			}
			return spec, nil
		}

		spec, err := usage.ParseFile(location)
		if err != nil {
			return nil, fmt.Errorf("parse usage spec: %w", err)
		}
		return spec, nil
	}

	return nil, fmt.Errorf("source must have location or content")
}

type findCommandResult struct {
	path           []string
	cmd            *usage.Command
	inheritedFlags []usage.Flag
}

func findCommand(spec *usage.Spec, ref string) (*findCommandResult, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("ref is empty")
	}

	targetPath := strings.Fields(ref)
	var result *findCommandResult

	var walk func(cmds []usage.Command, path []string, inheritedGlobals []usage.Flag)
	walk = func(cmds []usage.Command, path []string, inheritedGlobals []usage.Flag) {
		for _, cmd := range cmds {
			cmdPath := make([]string, len(path)+1)
			copy(cmdPath, path)
			cmdPath[len(path)] = cmd.Name

			var globalsForChildren []usage.Flag
			globalsForChildren = append(globalsForChildren, inheritedGlobals...)
			for _, f := range cmd.Flags {
				if f.Global {
					globalsForChildren = append(globalsForChildren, f)
				}
			}

			if pathMatchesWithAliases(cmdPath, targetPath, cmd) {
				cmdCopy := cmd
				result = &findCommandResult{
					path:           cmdPath,
					cmd:            &cmdCopy,
					inheritedFlags: inheritedGlobals,
				}
			}

			walk(cmd.Commands, cmdPath, globalsForChildren)
		}
	}

	walk(spec.Commands(), nil, nil)

	if result == nil {
		return nil, fmt.Errorf("command %q not found in usage spec", ref)
	}

	return result, nil
}

func pathMatchesWithAliases(cmdPath, targetPath []string, cmd usage.Command) bool {
	if len(cmdPath) != len(targetPath) {
		return false
	}
	if len(cmdPath) == 0 {
		return true
	}

	for i := 0; i < len(cmdPath)-1; i++ {
		if cmdPath[i] != targetPath[i] {
			return false
		}
	}

	lastIdx := len(cmdPath) - 1
	targetName := targetPath[lastIdx]

	if cmdPath[lastIdx] == targetName {
		return true
	}

	for _, alias := range cmd.Aliases {
		for _, name := range alias.Names {
			if name == targetName {
				return true
			}
		}
	}

	return false
}

func buildCLIArgs(cmdPath []string, cmd *usage.Command, inheritedGlobals []usage.Flag, input any) ([]string, error) {
	var args []string
	args = append(args, cmdPath...)

	if input == nil {
		return args, nil
	}

	inputMap, ok := openbindings.ToStringAnyMap(input)
	if !ok {
		return nil, fmt.Errorf("input must be an object with field names matching the command's flags and args")
	}

	flagDefs := make(map[string]usage.Flag)
	for _, f := range cmd.AllFlags(inheritedGlobals) {
		name := f.PrimaryName()
		if name != "" {
			flagDefs[name] = f
		}
		parsed := f.ParseUsage()
		for _, short := range parsed.Short {
			flagDefs[short] = f
		}
		for _, long := range parsed.Long {
			flagDefs[long] = f
		}
	}

	type argDef struct {
		name      string
		cleanName string
		def       usage.Arg
	}
	var argDefs []argDef
	for _, a := range cmd.Args {
		argDefs = append(argDefs, argDef{
			name:      a.Name,
			cleanName: a.CleanName(),
			def:       a,
		})
	}

	processed := make(map[string]bool)

	sortedKeys := make([]string, 0, len(inputMap))
	for key := range inputMap {
		sortedKeys = append(sortedKeys, key)
	}
	sort.Strings(sortedKeys)
	for _, key := range sortedKeys {
		value := inputMap[key]
		if flagDef, isFlag := flagDefs[key]; isFlag {
			flagArgs, err := formatFlagWithDef(key, value, flagDef)
			if err != nil {
				return nil, fmt.Errorf("flag %q: %w", key, err)
			}
			args = append(args, flagArgs...)
			processed[key] = true
		}
	}

	doubleDashInserted := false

	for _, ad := range argDefs {
		value, exists := inputMap[ad.cleanName]
		if !exists {
			continue
		}
		processed[ad.cleanName] = true

		if !doubleDashInserted && (ad.def.DoubleDash == "required" || ad.def.DoubleDash == "optional") {
			args = append(args, "--")
			doubleDashInserted = true
		}

		switch v := value.(type) {
		case []any:
			for _, item := range v {
				args = append(args, fmt.Sprintf("%v", item))
			}
		case []string:
			args = append(args, v...)
		case string:
			args = append(args, v)
		case nil:
		default:
			args = append(args, fmt.Sprintf("%v", v))
		}
	}

	for key := range inputMap {
		if !processed[key] {
			return nil, fmt.Errorf("unknown field %q: not defined as a flag or arg in the usage spec for this command", key)
		}
	}

	return args, nil
}

func formatFlagWithDef(name string, value any, flagDef usage.Flag) ([]string, error) {
	prefix := "--"
	if len(name) == 1 {
		prefix = "-"
	}
	flagName := prefix + name

	if flagDef.Count {
		count := 0
		switch v := value.(type) {
		case int:
			count = v
		case int64:
			count = int(v)
		case float64:
			count = int(v)
		case bool:
			if v {
				count = 1
			}
		}
		if count <= 0 {
			return nil, nil
		}
		var args []string
		for i := 0; i < count; i++ {
			args = append(args, flagName)
		}
		return args, nil
	}

	switch v := value.(type) {
	case bool:
		if v {
			return []string{flagName}, nil
		}
		if flagDef.Negate != "" {
			return []string{flagDef.Negate}, nil
		}
		return nil, nil
	case string:
		return []string{flagName, v}, nil
	case float64:
		return []string{flagName, fmt.Sprintf("%v", v)}, nil
	case int, int64:
		return []string{flagName, fmt.Sprintf("%d", v)}, nil
	case []any:
		var args []string
		for _, item := range v {
			args = append(args, flagName, fmt.Sprintf("%v", item))
		}
		return args, nil
	case nil:
		return nil, nil
	default:
		return []string{flagName, fmt.Sprintf("%v", v)}, nil
	}
}

func runCLI(ctx context.Context, binName string, args []string, opts *openbindings.ExecutionOptions) (any, int, error) {
	cmd := exec.CommandContext(ctx, binName, args...)

	if opts != nil && len(opts.Environment) > 0 {
		cmd.Env = os.Environ()
		for k, v := range opts.Environment {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, 1, err
		}
	}

	stdoutStr := stdout.String()
	stderrStr := stderr.String()

	if exitCode == 0 && len(stdoutStr) > 0 {
		trimmed := strings.TrimSpace(stdoutStr)
		if openbindings.MaybeJSON(trimmed) {
			var parsed any
			if json.Unmarshal([]byte(trimmed), &parsed) == nil {
				if stderrStr != "" {
					return map[string]any{
						"data":   parsed,
						"stderr": stderrStr,
					}, 0, nil
				}
				return parsed, 0, nil
			}
		}
	}

	output := map[string]any{
		"stdout": stdoutStr,
	}
	if stderrStr != "" {
		output["stderr"] = stderrStr
	}

	return output, exitCode, nil
}

func resolveCommandArtifact(location string) (string, error) {
	cmdStr := strings.TrimPrefix(location, "exec:")
	if cmdStr == "" {
		return "", fmt.Errorf("empty command in exec: artifact")
	}

	parts, err := shlex.Split(cmdStr)
	if err != nil {
		return "", fmt.Errorf("invalid command syntax: %w", err)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("empty command in exec: artifact")
	}

	binName := parts[0]
	args := parts[1:]

	ctx, cancel := context.WithTimeout(context.Background(), ResolveArtifactTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binName, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			msg := strings.TrimSpace(stderr.String())
			const maxStderr = 256
			if len(msg) > maxStderr {
				msg = msg[:maxStderr] + "..."
			}
			return "", fmt.Errorf("command failed: %s", msg)
		}
		return "", fmt.Errorf("command failed: %w", err)
	}

	return stdout.String(), nil
}
