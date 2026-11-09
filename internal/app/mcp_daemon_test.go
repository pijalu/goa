package app

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAgentMCPDaemonOccupiedPort(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[mcpServers]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	err = runAgentMCPDaemon([]string{
		"--config", configPath,
		"--listen", listener.Addr().String(),
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("runAgentMCPDaemon() error = %v, want occupied-listen diagnostic", err)
	}
}
