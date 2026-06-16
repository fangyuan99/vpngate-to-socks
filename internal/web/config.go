package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	defaultWebConfigPath     = "data/web-config.json"
	defaultAutoRefreshTime   = "03:00"
	webConfigPathEnvVar      = "WEB_CONFIG_PATH"
	autoRefreshTimePattern   = `^([01]\d|2[0-3]):[0-5]\d$`
	webConfigFilePermissions = 0o644
)

var autoRefreshTimeRE = regexp.MustCompile(autoRefreshTimePattern)

type webConfig struct {
	AutoRefresh autoRefreshConfig `json:"autoRefresh"`
}

type autoRefreshConfig struct {
	Enabled bool   `json:"enabled"`
	Time    string `json:"time"`
}

func defaultWebConfig() webConfig {
	return webConfig{
		AutoRefresh: autoRefreshConfig{
			Enabled: false,
			Time:    defaultAutoRefreshTime,
		},
	}
}

func webConfigPathFromEnv() string {
	if value := strings.TrimSpace(os.Getenv(webConfigPathEnvVar)); value != "" {
		return value
	}

	return defaultWebConfigPath
}

func loadWebConfig(path string) (webConfig, error) {
	config := defaultWebConfig()
	if strings.TrimSpace(path) == "" {
		return config, nil
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}
		return config, fmt.Errorf("读取 Web 配置失败: %w", err)
	}
	defer file.Close()

	if err := json.NewDecoder(file).Decode(&config); err != nil {
		return config, fmt.Errorf("解析 Web 配置失败: %w", err)
	}
	config = normalizeWebConfig(config)
	if err := validateWebConfig(config); err != nil {
		return config, err
	}

	return config, nil
}

func saveWebConfig(path string, config webConfig) error {
	config = normalizeWebConfig(config)
	if err := validateWebConfig(config); err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("Web 配置文件路径不能为空")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建 Web 配置目录失败: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".web-config-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时 Web 配置文件失败: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	encoder := json.NewEncoder(tmpFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(config); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("写入 Web 配置失败: %w", err)
	}
	if err := tmpFile.Chmod(webConfigFilePermissions); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("设置 Web 配置文件权限失败: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("关闭 Web 配置文件失败: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("保存 Web 配置失败: %w", err)
	}

	return nil
}

func validateWebConfig(config webConfig) error {
	if err := validateAutoRefreshTime(config.AutoRefresh.Time); err != nil {
		return err
	}

	return nil
}

func normalizeWebConfig(config webConfig) webConfig {
	if strings.TrimSpace(config.AutoRefresh.Time) == "" {
		config.AutoRefresh.Time = defaultAutoRefreshTime
	}

	return config
}

func validateAutoRefreshTime(value string) error {
	value = strings.TrimSpace(value)
	if !autoRefreshTimeRE.MatchString(value) {
		return fmt.Errorf("自动刷新时间必须使用 HH:MM 格式，范围为 00:00 到 23:59")
	}

	return nil
}
