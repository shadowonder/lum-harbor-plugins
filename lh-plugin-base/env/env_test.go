package env

import "testing"

func TestHostEnvironmentHelpers(t *testing.T) {
	t.Setenv(PluginIDVariable, "sample-plugin")
	t.Setenv(PluginVersionVariable, "1.2.3")
	t.Setenv(PluginInstallDirVariable, "/plugin")
	t.Setenv(PluginDataDirVariable, "/data")
	t.Setenv(PluginCacheDirVariable, "/cache")
	t.Setenv(OpenAIAPIKeyVariable, "secret")

	if PluginID() != "sample-plugin" || PluginVersion() != "1.2.3" || PluginInstallDir() != "/plugin" || PluginDataDir() != "/data" || PluginCacheDir() != "/cache" || OpenAIAPIKey() != "secret" {
		t.Fatal("environment helper returned an unexpected value")
	}
}
