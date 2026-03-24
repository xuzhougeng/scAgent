package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	WeixinEnabled         bool
}

type Server struct {
	Handler http.Handler
	Service *orchestrator.Service
	Config  Config
}

func NewServer(config Config) (*Server, error) {
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

	store, err := session.NewPersistentStore(filepath.Join(config.DataDir, "state", "store.db"))
	if err != nil {
		return nil, err
	}
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

	answerer, err := orchestrator.NewAnswerer(orchestrator.AnswererConfig{
		Mode:            config.PlannerMode,
		OpenAIAPIKey:    config.OpenAIAPIKey,
		OpenAIBaseURL:   config.OpenAIBaseURL,
		OpenAIModel:     config.OpenAIModel,
		ReasoningEffort: config.OpenAIReasoningEffort,
	})
	if err != nil {
		return nil, err
	}

	service := orchestrator.NewServiceWithComponents(store, registry, runtimeClient, planner, evaluator, answerer, config.DataDir)
	handler := api.NewHandler(service, config.DocsDir)

	mux := http.NewServeMux()
	handler.Register(mux)
	mux.Handle("/data/", http.StripPrefix("/data/", http.FileServer(http.Dir(config.DataDir))))
	localesDir := filepath.Join(config.WebDir, "locales")
	mux.Handle("/locales/", http.StripPrefix("/locales/", http.FileServer(http.Dir(localesDir))))
	mux.Handle("/", cacheControl(http.FileServer(http.Dir(config.WebDir))))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = runtimeClient.Health(ctx)

	return &Server{
		Handler: withLocale(mux),
		Service: service,
		Config:  config,
	}, nil
}

func withLocale(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		locale := extractLocale(r)
		ctx := runtime.ContextWithLocale(r.Context(), locale)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func extractLocale(r *http.Request) string {
	accept := r.Header.Get("Accept-Language")
	if accept == "" {
		return "zh"
	}
	lang := strings.SplitN(accept, ",", 2)[0]
	lang = strings.TrimSpace(lang)
	if idx := strings.IndexByte(lang, ';'); idx >= 0 {
		lang = lang[:idx]
	}
	lang = strings.TrimSpace(lang)
	switch lang {
	case "zh", "en":
		return lang
	default:
		return "zh"
	}
}

func cacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if filepath.Ext(r.URL.Path) == ".js" || filepath.Ext(r.URL.Path) == ".css" {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
