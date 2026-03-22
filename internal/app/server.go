package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"scagent/internal/api"
	"scagent/internal/orchestrator"
	"scagent/internal/runtime"
	"scagent/internal/session"
	"scagent/internal/skill"
)

type Config struct {
	SkillsPath            string
	PluginDir             string
	PluginStatePath       string
	DocsDir               string
	RuntimeURL            string
	DataDir               string
	WebDir                string
	PlannerMode           string
	OpenAIAPIKey          string
	OpenAIBaseURL         string
	OpenAIModel           string
	OpenAIReasoningEffort string
}

func NewServer(config Config) (http.Handler, error) {
	if err := os.MkdirAll(config.DataDir, 0o755); err != nil {
		return nil, err
	}

	loadRegistry := skill.LoadRegistry
	if config.PluginDir != "" {
		loadRegistry = func(path string) (*skill.Registry, error) {
			return skill.LoadRegistryWithPluginsAndState(path, config.PluginDir, config.PluginStatePath)
		}
	}
	registry, err := loadRegistry(config.SkillsPath)
	if err != nil {
		return nil, err
	}

	store := session.NewStore()
	runtimeClient := runtime.NewClient(config.RuntimeURL)
	planner, err := orchestrator.NewPlanner(orchestrator.PlannerConfig{
		Mode:            config.PlannerMode,
		OpenAIAPIKey:    config.OpenAIAPIKey,
		OpenAIBaseURL:   config.OpenAIBaseURL,
		OpenAIModel:     config.OpenAIModel,
		ReasoningEffort: config.OpenAIReasoningEffort,
		Skills:          registry,
	})
	if err != nil {
		return nil, err
	}

	evaluator, err := orchestrator.NewEvaluator(orchestrator.EvaluatorConfig{
		Mode:            config.PlannerMode,
		OpenAIAPIKey:    config.OpenAIAPIKey,
		OpenAIBaseURL:   config.OpenAIBaseURL,
		OpenAIModel:     config.OpenAIModel,
		ReasoningEffort: config.OpenAIReasoningEffort,
	})
	if err != nil {
		return nil, err
	}

	service := orchestrator.NewServiceWithEvaluator(store, registry, runtimeClient, planner, evaluator, config.DataDir)
	handler := api.NewHandler(service, config.DocsDir)

	mux := http.NewServeMux()
	handler.Register(mux)
	mux.Handle("/data/", http.StripPrefix("/data/", http.FileServer(http.Dir(config.DataDir))))
	mux.Handle("/", cacheControl(http.FileServer(http.Dir(config.WebDir))))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = runtimeClient.Health(ctx)

	return mux, nil
}

func cacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if filepath.Ext(r.URL.Path) == ".js" || filepath.Ext(r.URL.Path) == ".css" {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
