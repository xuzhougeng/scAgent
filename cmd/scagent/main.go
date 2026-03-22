package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"scagent/internal/app"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	runtimeURL := flag.String("runtime-url", "http://127.0.0.1:8081", "Python runtime base URL")
	skillsPath := flag.String("skills-path", "skills/registry.json", "Path to skill registry JSON")
	pluginDir := flag.String("plugin-dir", envOrDefault("SCAGENT_PLUGIN_DIR", "data/skill-hub/plugins"), "Directory used for skill hub plugin bundles")
	pluginStatePath := flag.String("plugin-state-path", envOrDefault("SCAGENT_PLUGIN_STATE_PATH", "data/skill-hub/state.json"), "Path to the persisted Skill Hub enable/disable state file")
	docsDir := flag.String("docs-dir", "docs", "Directory used for markdown help docs")
	dataDir := flag.String("data-dir", "data", "Directory used for session artifacts")
	webDir := flag.String("web-dir", "web", "Directory used for static web assets")
	plannerMode := flag.String("planner-mode", envOrDefault("SCAGENT_PLANNER_MODE", "fake"), "Planner mode: fake or llm")
	openAIBaseURL := flag.String("openai-base-url", envOrDefault("SCAGENT_OPENAI_BASE_URL", "https://api.openai.com/v1"), "Base URL for the OpenAI-compatible planner API")
	openAIModel := flag.String("openai-model", envOrDefault("SCAGENT_OPENAI_MODEL", "gpt-5.4"), "Model used by the LLM planner")
	openAIReasoning := flag.String("openai-reasoning", envOrDefault("SCAGENT_OPENAI_REASONING_EFFORT", "low"), "Reasoning effort for the LLM planner")
	openAIAPIKey := flag.String("openai-api-key", envOrDefault("SCAGENT_OPENAI_API_KEY", ""), "API key for the LLM planner")
	flag.Parse()

	server, err := app.NewServer(app.Config{
		SkillsPath:            *skillsPath,
		PluginDir:             *pluginDir,
		PluginStatePath:       *pluginStatePath,
		DocsDir:               *docsDir,
		RuntimeURL:            *runtimeURL,
		DataDir:               *dataDir,
		WebDir:                *webDir,
		PlannerMode:           *plannerMode,
		OpenAIAPIKey:          *openAIAPIKey,
		OpenAIBaseURL:         *openAIBaseURL,
		OpenAIModel:           *openAIModel,
		OpenAIReasoningEffort: *openAIReasoning,
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("scAgent listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, server))
}

func envOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
