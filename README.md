# MCode CLI

A Go-based coding agent that uses LM Studio locally to help with programming tasks through a command-line interface.

## Features

Based on minimalist principles, this agent is optimized for **local LLM performance**:

- **Context Efficiency Core**: Engineered to prevent context inflation, ensuring high speed even on consumer hardware.
- **Token-Aware Truncation**: Automatically trims tool outputs and history to maintain high-signal context.
- **Core Agent Architecture**: minimalist Go implementation with low overhead.
- **Essential Tools**:
  1. `read_file` - Paginated file reading
  2. `list_files` - Truncated directory listing
  3. `bash_command` - Shell execution
  4. `edit_file` - Precision incremental editing (find/replace)
  5. `write_file` - Targeted file creation
  6. `search_code` - High-speed grep-based searching
  7. `web_search` - Internet search for current docs and external facts
  8. `web_fetch` - Fetch and read a specific web page
- **Local Model**: Optimized for `qwen3-coder` and other local weights.

## Prerequisites

1. Install and run LM Studio with the model loaded:
   - Download LM Studio from https://lmstudio.ai
   - Load the model: `lmstudio-community/qwen3-coder-30b-a3b-instruct-mlx@8bit`
   - Start the local server on `http://localhost:1234`

## Setup

1. Install dependencies:
```bash
go mod tidy
```

2. Build the agent (this will auto-increment the build version):
```bash
make build
```

3. (Optional) Make it globally available by symlinking it into your local bin directory:
```bash
# Assuming ~/.local/bin is in your PATH
ln -sf $(pwd)/mcode ~/.local/bin/mcode
```

## Usage

### Interactive Mode
```bash
./mcode
```

### Single Command Mode  
```bash
./mcode "List all Go files in the current directory"
./mcode "Create a simple HTTP server in Go"
./mcode "Find all TODO comments in the code"
```

## Examples

- **File Operations**: "Show me the contents of main.go"
- **Code Generation**: "Create a REST API handler for user management"
- **Code Analysis**: "Find all functions that use the database connection"
- **Refactoring**: "Optimize the error handling in this file"
- **Testing**: "Generate unit tests for the user service"
- **Web Research**: "Search the web for the latest Go 1 release notes"
- **Web Page Fetching**: "Fetch the Go release notes page and summarize it"

## Web Access

`web_search` uses DuckDuckGo by default and supports `include_domains` / `exclude_domains` filters. `web_fetch` retrieves the contents of a specific URL after it has been identified. Both tools are gated by explicit saved permissions, and the search backend can be overridden with `MCODE_WEB_SEARCH_ENDPOINT` and `MCODE_WEB_SEARCH_INSTANT_ENDPOINT`.

## Architecture

The agent follows the minimalist approach outlined in the referenced articles:
- Local LLM integration via OpenAI-compatible API
- Stateless conversation management with full history
- Dynamic tool registration
- Simple input/output handling
- Local-first by default, with optional internet access via `web_search` and `web_fetch`

Each tool interaction includes the full conversation history, allowing the Qwen3 Coder model to maintain context while being stateless between invocations.

## Key Benefits

- **Privacy**: Core coding flows stay local; `web_search` and `web_fetch` are the explicit opt-in paths for public web access
- **Speed**: Direct local model inference without API latency
- **Cost**: No per-token charges - unlimited usage once model is downloaded
- **Customization**: Easy to modify for specific coding workflows

## Slash Commands

- `/init` - Initialize project and create AGENTS.md documentation
- `/new` - Clear conversation context (start fresh session)
- `/export` - Export conversation context to text file
- `/models` - List or switch between available models
- `/permissions` - Manage folder and web permissions
- `/compact` - Compact conversation context to save tokens
- `/exit` - Exit the agent gracefully  
- `/help` - Show available commands and usage
