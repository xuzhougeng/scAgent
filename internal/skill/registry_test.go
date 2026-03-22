package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryLoadsPluginBundles(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(root, "registry.json")
	pluginDir := filepath.Join(root, "plugins", "demo")

	if err := os.WriteFile(registryPath, []byte(`{
		"skills": [
			{
				"name": "inspect_dataset",
				"label": "Inspect dataset",
				"description": "Inspect",
				"target_kinds": ["raw_dataset"],
				"input": {},
				"output": {"summary": "string"}
			}
		]
	}`), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("create plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`{
		"id": "demo-plugin",
		"name": "Demo Plugin",
		"skills": [
			{
				"name": "demo_plugin_skill",
				"label": "Demo Plugin Skill",
				"category": "custom",
				"support_level": "wired",
				"description": "Provided by plugin",
				"target_kinds": ["raw_dataset"],
				"input": {},
				"output": {"summary": "string"},
				"runtime": {
					"kind": "python",
					"entrypoint": "plugin.py",
					"callable": "run"
				}
			}
		]
	}`), 0o644); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.py"), []byte("def run(context):\n    return {'summary': 'ok'}\n"), 0o644); err != nil {
		t.Fatalf("write plugin script: %v", err)
	}

	registry, err := LoadRegistryWithPlugins(registryPath, filepath.Join(root, "plugins"))
	if err != nil {
		t.Fatalf("load registry with plugins: %v", err)
	}

	definition, ok := registry.Get("demo_plugin_skill")
	if !ok {
		t.Fatalf("expected plugin skill to be registered")
	}
	if definition.Source != "plugin" {
		t.Fatalf("expected plugin source, got %q", definition.Source)
	}
	if definition.BundleID != "demo-plugin" {
		t.Fatalf("unexpected bundle id: %q", definition.BundleID)
	}

	bundles := registry.Bundles()
	if len(bundles) != 1 || bundles[0].ID != "demo-plugin" {
		t.Fatalf("unexpected bundles: %+v", bundles)
	}
}

func TestRegistryReloadPicksUpNewPluginBundle(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(root, "registry.json")
	pluginRoot := filepath.Join(root, "plugins")

	if err := os.WriteFile(registryPath, []byte(`{"skills":[]}`), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	registry, err := LoadRegistryWithPlugins(registryPath, pluginRoot)
	if err != nil {
		t.Fatalf("load registry with plugins: %v", err)
	}
	if len(registry.Bundles()) != 0 {
		t.Fatalf("expected no bundles initially")
	}

	pluginDir := filepath.Join(pluginRoot, "new-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("create plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`{
		"id": "new-plugin",
		"skills": [
			{
				"name": "new_skill",
				"label": "New Skill",
				"description": "Dynamic plugin skill",
				"target_kinds": ["raw_dataset"],
				"input": {},
				"output": {"summary": "string"},
				"runtime": {"entrypoint": "plugin.py"}
			}
		]
	}`), 0o644); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.py"), []byte("def run(context):\n    return {'summary': 'ok'}\n"), 0o644); err != nil {
		t.Fatalf("write plugin script: %v", err)
	}

	if err := registry.Reload(); err != nil {
		t.Fatalf("reload registry: %v", err)
	}

	if _, ok := registry.Get("new_skill"); !ok {
		t.Fatalf("expected reload to pick up new plugin skill")
	}
}
