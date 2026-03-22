package skill

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"scagent/internal/models"
)

type FieldSchema struct {
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Enum        []string `json:"enum,omitempty"`
	Description string   `json:"description"`
}

type Definition struct {
	Name         string                 `json:"name"`
	Label        string                 `json:"label"`
	Category     string                 `json:"category,omitempty"`
	SupportLevel string                 `json:"support_level,omitempty"`
	Description  string                 `json:"description"`
	TargetKinds  []models.ObjectKind    `json:"target_kinds"`
	Input        map[string]FieldSchema `json:"input"`
	Output       map[string]string      `json:"output"`
	Runtime      map[string]any         `json:"runtime,omitempty"`
	Source       string                 `json:"source,omitempty"`
	BundleID     string                 `json:"bundle_id,omitempty"`
}

const BuiltinBundleID = "builtin-core"

type registryFile struct {
	Skills []Definition `json:"skills"`
}

type PluginBundle struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Version     string       `json:"version,omitempty"`
	Description string       `json:"description,omitempty"`
	SourcePath  string       `json:"source_path,omitempty"`
	Builtin     bool         `json:"builtin,omitempty"`
	Enabled     bool         `json:"enabled"`
	Skills      []Definition `json:"skills"`
}

type pluginBundleFile struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Version     string       `json:"version,omitempty"`
	Description string       `json:"description,omitempty"`
	Skills      []Definition `json:"skills"`
}

type registryStateFile struct {
	DisabledBundles []string `json:"disabled_bundles,omitempty"`
}

type Registry struct {
	mu        sync.RWMutex
	basePath  string
	pluginDir string
	statePath string
	skills    map[string]Definition
	bundles   map[string]PluginBundle
}

func LoadRegistry(path string) (*Registry, error) {
	return LoadRegistryWithPluginsAndState(path, "", "")
}

func LoadRegistryWithPlugins(path, pluginDir string) (*Registry, error) {
	return LoadRegistryWithPluginsAndState(path, pluginDir, "")
}

func LoadRegistryWithPluginsAndState(path, pluginDir, statePath string) (*Registry, error) {
	registry := &Registry{
		basePath:  path,
		pluginDir: pluginDir,
		statePath: statePath,
		skills:    make(map[string]Definition),
		bundles:   make(map[string]PluginBundle),
	}
	if err := registry.Reload(); err != nil {
		return nil, err
	}
	return registry, nil
}

func (r *Registry) Reload() error {
	if r == nil {
		return nil
	}

	baseSkills, err := loadBaseSkills(r.basePath)
	if err != nil {
		return err
	}
	pluginSkills, bundles, err := loadPluginBundles(r.pluginDir)
	if err != nil {
		return err
	}
	for name := range pluginSkills {
		if _, exists := baseSkills[name]; exists {
			return fmt.Errorf("duplicate skill %q from plugin bundle", name)
		}
	}
	state, err := loadRegistryState(r.statePath)
	if err != nil {
		return err
	}

	mergedBundles := make(map[string]PluginBundle, len(bundles)+1)
	builtinBundle := PluginBundle{
		ID:          BuiltinBundleID,
		Name:        "内置技能",
		Description: "系统默认提供的规划与分析技能集合。",
		SourcePath:  r.basePath,
		Builtin:     true,
		Enabled:     !state.disabled(BuiltinBundleID),
		Skills:      make([]Definition, 0, len(baseSkills)),
	}
	for _, definition := range baseSkills {
		builtinBundle.Skills = append(builtinBundle.Skills, definition)
	}
	sort.Slice(builtinBundle.Skills, func(i, j int) bool {
		return builtinBundle.Skills[i].Name < builtinBundle.Skills[j].Name
	})
	mergedBundles[builtinBundle.ID] = builtinBundle
	for _, bundle := range bundles {
		bundle.Enabled = !state.disabled(bundle.ID)
		mergedBundles[bundle.ID] = bundle
	}

	merged := make(map[string]Definition, len(baseSkills)+len(pluginSkills))
	if builtinBundle.Enabled {
		for name, definition := range baseSkills {
			merged[name] = definition
		}
	}
	for name, definition := range pluginSkills {
		bundle := mergedBundles[definition.BundleID]
		if bundle.Enabled {
			merged[name] = definition
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills = merged
	r.bundles = mergedBundles
	return nil
}

func (r *Registry) List() []Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Definition, 0, len(r.skills))
	for _, definition := range r.skills {
		out = append(out, definition)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func (r *Registry) ListExecutable() []Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Definition, 0, len(r.skills))
	for _, definition := range r.skills {
		if definition.SupportLevel == "" || definition.SupportLevel == "wired" {
			out = append(out, definition)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func (r *Registry) Get(name string) (Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	definition, ok := r.skills[name]
	return definition, ok
}

func (r *Registry) Bundles() []PluginBundle {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]PluginBundle, 0, len(r.bundles))
	for _, bundle := range r.bundles {
		out = append(out, bundle)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Builtin != out[j].Builtin {
			return out[i].Builtin
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (r *Registry) SetBundleEnabled(bundleID string, enabled bool) (PluginBundle, error) {
	if r == nil {
		return PluginBundle{}, fmt.Errorf("registry is not configured")
	}
	if strings.TrimSpace(r.statePath) == "" {
		return PluginBundle{}, fmt.Errorf("registry state path is not configured")
	}
	if err := r.Reload(); err != nil {
		return PluginBundle{}, err
	}

	bundles := r.Bundles()
	exists := false
	for _, bundle := range bundles {
		if bundle.ID == bundleID {
			exists = true
			break
		}
	}
	if !exists {
		return PluginBundle{}, fmt.Errorf("unknown plugin bundle %q", bundleID)
	}

	state, err := loadRegistryState(r.statePath)
	if err != nil {
		return PluginBundle{}, err
	}
	if enabled {
		delete(state.DisabledBundles, bundleID)
	} else {
		state.DisabledBundles[bundleID] = struct{}{}
	}
	if err := saveRegistryState(r.statePath, state); err != nil {
		return PluginBundle{}, err
	}
	if err := r.Reload(); err != nil {
		return PluginBundle{}, err
	}

	for _, bundle := range r.Bundles() {
		if bundle.ID == bundleID {
			return bundle, nil
		}
	}
	return PluginBundle{}, fmt.Errorf("failed to refresh plugin bundle %q", bundleID)
}

func (r *Registry) ValidatePlan(plan models.Plan) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(plan.Steps) == 0 {
		return fmt.Errorf("plan has no steps")
	}

	for idx, step := range plan.Steps {
		definition, ok := r.skills[step.Skill]
		if !ok {
			return fmt.Errorf("step %d uses unknown skill %q", idx+1, step.Skill)
		}
		if definition.SupportLevel != "" && definition.SupportLevel != "wired" {
			return fmt.Errorf("step %d uses non-executable skill %q (support_level=%s)", idx+1, step.Skill, definition.SupportLevel)
		}
		for fieldName, field := range definition.Input {
			if !field.Required {
				continue
			}
			if step.Params == nil {
				return fmt.Errorf("step %d missing params for required field %q", idx+1, fieldName)
			}
			value, ok := step.Params[fieldName]
			if !ok {
				return fmt.Errorf("step %d missing required field %q", idx+1, fieldName)
			}
			if value == nil {
				return fmt.Errorf("step %d has nil required field %q", idx+1, fieldName)
			}
		}
	}

	return nil
}

func LoadPluginBundleFile(path string) (*PluginBundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plugin bundle: %w", err)
	}
	return decodePluginBundleFile(path, data)
}

func loadBaseSkills(path string) (map[string]Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read skills registry: %w", err)
	}

	var file registryFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decode skills registry: %w", err)
	}

	skills := make(map[string]Definition, len(file.Skills))
	for _, definition := range file.Skills {
		definition.Source = "builtin"
		definition.BundleID = BuiltinBundleID
		if strings.TrimSpace(definition.Name) == "" {
			return nil, fmt.Errorf("encountered builtin skill without name")
		}
		skills[definition.Name] = definition
	}
	return skills, nil
}

func loadPluginBundles(pluginDir string) (map[string]Definition, map[string]PluginBundle, error) {
	skills := make(map[string]Definition)
	bundles := make(map[string]PluginBundle)
	if strings.TrimSpace(pluginDir) == "" {
		return skills, bundles, nil
	}

	info, err := os.Stat(pluginDir)
	if err != nil {
		if os.IsNotExist(err) {
			return skills, bundles, nil
		}
		return nil, nil, fmt.Errorf("stat plugin directory: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("plugin directory %q is not a directory", pluginDir)
	}

	manifestPaths := make([]string, 0, 8)
	err = filepath.Walk(pluginDir, func(path string, fileInfo os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fileInfo.IsDir() {
			return nil
		}
		if strings.EqualFold(fileInfo.Name(), "plugin.json") {
			manifestPaths = append(manifestPaths, path)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("walk plugin directory: %w", err)
	}
	sort.Strings(manifestPaths)

	for _, manifestPath := range manifestPaths {
		bundle, err := LoadPluginBundleFile(manifestPath)
		if err != nil {
			return nil, nil, err
		}
		if _, exists := bundles[bundle.ID]; exists {
			return nil, nil, fmt.Errorf("duplicate plugin bundle id %q", bundle.ID)
		}
		bundles[bundle.ID] = *bundle
		for _, definition := range bundle.Skills {
			if _, exists := skills[definition.Name]; exists {
				return nil, nil, fmt.Errorf("duplicate plugin skill %q", definition.Name)
			}
			skills[definition.Name] = definition
		}
	}

	return skills, bundles, nil
}

func decodePluginBundleFile(path string, data []byte) (*PluginBundle, error) {
	var file pluginBundleFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decode plugin bundle %q: %w", path, err)
	}

	bundleID := strings.TrimSpace(file.ID)
	if bundleID == "" {
		bundleID = filepath.Base(filepath.Dir(path))
	}
	if bundleID == "" || bundleID == "." {
		return nil, fmt.Errorf("plugin bundle %q is missing id", path)
	}

	bundle := &PluginBundle{
		ID:          bundleID,
		Name:        strings.TrimSpace(file.Name),
		Version:     strings.TrimSpace(file.Version),
		Description: strings.TrimSpace(file.Description),
		SourcePath:  path,
		Skills:      make([]Definition, 0, len(file.Skills)),
	}
	if bundle.Name == "" {
		bundle.Name = bundle.ID
	}

	for _, definition := range file.Skills {
		if strings.TrimSpace(definition.Name) == "" {
			return nil, fmt.Errorf("plugin bundle %q contains a skill without name", path)
		}
		runtimeConfig := definition.Runtime
		if len(runtimeConfig) == 0 {
			return nil, fmt.Errorf("plugin skill %q in %q is missing runtime config", definition.Name, path)
		}
		entrypoint, ok := runtimeConfig["entrypoint"]
		if !ok || strings.TrimSpace(fmt.Sprint(entrypoint)) == "" {
			return nil, fmt.Errorf("plugin skill %q in %q is missing runtime.entrypoint", definition.Name, path)
		}
		definition.Source = "plugin"
		definition.BundleID = bundle.ID
		bundle.Skills = append(bundle.Skills, definition)
	}
	sort.Slice(bundle.Skills, func(i, j int) bool {
		return bundle.Skills[i].Name < bundle.Skills[j].Name
	})
	return bundle, nil
}

type registryState struct {
	DisabledBundles map[string]struct{}
}

func (s registryState) disabled(bundleID string) bool {
	_, ok := s.DisabledBundles[bundleID]
	return ok
}

func loadRegistryState(path string) (registryState, error) {
	state := registryState{DisabledBundles: make(map[string]struct{})}
	if strings.TrimSpace(path) == "" {
		return state, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, fmt.Errorf("read registry state: %w", err)
	}

	var file registryStateFile
	if err := json.Unmarshal(data, &file); err != nil {
		return state, fmt.Errorf("decode registry state: %w", err)
	}
	for _, bundleID := range file.DisabledBundles {
		bundleID = strings.TrimSpace(bundleID)
		if bundleID == "" {
			continue
		}
		state.DisabledBundles[bundleID] = struct{}{}
	}
	return state, nil
}

func saveRegistryState(path string, state registryState) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create registry state directory: %w", err)
	}

	disabledBundles := make([]string, 0, len(state.DisabledBundles))
	for bundleID := range state.DisabledBundles {
		disabledBundles = append(disabledBundles, bundleID)
	}
	sort.Strings(disabledBundles)

	payload, err := json.MarshalIndent(registryStateFile{DisabledBundles: disabledBundles}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode registry state: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return fmt.Errorf("write registry state: %w", err)
	}
	return nil
}
