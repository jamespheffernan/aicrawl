package app

import (
	"errors"
	"fmt"
	"strings"
)

type globalOptions struct {
	configPath string
	format     string
	help       bool
	version    bool
}

type parsedOptions struct {
	positionals []string
	values      map[string]string
	bools       map[string]bool
}

func extractGlobalOptions(args []string) ([]string, globalOptions, error) {
	opts := globalOptions{format: "text"}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			opts.help = true
		case arg == "--version":
			opts.version = true
		case arg == "--json":
			opts.format = "json"
		case arg == "--format":
			if i+1 >= len(args) {
				return nil, opts, errors.New("--format requires a value")
			}
			i++
			opts.format = args[i]
		case strings.HasPrefix(arg, "--format="):
			opts.format = strings.TrimPrefix(arg, "--format=")
		case arg == "--config":
			if i+1 >= len(args) {
				return nil, opts, errors.New("--config requires a value")
			}
			i++
			opts.configPath = args[i]
		case strings.HasPrefix(arg, "--config="):
			opts.configPath = strings.TrimPrefix(arg, "--config=")
		default:
			out = append(out, arg)
		}
	}
	switch opts.format {
	case "", "text":
		opts.format = "text"
	case "json":
	default:
		return nil, opts, fmt.Errorf("unsupported format %q", opts.format)
	}
	return out, opts, nil
}

func parseOptions(args []string, boolNames map[string]bool, valueNames map[string]bool) (parsedOptions, error) {
	parsed := parsedOptions{
		values: map[string]string{},
		bools:  map[string]bool{},
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") || arg == "--" {
			if arg == "--" {
				parsed.positionals = append(parsed.positionals, args[i+1:]...)
				break
			}
			parsed.positionals = append(parsed.positionals, arg)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if boolNames[name] {
			if hasValue {
				switch value {
				case "true", "1", "yes":
					parsed.bools[name] = true
				case "false", "0", "no":
					parsed.bools[name] = false
				default:
					return parsedOptions{}, fmt.Errorf("--%s expects a boolean value", name)
				}
			} else {
				parsed.bools[name] = true
			}
			continue
		}
		if !valueNames[name] {
			return parsedOptions{}, fmt.Errorf("unknown option --%s", name)
		}
		if !hasValue {
			if i+1 >= len(args) {
				return parsedOptions{}, fmt.Errorf("--%s requires a value", name)
			}
			i++
			value = args[i]
		}
		parsed.values[name] = value
	}
	return parsed, nil
}

func boolSet(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

func valueSet(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}
