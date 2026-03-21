package skill

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

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
}

type registryFile struct {
	Skills []Definition `json:"skills"`
}

type Registry struct {
	skills map[string]Definition
}

func LoadRegistry(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read skills registry: %w", err)
	}

	var file registryFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decode skills registry: %w", err)
	}

	registry := &Registry{
		skills: make(map[string]Definition, len(file.Skills)),
	}
	for _, definition := range file.Skills {
		registry.skills[definition.Name] = definition
	}

	return registry, nil
}

func (r *Registry) List() []Definition {
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
	definition, ok := r.skills[name]
	return definition, ok
}

func (r *Registry) ValidatePlan(plan models.Plan) error {
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
