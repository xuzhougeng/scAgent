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
	statePath := filepath.Join(root, "state.json")

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

	registry, err := LoadRegistryWithPluginsAndState(registryPath, filepath.Join(root, "plugins"), statePath)
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
	if len(bundles) != 2 {
		t.Fatalf("unexpected bundles: %+v", bundles)
	}
	if bundles[0].ID != BuiltinBundleID || !bundles[0].Builtin || !bundles[0].Enabled {
		t.Fatalf("expected builtin bundle first, got %+v", bundles[0])
	}
	if bundles[1].ID != "demo-plugin" || !bundles[1].Enabled {
		t.Fatalf("unexpected plugin bundle: %+v", bundles[1])
	}
}

func TestRegistryReloadPicksUpNewPluginBundle(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(root, "registry.json")
	pluginRoot := filepath.Join(root, "plugins")
	statePath := filepath.Join(root, "state.json")

	if err := os.WriteFile(registryPath, []byte(`{"skills":[]}`), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	registry, err := LoadRegistryWithPluginsAndState(registryPath, pluginRoot, statePath)
	if err != nil {
		t.Fatalf("load registry with plugins: %v", err)
	}
	if len(registry.Bundles()) != 1 || registry.Bundles()[0].ID != BuiltinBundleID {
		t.Fatalf("expected builtin bundle initially, got %+v", registry.Bundles())
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

func TestRegistryCanDisableBuiltinAndPluginBundles(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(root, "registry.json")
	pluginDir := filepath.Join(root, "plugins", "demo")
	statePath := filepath.Join(root, "state.json")

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
		"skills": [
			{
				"name": "demo_skill",
				"label": "Demo Skill",
				"description": "Demo",
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

	registry, err := LoadRegistryWithPluginsAndState(registryPath, filepath.Join(root, "plugins"), statePath)
	if err != nil {
		t.Fatalf("load registry with state: %v", err)
	}

	if _, err := registry.SetBundleEnabled(BuiltinBundleID, false); err != nil {
		t.Fatalf("disable builtin bundle: %v", err)
	}
	if _, ok := registry.Get("inspect_dataset"); ok {
		t.Fatalf("expected builtin skill to be disabled")
	}

	bundle, err := registry.SetBundleEnabled("demo-plugin", false)
	if err != nil {
		t.Fatalf("disable plugin bundle: %v", err)
	}
	if bundle.Enabled {
		t.Fatalf("expected plugin bundle to be disabled")
	}
	if _, ok := registry.Get("demo_skill"); ok {
		t.Fatalf("expected plugin skill to be disabled")
	}

	if _, err := registry.SetBundleEnabled("demo-plugin", true); err != nil {
		t.Fatalf("re-enable plugin bundle: %v", err)
	}
	if _, ok := registry.Get("demo_skill"); !ok {
		t.Fatalf("expected plugin skill to be re-enabled")
	}
}
