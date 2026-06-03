package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openclaw/crawlkit/config"
)

const (
	appID       = "aicrawl"
	appName     = "aicrawl"
	displayName = "AI Crawl"
	configEnv   = "AICRAWL_CONFIG"
)

type runtime struct {
	ConfigPath string `json:"config_path"`
	DBPath     string `json:"db_path" toml:"db_path"`
	CacheDir   string `json:"cache_dir" toml:"cache_dir"`
	LogDir     string `json:"log_dir" toml:"log_dir"`
	ShareDir   string `json:"share_dir" toml:"share_dir"`
	Version    int    `json:"version" toml:"version"`
}

func crawlkitApp() config.App {
	return config.App{
		Name:          appName,
		ConfigEnv:     configEnv,
		LegacyBaseDir: "~/.config/aicrawl",
		PlatformDirs:  true,
	}
}

func defaultRuntimeConfig(app config.App) (config.RuntimeConfig, error) {
	defaults, err := app.DefaultRuntimeConfig()
	if err != nil {
		return config.RuntimeConfig{}, err
	}
	// Keep temp-home smoke runs isolated even when the parent shell has XDG_STATE_HOME
	// pointing at the real user home. Logs are private runtime data, not source data.
	defaults.LogDir = filepath.Join(defaults.CacheDir, "logs")
	return defaults, nil
}

func resolveRuntime(configFlag string, create bool) (runtime, error) {
	app := crawlkitApp()
	defaults, err := defaultRuntimeConfig(app)
	if err != nil {
		return runtime{}, err
	}
	configPath, err := app.ResolveConfigPath(configFlag)
	if err != nil {
		return runtime{}, err
	}
	cfg := config.RuntimeConfig{}
	if err := config.LoadTOML(configPath, &cfg); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return runtime{}, err
		}
		cfg = defaults
	} else {
		config.ApplyRuntimeDefaults(&cfg, defaults)
	}
	rt := runtime{
		ConfigPath: configPath,
		DBPath:     cfg.DBPath,
		CacheDir:   cfg.CacheDir,
		LogDir:     cfg.LogDir,
		ShareDir:   cfg.ShareDir,
		Version:    cfg.Version,
	}
	if create {
		if err := ensurePrivateRuntime(rt); err != nil {
			return runtime{}, err
		}
	}
	return rt, nil
}

func writeDefaultConfig(configFlag string) (runtime, bool, error) {
	app := crawlkitApp()
	defaults, err := defaultRuntimeConfig(app)
	if err != nil {
		return runtime{}, false, err
	}
	configPath, err := app.ResolveConfigPath(configFlag)
	if err != nil {
		return runtime{}, false, err
	}
	existed := true
	cfg := config.RuntimeConfig{}
	if err := config.LoadTOML(configPath, &cfg); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return runtime{}, false, err
		}
		existed = false
		cfg = defaults
	}
	config.ApplyRuntimeDefaults(&cfg, defaults)
	rt := runtime{
		ConfigPath: configPath,
		DBPath:     cfg.DBPath,
		CacheDir:   cfg.CacheDir,
		LogDir:     cfg.LogDir,
		ShareDir:   cfg.ShareDir,
		Version:    cfg.Version,
	}
	if err := ensurePrivateRuntime(rt); err != nil {
		return runtime{}, false, err
	}
	if !existed {
		if err := config.WriteTOML(configPath, cfg, 0o600); err != nil {
			return runtime{}, false, err
		}
	}
	return rt, !existed, nil
}

func ensurePrivateRuntime(rt runtime) error {
	for _, path := range []string{
		filepath.Dir(rt.ConfigPath),
		filepath.Dir(rt.DBPath),
		rt.CacheDir,
		rt.LogDir,
		rt.ShareDir,
	} {
		if strings.TrimSpace(path) == "" || path == "." {
			continue
		}
		if err := ensurePrivateDir(path); err != nil {
			return err
		}
	}
	return nil
}

func ensurePrivateDir(path string) error {
	if strings.TrimSpace(path) == "" || path == "." {
		return nil
	}
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("runtime path %s exists and is not a directory", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat runtime dir %s: %w", path, err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create runtime dir %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("chmod runtime dir %s: %w", path, err)
	}
	return nil
}

func expandPath(path string) string {
	return config.ExpandHome(path)
}
