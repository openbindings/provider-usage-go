package usage

import (
	"fmt"
	"strings"

	openbindings "github.com/openbindings/openbindings-go"
	"github.com/openbindings/openbindings-go/formattoken"
	"github.com/openbindings/usage-go/usage"
)

const (
	schemaTypeString  = "string"
	schemaTypeBoolean = "boolean"
	schemaTypeInteger = "integer"
	schemaTypeArray   = "array"
	schemaTypeObject  = "object"
)

type convertParams struct {
	toFormat  string
	inputPath string
	content   string
}

func convertToInterface(params convertParams) (openbindings.Interface, error) {
	if strings.TrimSpace(params.inputPath) == "" && strings.TrimSpace(params.content) == "" {
		return openbindings.Interface{}, fmt.Errorf("missing input path or content")
	}

	toFormat := strings.TrimSpace(params.toFormat)
	if toFormat == "" {
		toFormat = "openbindings@" + openbindings.MaxTestedVersion
	}

	toTok, err := formattoken.Parse(toFormat)
	if err != nil {
		return openbindings.Interface{}, fmt.Errorf("invalid target format %q", toFormat)
	}

	if toTok.Name != "openbindings" {
		return openbindings.Interface{}, fmt.Errorf("unsupported target format %q (expected openbindings@<ver>)", toFormat)
	}

	fromTok := formattoken.FormatToken{Name: "usage", Version: usage.MaxTestedVersion}
	ok, err := usage.IsSupportedVersion(fromTok.Version)
	if err != nil || !ok {
		return openbindings.Interface{}, fmt.Errorf("unsupported usage version %q (supported %s-%s)", fromTok.Version, usage.MinSupportedVersion, usage.MaxTestedVersion)
	}
	ok, err = openbindings.IsSupportedVersion(toTok.Version)
	if err != nil || !ok {
		return openbindings.Interface{}, fmt.Errorf("unsupported openbindings version %q (supported %s-%s)", toTok.Version, openbindings.MinSupportedVersion, openbindings.MaxTestedVersion)
	}

	var spec *usage.Spec
	if params.content != "" {
		spec, err = usage.ParseKDL([]byte(params.content))
	} else {
		spec, err = usage.ParseFile(params.inputPath)
	}
	if err != nil {
		return openbindings.Interface{}, err
	}

	return buildInterfaceFromSpec(spec, params.inputPath, fromTok.String(), toTok.Version)
}

// convertToInterfaceWithSpec builds an interface from a pre-loaded spec.
func convertToInterfaceWithSpec(spec *usage.Spec, location string) (openbindings.Interface, error) {
	fromTok := formattoken.FormatToken{Name: "usage", Version: usage.MaxTestedVersion}
	return buildInterfaceFromSpec(spec, location, fromTok.String(), openbindings.MaxTestedVersion)
}

func buildInterfaceFromSpec(spec *usage.Spec, location, formatStr, obVersion string) (openbindings.Interface, error) {
	meta := spec.Meta()

	sourceEntry := openbindings.Source{
		Format: formatStr,
	}
	if location != "" {
		sourceEntry.Location = location
	}

	iface := openbindings.Interface{
		OpenBindings: obVersion,
		Name:         meta.Name,
		Version:      meta.Version,
		Description:  meta.About,
		Operations:   map[string]openbindings.Operation{},
		Sources: map[string]openbindings.Source{
			DefaultSourceName: sourceEntry,
		},
	}

	bindings := map[string]openbindings.BindingEntry{}
	var dupes []string
	var schemaErr error

	walkWithGlobals(spec, func(path []string, cmd usage.Command, inheritedGlobals []usage.Flag) {
		if schemaErr != nil {
			return
		}
		if len(path) == 0 {
			return
		}
		if cmd.SubcommandRequired {
			return
		}
		opKey := strings.Join(path, ".")
		if override, ok := cmd.Node.Props["opKey"]; ok {
			if s := override.String(); s != "" {
				opKey = s
			}
		}
		if _, exists := iface.Operations[opKey]; exists {
			dupes = append(dupes, opKey)
			return
		}

		op := openbindings.Operation{
			Kind:        openbindings.OperationKindMethod,
			Description: cmd.Help,
		}

		if len(path) > 1 {
			op.Tags = make([]string, len(path)-1)
			copy(op.Tags, path[:len(path)-1])
		}

		for _, alias := range cmd.Aliases {
			for _, name := range alias.Names {
				if !alias.Hide {
					op.Aliases = append(op.Aliases, name)
				}
			}
		}

		inputSchema, err := generateInputSchema(cmd, inheritedGlobals)
		if err != nil {
			schemaErr = err
			return
		}
		if inputSchema != nil {
			op.Input = inputSchema
		}

		iface.Operations[opKey] = op
		bindingKey := opKey + "." + DefaultSourceName
		bindings[bindingKey] = openbindings.BindingEntry{
			Operation: opKey,
			Source:    DefaultSourceName,
			Ref:       strings.Join(path, " "),
		}
	})
	if schemaErr != nil {
		return openbindings.Interface{}, schemaErr
	}
	if len(dupes) > 0 {
		return openbindings.Interface{}, fmt.Errorf("duplicate command paths: %s", strings.Join(dupes, ", "))
	}
	iface.Bindings = bindings

	return iface, nil
}

func walkWithGlobals(spec *usage.Spec, fn func(path []string, cmd usage.Command, inheritedGlobals []usage.Flag)) {
	for _, cmd := range spec.Commands() {
		walkCommandWithGlobals(nil, cmd, nil, fn)
	}
}

func walkCommandWithGlobals(path []string, cmd usage.Command, inheritedGlobals []usage.Flag, fn func([]string, usage.Command, []usage.Flag)) {
	currentPath := make([]string, len(path)+1)
	copy(currentPath, path)
	currentPath[len(path)] = cmd.Name

	var newGlobals []usage.Flag
	newGlobals = append(newGlobals, inheritedGlobals...)
	for _, f := range cmd.Flags {
		if f.Global {
			newGlobals = append(newGlobals, f)
		}
	}

	fn(currentPath, cmd, inheritedGlobals)

	for _, sub := range cmd.Commands {
		walkCommandWithGlobals(currentPath, sub, newGlobals, fn)
	}
}

func generateInputSchema(cmd usage.Command, inheritedGlobals []usage.Flag) (map[string]any, error) {
	properties := make(map[string]any)
	seen := make(map[string]string)
	var required []string

	allFlags := cmd.AllFlags(inheritedGlobals)
	for _, flag := range allFlags {
		name := flag.PrimaryName()
		if name == "" {
			continue
		}

		if existing, ok := seen[name]; ok {
			return nil, fmt.Errorf("name collision in command %q: %q is used by both %s and flag --%s",
				cmd.Name, name, existing, name)
		}
		seen[name] = fmt.Sprintf("flag --%s", name)

		prop := generateFlagSchema(flag)
		if prop != nil {
			properties[name] = prop
		}
	}

	for _, arg := range cmd.Args {
		name := arg.CleanName()
		if name == "" {
			continue
		}

		if existing, ok := seen[name]; ok {
			return nil, fmt.Errorf("name collision in command %q: %q is used by both %s and arg <%s>",
				cmd.Name, name, existing, name)
		}
		seen[name] = fmt.Sprintf("arg <%s>", name)

		prop := generateArgSchema(arg)
		if prop != nil {
			properties[name] = prop
		}

		if arg.IsRequired() && arg.Default == nil {
			required = append(required, name)
		}
	}

	if len(properties) == 0 {
		return nil, nil
	}

	schema := map[string]any{
		"type":       schemaTypeObject,
		"properties": properties,
	}

	if len(required) > 0 {
		schema["required"] = required
	}

	return schema, nil
}

func generateFlagSchema(flag usage.Flag) map[string]any {
	prop := make(map[string]any)

	parsed := flag.ParseUsage()

	if flag.Count {
		prop["type"] = schemaTypeInteger
		if flag.Help != "" {
			prop["description"] = flag.Help
		}
		if flag.Default != nil {
			prop["default"] = flag.Default
		}
		return prop
	}

	takesValue := parsed.ArgName != "" || len(flag.Args) > 0

	if !takesValue {
		prop["type"] = schemaTypeBoolean
		if flag.Help != "" {
			prop["description"] = flag.Help
		}
		if flag.Default != nil {
			prop["default"] = flag.Default
		}
		return prop
	}

	if flag.Var {
		itemSchema := map[string]any{"type": schemaTypeString}
		if len(flag.Choices) > 0 {
			itemSchema["enum"] = flag.Choices
		}
		prop["type"] = schemaTypeArray
		prop["items"] = itemSchema
		if flag.VarMin != nil {
			prop["minItems"] = *flag.VarMin
		}
		if flag.VarMax != nil {
			prop["maxItems"] = *flag.VarMax
		}
	} else {
		prop["type"] = schemaTypeString
		if len(flag.Choices) > 0 {
			prop["enum"] = flag.Choices
		}
	}

	if flag.Help != "" {
		prop["description"] = flag.Help
	}
	if flag.Default != nil {
		prop["default"] = flag.Default
	}

	return prop
}

func generateArgSchema(arg usage.Arg) map[string]any {
	prop := make(map[string]any)

	if arg.IsVariadic() {
		itemSchema := map[string]any{"type": schemaTypeString}
		if len(arg.Choices) > 0 {
			itemSchema["enum"] = arg.Choices
		}
		prop["type"] = schemaTypeArray
		prop["items"] = itemSchema
		if arg.VarMin != nil {
			prop["minItems"] = *arg.VarMin
		} else if arg.IsRequired() && arg.Default == nil {
			prop["minItems"] = 1
		}
		if arg.VarMax != nil {
			prop["maxItems"] = *arg.VarMax
		}
	} else {
		prop["type"] = schemaTypeString
		if len(arg.Choices) > 0 {
			prop["enum"] = arg.Choices
		}
	}

	if arg.Help != "" {
		prop["description"] = arg.Help
	}
	if arg.Default != nil {
		prop["default"] = arg.Default
	}

	return prop
}
