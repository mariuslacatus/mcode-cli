package tools

// ReadFileArgs defines the arguments for the read_file tool
type ReadFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// ListFilesArgs defines the arguments for the list_files tool
type ListFilesArgs struct {
	Path string `json:"path"`
}

// BashCommandArgs defines the arguments for the bash_command tool
type BashCommandArgs struct {
	Command string `json:"command"`
}

// EditFileArgs defines the arguments for the edit_file tool
type EditFileArgs struct {
	FilePath   string `json:"filePath"`
	Path       string `json:"path,omitempty"` // Alias for filePath
	OldString  string `json:"oldString,omitempty"`
	NewString  string `json:"newString"`
	ReplaceAll bool   `json:"replaceAll,omitempty"`
}

// GetFilePath returns either FilePath or Path, whichever is provided
func (a *EditFileArgs) GetFilePath() string {
	if a.FilePath != "" {
		return a.FilePath
	}
	return a.Path
}

// WriteFileArgs defines the arguments for the write_file tool
type WriteFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// SearchCodeArgs defines the arguments for the search_code tool
type SearchCodeArgs struct {
	Pattern   string `json:"pattern"`
	Directory string `json:"directory,omitempty"`
}

// WebSearchArgs defines the arguments for the web_search tool
type WebSearchArgs struct {
	Query          string   `json:"query"`
	MaxResults     int      `json:"max_results,omitempty"`
	IncludeDomains []string `json:"include_domains,omitempty"`
	ExcludeDomains []string `json:"exclude_domains,omitempty"`
}

// WebFetchArgs defines the arguments for the web_fetch tool
type WebFetchArgs struct {
	URL            string `json:"url"`
	Format         string `json:"format,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	MaxChars       int    `json:"max_chars,omitempty"`
}
