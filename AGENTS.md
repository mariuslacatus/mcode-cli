# mcode - AI Agent Instructions

## Project Overview
**Project Name:** mcode  
**Location:** /Users/marius/Documents/development/mcode  
**Initialized:** 2025-08-24 15:53:41  

## Project Structure
```
mcode/
├── cmd/              # Command-line interface entry points
├── pkg/
│   ├── agent/        # Core agent logic and chat functionality
│   ├── commands/     # Slash command handlers
│   ├── config/       # Configuration management
│   ├── markdown/     # Markdown processing utilities
│   ├── project/      # Project management and permanent instructions
│   ├── tools/        # Tool implementations (read_file, write_file, etc.)
│   ├── tui/          # Terminal UI components
│   └── types/        # Type definitions and interfaces
├── main.go           # Main entry point
├── go.mod            # Go module definition
└── AGENTS.md         # This file
```

## Development Guidelines
- always use Go best practices for this project
- test instruction for comprehensive check
- Keep tools simple and focused on single responsibilities
- Use clear, descriptive function names
- Add comprehensive error handling
- Document all public functions with comments

## AI Agent Instructions

### Permanent Instructions
- always use Go best practices for this project
- test instruction for comprehensive check
- When adding new tools, ensure they follow the existing pattern:
- coding-agent/pkg/agent
  1. Register the tool in `RegisterTools()` function
  2. Add tool definition to `GetToolDefinitions()` function
  3. Implement the tool function with proper error handling
  4. Update documentation

### Project Context
*Key information about this project that AI agents should know*

## Available Tools

### 1. read_file
Read the contents of a file.
- Parameters: `path` (string) - Path to the file to read
- Returns: File contents as string

### 2. write_file
Write content to a file, creating it if it doesn't exist or overwriting if it does.
- Parameters: 
  - `path` (string) - The absolute path to the file to write
  - `content` (string) - The content to write to the file
- Returns: Success message with file path and preview of written content
- Use this for complete file replacements when edit_file causes confusion

### 3. list_files
List files in a directory.
- Parameters: `path` (string) - Directory path to list (defaults to current directory)
- Returns: List of files and directories

### 4. bash_command
Execute a bash command.
- Parameters: `command` (string) - Command to execute
- Returns: Command output and status
- Supports timeout handling and background execution

### 5. edit_file
Perform incremental edits to a file using find-and-replace.
- Parameters:
  - `filePath` (string) - The absolute path to the file to modify
  - `oldString` (string) - The text to replace (for editing existing files)
  - `newString` (string) - The replacement text
  - `replaceAll` (boolean) - Replace all occurrences (default: false)
- Returns: Diff showing changes made
- Use this for targeted edits when you know the exact text to replace

### 6. search_code
Search for code patterns in files.
- Parameters:
  - `pattern` (string) - Pattern to search for
  - `directory` (string) - Directory to search in (defaults to current directory)
- Returns: Matching lines of code

## Usage Examples

### Creating a new file
```
write_file with path: /path/to/newfile.go and content: "package main\n\nfunc main() {\n\tfmt.Println(\"Hello\")\n}"
```

### Overwriting an existing file
```
write_file with path: /path/to/existing.go and content: "new content here"
```

### Reading a file
```
read_file with path: /path/to/file.go
```

### Listing files
```
list_files with path: /path/to/directory
```

### Executing commands
```
bash_command with command: "ls -la"
```

### Editing files
```
edit_file with filePath: /path/to/file.go, oldString: "old code", newString: "new code"
```

### Searching code
```
search_code with pattern: "TODO" and directory: /path/to/project
```

## Slash Commands
- `/help` - Show available commands and usage
- `/init` - Initialize project and create AGENTS.md documentation
- `/new` - Clear conversation context (start fresh session)
- `/export` - Export conversation to markdown
- `/models` - List available models
- `/permissions` - Show current permissions
- `/compact` - Compress conversation context
- `/exit` - Exit the agent gracefully

## Permanent Instructions
Use `#instruction` prefix to add permanent instructions that will be saved to AGENTS.md and remembered across sessions.
