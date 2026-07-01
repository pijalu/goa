<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# XML Streaming Observer

The XML Streaming Observer (`observer/xmlstream`) provides real-time XML output of agent conversations. It implements the `agentic.OutputObserver` interface and writes conversation events as incremental XML chunks to any `StreamingXMLWriter`.

## Features

- **Character-level streaming**: XML chunks are written as events arrive, enabling real-time client parsing
- **Role-based message separation**: System, User, and Assistant messages are properly separated into distinct `<message>` elements
- **Nested skill support**: Skill calls include nested `<conversation>` blocks for sub-agent events
- **Flexible output**: Multiple writer implementations (Console, HTTP, Callback)
- **Configurable**: Custom model name, conversation ID, timing statistics

## XML Structure

```xml
<conversation>
  <metadata>
    <id>unique-conversation-id</id>
    <model>gpt-4</model>
    <start>2024-01-01T00:00:00Z</start>
  </metadata>
  <messages>
    <message>
      <role>system</role>
      <blocks>
        <content>System prompt content</content>
      </blocks>
    </message>
    <message>
      <role>user</role>
      <metadata>
        <item key="category">finance</item>
        <item key="internal">true</item>
      </metadata>
      <blocks>
        <content>User message</content>
      </blocks>
    </message>
    <message>
      <role>assistant</role>
      <blocks>
        <thinking>Reasoning process...</thinking>
        <content>Final response...</content>
        <stats>
          <tokens>
            <prompt>10</prompt>
            <predicted>50</predicted>
          </tokens>
          <timing_ms>
            <prompt>5.00</prompt>
            <predicted>100.00</predicted>
          </timing_ms>
        </stats>
      </blocks>
    </message>
    <message>
      <role>assistant</role>
      <blocks>
        <toolcall>
          <name>calculator</name>
          <input><![CDATA[{"a":5,"b":3,"op":"+"}]]></input>
          <output><![CDATA[8]]></output>
        </toolcall>
      </blocks>
    </message>
    <message>
      <role>assistant</role>
      <blocks>
        <skillcall>
          <name>file-resumer</name>
          <input><![CDATA[{"skill_name":"file-resumer","task":"resume /tmp/file.txt"}]]></input>
          <conversation>
            <!-- Nested skill sub-agent conversation -->
          </conversation>
          <output><![CDATA[Skill result output]]></output>
        </skillcall>
      </blocks>
    </message>
  </messages>
</conversation>
```

### Block Types

| Block | Description |
|-------|-------------|
| `<content>` | Regular text content from LLM or tool result |
| `<thinking>` | Reasoning/thinking tokens (if supported by model) |
| `<toolcall>` | Tool execution request with input/output |
| `<skillcall>` | Skill execution with nested conversation |
| `<stats>` | Token timing statistics (if enabled) |
| `<metadata>` | Opaque key/value pairs attached to a message (not sent to LLM) |

## Usage

### Basic Setup

```go
import (
    "os"
    
    agentic "github.com/pijalu/agentic"
    "github.com/pijalu/agentic/observer/xmlstream"
)

// Create the observer
obs, err := xmlstream.NewXMLStreamingObserver(xmlstream.Config{
    Writer:         xmlstream.NewConsoleWriter(os.Stdout),
    Model:          "gpt-4",
    ConversationID: "my-conversation-123",
    IncludeTimings: true,
})
if err != nil {
    // Handle error
}

// Create agent and add observer
agent := agentic.NewAgent(agentic.Config{
    Provider:     provider,
    SystemPrompt: "You are a helpful assistant.",
})

agent.AddObserver(obs)

// Run conversation
agent.Run(ctx, "Hello!")
obs.Flush() // Important: Call Flush() to write closing tags
```

### With Skills (Shared Observer)

To stream events from skill sub-agents, share the observer with the skill runner:

```go
import "github.com/pijalu/agentic/skillrunner"

// Create XML streaming observer
obs, _ := xmlstream.NewXMLStreamingObserver(xmlstream.Config{
    Writer: xmlstream.NewConsoleWriter(os.Stdout),
    Model:  "local-model",
})

// Create skill runner with shared observer
runner, _ := skillrunner.NewRunner(skillrunner.Config{
    Loader:   skillrunner.NewFileSkillsLoader([]string{"./skills"}),
    Provider: provider,
    WorkDir:  ".",
    Observer: obs,  // Share observer with sub-agents
})

// Create agent
agent := agentic.NewAgent(agentic.Config{
    Provider:     provider,
    SystemPrompt: "You are helpful.\n" + runner.GenerateSkillsSection(),
    Tools:        []agentic.Tool{runner},
})

// Add observer to main agent
agent.AddObserver(obs)
```

### HTTP Streaming

For HTTP server-sent events or chunked transfer:

```go
import "net/http"

func streamHandler(w http.ResponseWriter, r *http.Request) {
    // Create HTTP chunked writer
    hw := xmlstream.NewHTTPChunkedWriter(100)
    
    // Create observer
    obs, _ := xmlstream.NewXMLStreamingObserver(xmlstream.Config{
        Writer: hw,
        Model:  "gpt-4",
    })
    
    // Create and run agent
    agent := agentic.NewAgent(cfg)
    agent.AddObserver(obs)
    agent.Run(r.Context(), "Hello!")
    obs.Flush()
    
    // Flush to HTTP response
    xmlstream.FlushHTTP(w, r, hw)
}
```

### Custom Writer (Callback)

```go
// Using callback functions
writer := xmlstream.NewCallbackWriter(
    func(chunk string) error {
        fmt.Print(chunk)  // Write to console
        return nil
    },
    func() error {
        return nil  // Cleanup
    },
)

obs, _ := xmlstream.NewXMLStreamingObserver(xmlstream.Config{
    Writer: writer,
    Model:  "model-name",
})
```

## Writers

### ConsoleWriter

Writes XML chunks to an `io.Writer`:

```go
writer := xmlstream.NewConsoleWriter(os.Stdout)
// or
writer := xmlstream.NewConsoleWriter(myFile)
```

### CallbackWriter

Writes via callback functions:

```go
writer := xmlstream.NewCallbackWriter(
    writeFunc func(chunk string) error,
    closeFunc func() error,
)
```

### HTTPChunkedWriter

Channel-based writer for HTTP streaming:

```go
hw := xmlstream.NewHTTPChunkedWriter(bufferSize)

// In goroutine-safe scenarios:
for chunk := range hw.Output() {
    fmt.Fprintf(w, "%x\r\n%s\r\n", len(chunk), chunk)
    flusher.Flush()
}
```

Use `FlushHTTP()` helper for standard chunked transfer encoding.

## Configuration

```go
type Config struct {
    // Writer is the destination for XML chunks. Required.
    Writer StreamingXMLWriter
    
    // Model is the LLM model name for metadata. Required.
    Model string
    
    // ConversationID is a unique identifier.
    // If empty, a UUID-like string is generated.
    ConversationID string
    
    // IncludeTimings controls token stats output.
    // Defaults to true if not set.
    IncludeTimings bool
}
```

## Event Flow

The observer processes events in this order:

1. **EventStateChange** → Updates block type tracking
2. **EventContent** → Writes content/thinking tokens as they arrive
3. **EventToolCall** → Writes tool call structure
4. **EventToolResult** → Writes tool output
5. **EventTokenStats** → Stores stats for later output
6. **EventEnd** → Closes message, writes pending stats
7. **Flush()** → Writes closing tags and closes writer

## Integration with Skill Runner

The `skillrunner.Config` includes an `Observer` field:

```go
type Config struct {
    Loader   *SkillsLoader
    Provider agentic.LLMProvider
    WorkDir  string
    Observer agentic.OutputObserver  // Shared observer for sub-agents
    
    // ... other fields
}
```

When set, all skill sub-agents forward their events to this observer, enabling complete conversation streaming including nested skill executions.

## Message Metadata

Messages can carry opaque `map[string]string` metadata that is visible in the XML stream but **never sent to the LLM**. Use `agent.RunWithMetadata` to attach tags such as category, visibility flags, or routing hints to individual user messages.

```go
agent.RunWithMetadata(ctx, "Analyze Q3 earnings", map[string]string{
    "category": "finance",
    "internal": "true",
})
```

The XML observer emits metadata as a `<metadata>` element inside each `<message>`:

```xml
<message>
  <role>user</role>
  <metadata>
    <item key="category">finance</item>
    <item key="internal">true</item>
  </metadata>
  <blocks>
    <content>Analyze Q3 earnings</content>
  </blocks>
</message>
```

Metadata is preserved in `GetHistory()` and forwarded through the Output channel and all observers.

## Example Output

```xml
<conversation>
  <metadata>
    <id>stream-demo-123</id>
    <model>llama-3</model>
    <start>2024-01-15T10:30:00Z</start>
  </metadata>
  <messages>
    <message>
      <role>system</role>
      <blocks>
        <content>You are a helpful coding assistant.</content>
      </blocks>
    </message>
    <message>
      <role>user</role>
      <blocks>
        <content>Write a hello world program in Go</content>
      </blocks>
    </message>
    <message>
      <role>assistant</role>
      <blocks>
        <content>Here's a simple Hello World program in Go:</content>
        <content>```go
package main

import "fmt"

func main() {
    fmt.Println("Hello, World!")
}
```</content>
        <stats>
          <tokens>
            <prompt>25</prompt>
            <predicted>85</predicted>
          </tokens>
          <timing_ms>
            <prompt>12.50</prompt>
            <predicted>234.18</predicted>
          </timing_ms>
        </stats>
      </blocks>
    </message>
  </messages>
</conversation>
```

## Error Handling

- Writer errors are logged but don't stop the stream
- The observer recovers from panics in callbacks
- Call `Flush()` even if errors occur to ensure proper XML closure

## Testing

```bash
go test ./observer/xmlstream/... -v
```

Run integration tests:

```bash
go test ./observer/xmlstream/... -v -run Integration
```
