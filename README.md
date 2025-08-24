# MCode CLI

A Go-based coding agent that uses LM Studio locally with Qwen3 Coder to help with programming tasks through a command-line interface.

## Features

Based on the principles from the referenced articles, this agent implements:

- **Core Agent Architecture**: ~300-line implementation with local LLM integration
- **Five Essential Tools**:
  1. `read_file` - Read file contents
  2. `list_files` - List directory contents  
  3. `bash_command` - Execute shell commands
  4. `edit_file` - Create/modify files
  5. `search_code` - Search for code patterns
- **Local Model**: Uses `lmstudio-community/qwen3-coder-30b-a3b-instruct-mlx@8bit`

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

2. Build the agent:
```bash
go build -o mcode-cli
```

## Usage

### Interactive Mode
```bash
./mcode-cli
```

### Single Command Mode  
```bash
./mcode-cli "List all Go files in the current directory"
./mcode-cli "Create a simple HTTP server in Go"
./mcode-cli "Find all TODO comments in the code"
```

## Examples

- **File Operations**: "Show me the contents of main.go"
- **Code Generation**: "Create a REST API handler for user management"
- **Code Analysis**: "Find all functions that use the database connection"
- **Refactoring**: "Optimize the error handling in this file"
- **Testing**: "Generate unit tests for the user service"

## Architecture

The agent follows the minimalist approach outlined in the referenced articles:
- Local LLM integration via OpenAI-compatible API
- Stateless conversation management with full history
- Dynamic tool registration
- Simple input/output handling
- No external API keys required - runs completely locally

Each tool interaction includes the full conversation history, allowing the Qwen3 Coder model to maintain context while being stateless between invocations.

## Key Benefits

- **Privacy**: All processing happens locally - no data sent to external services
- **Speed**: Direct local model inference without API latency
- **Cost**: No per-token charges - unlimited usage once model is downloaded
- **Customization**: Easy to modify for specific coding workflows

## Slash Commands

- `/init` - Initialize project and create AGENTS.md documentation
- `/new` - Clear conversation context (start fresh session)
- `/exit` - Exit the agent gracefully  
- `/help` - Show available commands and usage