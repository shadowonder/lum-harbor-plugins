// Package env exposes the environment values supplied by the Lum Harbor host.
package env

import "os"

const (
	PluginIDVariable         = "LUM_HARBOR_PLUGIN_ID"
	PluginVersionVariable    = "LUM_HARBOR_PLUGIN_VERSION"
	PluginInstallDirVariable = "LUM_HARBOR_PLUGIN_INSTALL_DIR"
	PluginDataDirVariable    = "LUM_HARBOR_PLUGIN_DATA_DIR"
	PluginCacheDirVariable   = "LUM_HARBOR_PLUGIN_CACHE_DIR"
	OpenAIAPIKeyVariable     = "LUM_HARBOR_OPENAI_API_KEY"
)

func PluginID() string         { return os.Getenv(PluginIDVariable) }
func PluginVersion() string    { return os.Getenv(PluginVersionVariable) }
func PluginInstallDir() string { return os.Getenv(PluginInstallDirVariable) }
func PluginDataDir() string    { return os.Getenv(PluginDataDirVariable) }
func PluginCacheDir() string   { return os.Getenv(PluginCacheDirVariable) }
func OpenAIAPIKey() string     { return os.Getenv(OpenAIAPIKeyVariable) }
