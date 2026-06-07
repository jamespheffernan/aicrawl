package app

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type launchdResult struct {
	Path             string   `json:"path"`
	Label            string   `json:"label"`
	Provider         string   `json:"provider"`
	IntervalSeconds  int      `json:"interval_seconds"`
	MaxConversations int      `json:"max_conversations"`
	ProgramArguments []string `json:"program_arguments"`
	NextSteps        []string `json:"next_steps"`
}

func (a *App) schedule(ctx context.Context, globals globalOptions, args []string) error {
	if len(args) == 0 || args[0] != "launchd" {
		return withExitCode(2, fmt.Errorf("schedule requires subcommand launchd"))
	}
	return a.scheduleLaunchd(ctx, globals, args[1:])
}

func (a *App) scheduleLaunchd(ctx context.Context, globals globalOptions, args []string) error {
	parsed, err := parseOptions(args, boolSet("json"), valueSet("provider", "cdp-url", "interval-minutes", "max-conversations", "out", "aicrawl-bin", "label"))
	if err != nil {
		return withExitCode(2, err)
	}
	if len(parsed.positionals) != 0 {
		return withExitCode(2, fmt.Errorf("schedule launchd does not accept positional arguments"))
	}
	provider, err := webProvider(parsed.values["provider"])
	if err != nil {
		return withExitCode(2, err)
	}
	cdpURL := strings.TrimSpace(parsed.values["cdp-url"])
	if cdpURL == "" {
		return withExitCode(2, fmt.Errorf("--cdp-url is required"))
	}
	intervalMinutes, err := parsePositiveOption("interval-minutes", parsed.values["interval-minutes"], 15)
	if err != nil {
		return withExitCode(2, err)
	}
	maxConversations, err := parsePositiveOption("max-conversations", parsed.values["max-conversations"], 50)
	if err != nil {
		return withExitCode(2, err)
	}
	binPath := strings.TrimSpace(parsed.values["aicrawl-bin"])
	if binPath == "" {
		if executable, err := os.Executable(); err == nil && executable != "" {
			binPath = executable
		} else {
			binPath = "aicrawl"
		}
	} else {
		binPath = expandPath(binPath)
	}
	label := strings.TrimSpace(parsed.values["label"])
	if label == "" {
		label = "com.openclaw.aicrawl.sync." + provider
	}
	outPath := strings.TrimSpace(parsed.values["out"])
	if outPath == "" {
		outPath = defaultLaunchAgentPath(label)
	} else {
		outPath = expandPath(outPath)
	}
	programArgs := []string{binPath}
	if globals.configPath != "" {
		programArgs = append(programArgs, "--config", expandPath(globals.configPath))
	}
	programArgs = append(programArgs,
		"sync", "web",
		"--provider", provider,
		"--cdp-url", cdpURL,
		"--max-conversations", fmt.Sprint(maxConversations),
		"--json",
	)
	result := launchdResult{
		Path:             outPath,
		Label:            label,
		Provider:         provider,
		IntervalSeconds:  intervalMinutes * 60,
		MaxConversations: maxConversations,
		ProgramArguments: append([]string(nil), programArgs...),
		NextSteps: []string{
			"Keep the browser running with remote debugging enabled at the configured CDP URL.",
			"Load the LaunchAgent with launchctl when you are ready.",
		},
	}
	if err := writeLaunchAgent(outPath, label, programArgs, result.IntervalSeconds); err != nil {
		return err
	}
	_ = ctx
	if globals.format == "json" || parsed.bools["json"] {
		return writeJSON(a.stdout, result)
	}
	if err := writeTextLine(a.stdout, "wrote LaunchAgent to %s", outPath); err != nil {
		return err
	}
	return writeTextLine(a.stdout, "load with: launchctl bootstrap gui/$(id -u) %s", outPath)
}

func defaultLaunchAgentPath(label string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "~"
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

func writeLaunchAgent(path, label string, programArgs []string, intervalSeconds int) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("launchd output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create LaunchAgent directory: %w", err)
	}
	plist := renderLaunchAgent(label, programArgs, intervalSeconds)
	if err := os.WriteFile(path, []byte(plist), 0o600); err != nil {
		return fmt.Errorf("write LaunchAgent: %w", err)
	}
	return nil
}

func renderLaunchAgent(label string, programArgs []string, intervalSeconds int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n")
	b.WriteString("<dict>\n")
	writePlistKeyString(&b, "Label", label)
	b.WriteString("  <key>ProgramArguments</key>\n")
	b.WriteString("  <array>\n")
	for _, arg := range programArgs {
		b.WriteString("    <string>")
		_ = xml.EscapeText(&b, []byte(arg))
		b.WriteString("</string>\n")
	}
	b.WriteString("  </array>\n")
	writePlistKeyInteger(&b, "StartInterval", intervalSeconds)
	writePlistKeyBool(&b, "RunAtLoad", true)
	b.WriteString("</dict>\n")
	b.WriteString("</plist>\n")
	return b.String()
}

func writePlistKeyString(b *strings.Builder, key, value string) {
	b.WriteString("  <key>")
	_ = xml.EscapeText(b, []byte(key))
	b.WriteString("</key>\n")
	b.WriteString("  <string>")
	_ = xml.EscapeText(b, []byte(value))
	b.WriteString("</string>\n")
}

func writePlistKeyInteger(b *strings.Builder, key string, value int) {
	b.WriteString("  <key>")
	_ = xml.EscapeText(b, []byte(key))
	b.WriteString("</key>\n")
	b.WriteString("  <integer>")
	b.WriteString(fmt.Sprint(value))
	b.WriteString("</integer>\n")
}

func writePlistKeyBool(b *strings.Builder, key string, value bool) {
	b.WriteString("  <key>")
	_ = xml.EscapeText(b, []byte(key))
	b.WriteString("</key>\n")
	if value {
		b.WriteString("  <true/>\n")
	} else {
		b.WriteString("  <false/>\n")
	}
}
