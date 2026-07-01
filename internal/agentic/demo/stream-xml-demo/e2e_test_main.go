//go:build ignore
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main
import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"

	agentic "github.com/pijalu/agentic"
	"github.com/pijalu/agentic/demo/shared"
	"github.com/pijalu/agentic/observer/xmlstream"
	"github.com/pijalu/agentic/skillrunner"
)

// bufferWriter implements StreamingXMLWriter for capturing output
type bufferWriter struct {
	buf bytes.Buffer
}

func (bw *bufferWriter) WriteChunk(chunk string) error {
	bw.buf.WriteString(chunk)
	return nil
}

func (bw *bufferWriter) Close() error {
	return nil
}

func (bw *bufferWriter) String() string {
	return bw.buf.String()
}

func main() {
	cfg := shared.Parse(
		"http://localhost:1234/v1/chat/completions",
		"local-model",
	)

	fmt.Println("=== XML Streaming E2E Test ===")
	fmt.Println()

	xmlContent, err := runE2ETest(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	printXMLSummary(xmlContent)
	if err := validateXML(xmlContent); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	checkQuality(xmlContent)

	fmt.Println()
	fmt.Println("=== Test Complete ===")
}

func runE2ETest(cfg shared.Config) (string, error) {
	buf := &bufferWriter{}
	obs, err := xmlstream.NewXMLStreamingObserver(xmlstream.Config{
		Writer:         buf,
		Model:          cfg.Model,
		ConversationID: "e2e-test",
		IncludeTimings: true,
	})
	if err != nil {
		return "", fmt.Errorf("create XML observer: %w", err)
	}

	provider := agentic.NewOpenAIProvider(cfg.Endpoint, cfg.Model)
	if cfg.APIKey != "" {
		provider.APIKey = cfg.APIKey
	}
	provider.Logger = agentic.NewLogger(agentic.Warn)

	loader := skillrunner.NewFileSkillsLoader([]string{"./skills"})
	runner, err := skillrunner.NewRunner(skillrunner.Config{
		Loader:   loader,
		Provider: provider,
		WorkDir:  ".",
		Logger:   agentic.NewLogger(agentic.Warn),
		Observer: obs,
	})
	if err != nil {
		return "", fmt.Errorf("create runner: %w", err)
	}

	agent := agentic.NewAgent(agentic.Config{
		Provider:     provider,
		SystemPrompt: "You are a helpful assistant with skills.\n" + runner.GenerateSkillsSection(),
		Logger:       agentic.NewLogger(agentic.Warn),
		Tools:        []agentic.Tool{runner},
	})
	agent.AddObserver(obs)

	fmt.Println("Running agent with skill call...")
	agent.Run(context.Background(), "What time is it?")
	agent.Stop()
	obs.Flush()
	return buf.String(), nil
}

func printXMLSummary(xmlContent string) {
	fmt.Println("=== Generated XML (truncated) ===")
	if len(xmlContent) > 2000 {
		fmt.Println(xmlContent[:2000])
		fmt.Println("... [truncated]")
	} else {
		fmt.Println(xmlContent)
	}
	fmt.Println()

	fmt.Printf("Message count: %d\n", bytes.Count([]byte(xmlContent), []byte("<message>")))
	fmt.Printf("Thinking blocks: %d\n", bytes.Count([]byte(xmlContent), []byte("<thinking>")))
	contentCount := bytes.Count([]byte(xmlContent), []byte("<content>")) - bytes.Count([]byte(xmlContent), []byte("]]></content>"))
	fmt.Printf("Content blocks: %d\n", contentCount)
}

func validateXML(xmlContent string) error {
	fmt.Println()
	fmt.Println("=== XML Validation ===")
	if _, err := exec.LookPath("xmllint"); err != nil {
		fmt.Println("xmllint not available, skipping validation")
		return nil
	}
	tmpFile := "/tmp/e2e-test.xml"
	if err := os.WriteFile(tmpFile, []byte(xmlContent), 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	cmd := exec.Command("xmllint", "--format", tmpFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("XML validation FAILED:\n%s", output)
	}
	fmt.Println("XML is valid!")
	return nil
}

func checkQuality(xmlContent string) {
	fmt.Println()
	fmt.Println("=== Quality Checks ===")

	xmlBytes := []byte(xmlContent)
	shortBlocks := countShortBlocks(xmlBytes)
	if shortBlocks > 0 {
		fmt.Printf("WARNING: Found %d potentially split blocks (short content < 5 chars)\n", shortBlocks)
	} else {
		fmt.Println("✓ No split blocks detected")
	}

	printTagBalance(xmlBytes, "skillcall")
	printTagBalance(xmlBytes, "toolcall")
}

func countShortBlocks(xmlBytes []byte) int {
	shortBlocks := 0
	for _, line := range bytes.Split(xmlBytes, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if !bytes.HasPrefix(trimmed, []byte("<thinking>")) && !bytes.HasPrefix(trimmed, []byte("<content>")) {
			continue
		}
		startIdx := bytes.Index(line, []byte(">"))
		endIdx := bytes.LastIndex(line, []byte("<"))
		if startIdx <= 0 || endIdx <= startIdx {
			continue
		}
		content := string(line[startIdx+1 : endIdx])
		if len(content) <= 5 && content != " " {
			shortBlocks++
		}
	}
	return shortBlocks
}

func printTagBalance(xmlBytes []byte, tag string) {
	open := bytes.Count(xmlBytes, []byte("<"+tag+">"))
	close := bytes.Count(xmlBytes, []byte("</"+tag+">"))
	if open != close {
		fmt.Printf("WARNING: %s mismatch: %d open, %d close\n", tag, open, close)
	} else {
		fmt.Printf("✓ %s tags balanced\n", tag)
	}
}
