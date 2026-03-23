package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestResetAllDataPreservesWeixinCredentials(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	statePath := filepath.Join(dataDir, "state", "store.db")
	sessionsPath := filepath.Join(dataDir, "sessions", "sess_000001")
	workspacesPath := filepath.Join(dataDir, "workspaces", "ws_000001")
	weixinAccountPath := filepath.Join(dataDir, "weixin-bridge", "account.json")

	for _, path := range []string{
		filepath.Dir(statePath),
		sessionsPath,
		workspacesPath,
		filepath.Dir(weixinAccountPath),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}
	if err := os.WriteFile(statePath, []byte("state"), 0o644); err != nil {
		t.Fatalf("write state db: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsPath, "artifact.txt"), []byte("legacy"), 0o644); err != nil {
		t.Fatalf("write legacy session file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspacesPath, "artifact.txt"), []byte("artifact"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
	if err := os.WriteFile(weixinAccountPath, []byte(`{"bot_token":"secret"}`), 0o644); err != nil {
		t.Fatalf("write weixin account: %v", err)
	}

	var output bytes.Buffer
	if err := resetAllData(dataDir, &output); err != nil {
		t.Fatalf("reset all data: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "state")); !os.IsNotExist(err) {
		t.Fatalf("expected state directory to be removed, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("expected legacy sessions directory to be removed, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "workspaces")); !os.IsNotExist(err) {
		t.Fatalf("expected workspaces directory to be removed, got err=%v", err)
	}
	if _, err := os.Stat(weixinAccountPath); err != nil {
		t.Fatalf("expected weixin account to be preserved, got err=%v", err)
	}
	if !bytes.Contains(output.Bytes(), []byte("preserved="+filepath.Join(dataDir, "weixin-bridge"))) {
		t.Fatalf("expected reset output to mention preserved weixin path, got %q", output.String())
	}
}
