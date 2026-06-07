package browser

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const defaultLaunchWait = 20 * time.Second

type LaunchOptions struct {
	Provider            string
	ProfilePath         string
	BrowserPath         string
	RemoteDebuggingPort int
	Wait                time.Duration
}

type LaunchResult struct {
	BrowserPath         string      `json:"browser_path"`
	ProfilePath         string      `json:"profile_path"`
	Endpoint            string      `json:"endpoint"`
	Launched            bool        `json:"launched"`
	Reused              bool        `json:"reused,omitempty"`
	PID                 int         `json:"pid,omitempty"`
	RemoteDebuggingPort int         `json:"remote_debugging_port"`
	Process             *os.Process `json:"-"`
}

func (r LaunchResult) Stop() error {
	if r.Process == nil {
		return nil
	}
	return r.Process.Kill()
}

func LaunchProfile(ctx context.Context, opts LaunchOptions) (LaunchResult, error) {
	if strings.TrimSpace(opts.ProfilePath) == "" {
		return LaunchResult{}, fmt.Errorf("browser profile path is required")
	}
	if opts.RemoteDebuggingPort < 0 {
		return LaunchResult{}, fmt.Errorf("--remote-debugging-port must be a non-negative integer")
	}
	if _, err := Provider(opts.Provider); err != nil {
		return LaunchResult{}, err
	}
	profilePath := filepath.Clean(opts.ProfilePath)
	if endpoint, port, ok := existingEndpoint(ctx, profilePath, opts.RemoteDebuggingPort); ok {
		return LaunchResult{
			BrowserPath:         strings.TrimSpace(opts.BrowserPath),
			ProfilePath:         profilePath,
			Endpoint:            endpoint,
			Reused:              true,
			RemoteDebuggingPort: port,
		}, nil
	}
	spec, err := Provider(opts.Provider)
	if err != nil {
		return LaunchResult{}, err
	}
	browserPath, err := ResolveBrowserPath(opts.BrowserPath)
	if err != nil {
		return LaunchResult{}, err
	}
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		return LaunchResult{}, fmt.Errorf("create browser profile directory: %w", err)
	}
	_ = os.Chmod(profilePath, 0o700)
	portFile := filepath.Join(profilePath, "DevToolsActivePort")
	_ = os.Remove(portFile)
	portArg := strconv.Itoa(opts.RemoteDebuggingPort)
	args := []string{
		"--user-data-dir=" + profilePath,
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + portArg,
		"--no-first-run",
		"--no-default-browser-check",
		spec.HomeURL,
	}
	cmd := exec.CommandContext(ctx, browserPath, args...)
	if err := cmd.Start(); err != nil {
		return LaunchResult{}, fmt.Errorf("launch browser: %w", err)
	}
	go func() {
		_ = cmd.Wait()
	}()
	wait := opts.Wait
	if wait <= 0 {
		wait = defaultLaunchWait
	}
	endpoint, port, err := waitForLaunchEndpoint(ctx, profilePath, opts.RemoteDebuggingPort, wait)
	if err != nil {
		_ = cmd.Process.Kill()
		return LaunchResult{}, err
	}
	return LaunchResult{
		BrowserPath:         browserPath,
		ProfilePath:         profilePath,
		Endpoint:            endpoint,
		Launched:            true,
		PID:                 cmd.Process.Pid,
		RemoteDebuggingPort: port,
		Process:             cmd.Process,
	}, nil
}

func existingEndpoint(ctx context.Context, profilePath string, requestedPort int) (string, int, bool) {
	if requestedPort > 0 {
		endpoint := fmt.Sprintf("http://127.0.0.1:%d", requestedPort)
		if probeCDPEndpoint(ctx, endpoint) == nil {
			return endpoint, requestedPort, true
		}
		return "", 0, false
	}
	port, err := readDevToolsPort(filepath.Join(profilePath, "DevToolsActivePort"))
	if err != nil {
		return "", 0, false
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	if probeCDPEndpoint(ctx, endpoint) != nil {
		return "", 0, false
	}
	return endpoint, port, true
}

func ResolveBrowserPath(value string) (string, error) {
	candidates := browserCandidates(value)
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if strings.ContainsRune(candidate, os.PathSeparator) {
			info, err := os.Stat(candidate)
			if err == nil && !info.IsDir() {
				return candidate, nil
			}
			continue
		}
		resolved, err := exec.LookPath(candidate)
		if err == nil {
			return resolved, nil
		}
	}
	if strings.TrimSpace(value) != "" {
		return "", fmt.Errorf("browser executable not found: %s", value)
	}
	return "", fmt.Errorf("browser executable not found; pass --browser or set AICRAWL_BROWSER")
}

func browserCandidates(value string) []string {
	var candidates []string
	if strings.TrimSpace(value) != "" {
		candidates = append(candidates, strings.TrimSpace(value))
	}
	if env := strings.TrimSpace(os.Getenv("AICRAWL_BROWSER")); env != "" {
		candidates = append(candidates, env)
	}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		)
	}
	candidates = append(candidates,
		"google-chrome",
		"chrome",
		"chromium",
		"chromium-browser",
		"microsoft-edge",
		"msedge",
	)
	return candidates
}

func waitForLaunchEndpoint(ctx context.Context, profilePath string, requestedPort int, wait time.Duration) (string, int, error) {
	deadline := time.Now().Add(wait)
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", 0, ctx.Err()
		default:
		}
		port := requestedPort
		if port == 0 {
			discovered, err := readDevToolsPort(filepath.Join(profilePath, "DevToolsActivePort"))
			if err != nil {
				lastErr = err
				time.Sleep(150 * time.Millisecond)
				continue
			}
			port = discovered
		}
		endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
		if err := probeCDPEndpoint(ctx, endpoint); err != nil {
			lastErr = err
			time.Sleep(150 * time.Millisecond)
			continue
		}
		return endpoint, port, nil
	}
	if lastErr != nil {
		return "", 0, fmt.Errorf("browser did not expose a ready CDP endpoint: %w", lastErr)
	}
	return "", 0, fmt.Errorf("browser did not expose a ready CDP endpoint")
}

func readDevToolsPort(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return 0, fmt.Errorf("DevToolsActivePort is empty")
	}
	port, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || port <= 0 {
		return 0, fmt.Errorf("DevToolsActivePort has invalid port")
	}
	return port, nil
}

func probeCDPEndpoint(ctx context.Context, endpoint string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/json/version", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("CDP version endpoint returned HTTP status %d", resp.StatusCode)
	}
	return nil
}
