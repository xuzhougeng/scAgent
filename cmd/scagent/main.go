package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"scagent/internal/app"
	"scagent/internal/weixin"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "reset" {
		if err := runResetCommand(os.Args[2:], os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}

	addr := flag.String("addr", ":8080", "HTTP listen address")
	runtimeURL := flag.String("runtime-url", "http://127.0.0.1:8081", "Python runtime base URL")
	skillsPath := flag.String("skills-path", "skills/registry.json", "Path to skill registry JSON")
	pluginDir := flag.String("plugin-dir", envOrDefault("SCAGENT_PLUGIN_DIR", "data/skill-hub/plugins"), "Directory used for skill hub plugin bundles")
	pluginStatePath := flag.String("plugin-state-path", envOrDefault("SCAGENT_PLUGIN_STATE_PATH", "data/skill-hub/state.json"), "Path to the persisted Skill Hub enable/disable state file")
	docsDir := flag.String("docs-dir", "docs", "Directory used for markdown help docs")
	dataDir := flag.String("data-dir", "data", "Directory used for session artifacts")
	webDir := flag.String("web-dir", "web", "Directory used for static web assets")
	plannerMode := flag.String("planner-mode", envOrDefault("SCAGENT_PLANNER_MODE", "llm"), "Planner mode: llm or fake (debug only)")
	openAIBaseURL := flag.String("openai-base-url", envOrDefault("SCAGENT_OPENAI_BASE_URL", "https://api.openai.com/v1"), "Base URL for the OpenAI-compatible planner API")
	openAIModel := flag.String("openai-model", envOrDefault("SCAGENT_OPENAI_MODEL", "gpt-5.4"), "Model used by the LLM planner")
	openAIReasoning := flag.String("openai-reasoning", envOrDefault("SCAGENT_OPENAI_REASONING_EFFORT", "low"), "Reasoning effort for the LLM planner")
	openAIAPIKey := flag.String("openai-api-key", envOrDefault("SCAGENT_OPENAI_API_KEY", ""), "API key for the LLM planner")
	weixinEnabled := flag.Bool("weixin", envOrDefault("WEIXIN_BRIDGE_ENABLED", "0") == "1", "Enable WeChat bridge")
	weixinLogin := flag.Bool("weixin-login", false, "Run WeChat QR login then exit")
	weixinLogout := flag.Bool("weixin-logout", false, "Remove saved WeChat credentials and exit")
	flag.Parse()

	// Handle login/logout modes early — they don't need the full server.
	if *weixinLogin || *weixinLogout {
		bridge := weixin.NewBridge(
			weixin.NewClient("", ""),
			nil,
			weixin.BridgeConfig{
				DataDir:      *dataDir,
				SessionLabel: envOrDefault("WEIXIN_BRIDGE_SESSION_LABEL", "WeChat"),
				JobTimeout:   parseDuration(envOrDefault("WEIXIN_BRIDGE_TIMEOUT_MS", "300000")),
			},
		)
		if *weixinLogin {
			if err := bridge.Login(); err != nil {
				log.Fatal(err)
			}
		} else {
			if err := bridge.Logout(); err != nil {
				log.Fatal(err)
			}
		}
		return
	}

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

	shutdownCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	// Start WeChat bridge in background
	if *weixinEnabled {
		if !bridge.LoadAccount() {
			log.Println("[weixin] no saved account, running login...")
			if err := bridge.Login(); err != nil {
				log.Fatalf("[weixin] login failed: %v", err)
			}
		}
		go func() {
			if err := bridge.Run(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("[weixin] bridge stopped: %v", err)
			}
		}()
	}

	httpServer := &http.Server{
		Addr:    *addr,
		Handler: server.Handler,
		BaseContext: func(_ net.Listener) context.Context {
			return shutdownCtx
		},
	}
	serverErrCh := make(chan error, 1)
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
			return
		}
		serverErrCh <- nil
	}()

	log.Printf("scAgent listening on %s", listener.Addr())
	log.Printf(
		"startup complete: web=%s runtime=%s weixin=%s",
		displayWebURL(listener.Addr().String()),
		*runtimeURL,
		weixinStatus(*weixinEnabled),
	)

	select {
	case err := <-serverErrCh:
		if err != nil {
			log.Fatal(err)
		}
		return
	case <-shutdownCtx.Done():
	}

	// Restore default signal behavior after the first interrupt so a second
	// Ctrl+C can still terminate the process immediately.
	stopSignals()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("http server shutdown failed: %v", err)
	}

	if err := <-serverErrCh; err != nil {
		log.Fatal(err)
	}
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

func displayWebURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			return "http://127.0.0.1" + addr
		}
		return "http://" + addr
	}

	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}

	return "http://" + net.JoinHostPort(host, port)
}

func weixinStatus(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func runResetCommand(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("reset", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	dataDir := flags.String("data-dir", envOrDefault("SCAGENT_DATA_DIR", "data"), "Directory used for session artifacts")
	resetAll := flags.Bool("all", false, "Remove persisted scAgent state and workspace data")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*resetAll {
		return fmt.Errorf("reset currently requires --all")
	}
	return resetAllData(*dataDir, stdout)
}

func resetAllData(dataDir string, stdout io.Writer) error {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return fmt.Errorf("data dir is required")
	}

	statePath := filepath.Join(dataDir, "state", "store.db")
	workspacesPath := filepath.Join(dataDir, "workspaces")

	if err := os.Remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove state db: %w", err)
	}
	if err := os.RemoveAll(workspacesPath); err != nil {
		return fmt.Errorf("remove workspaces: %w", err)
	}

	if stdout != nil {
		fmt.Fprintf(stdout, "reset complete\nstate_db=%s\nworkspaces=%s\npreserved=%s\n", statePath, workspacesPath, filepath.Join(dataDir, "weixin-bridge"))
	}
	return nil
}
