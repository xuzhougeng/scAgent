package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"scagent/internal/app"
	"scagent/internal/weixin"
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
	weixinEnabled := flag.Bool("weixin", envOrDefault("WEIXIN_BRIDGE_ENABLED", "0") == "1", "Enable WeChat bridge")
	weixinLogin := flag.Bool("weixin-login", false, "Run WeChat QR login then exit")
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
		WeixinEnabled:         *weixinEnabled,
	})
	if err != nil {
		log.Fatal(err)
	}

	bridge := weixin.NewBridge(
		weixin.NewClient("", ""),
		server.Service,
		weixin.BridgeConfig{
			DataDir:      *dataDir,
			SessionLabel: envOrDefault("WEIXIN_BRIDGE_SESSION_LABEL", "WeChat"),
			JobTimeout:   parseDuration(envOrDefault("WEIXIN_BRIDGE_TIMEOUT_MS", "300000")),
		},
	)

	// Login-only mode
	if *weixinLogin {
		if err := bridge.Login(); err != nil {
			log.Fatal(err)
		}
		return
	}

	// Start WeChat bridge in background
	if *weixinEnabled {
		if !bridge.LoadAccount() {
			log.Println("[weixin] no saved account, running login...")
			if err := bridge.Login(); err != nil {
				log.Fatalf("[weixin] login failed: %v", err)
			}
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Graceful shutdown on signal
		go func() {
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt)
			<-sigCh
			cancel()
		}()

		go func() {
			if err := bridge.Run(ctx); err != nil {
				log.Printf("[weixin] bridge stopped: %v", err)
			}
		}()
	}

	log.Printf("scAgent listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, server.Handler))
}

func envOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func parseDuration(ms string) time.Duration {
	var v int
	for _, c := range ms {
		if c >= '0' && c <= '9' {
			v = v*10 + int(c-'0')
		}
	}
	if v == 0 {
		return 5 * time.Minute
	}
	return time.Duration(v) * time.Millisecond
}
