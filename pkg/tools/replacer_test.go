package tools

import (
	"testing"
)

func TestReplaceInContent(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		oldString  string
		newString  string
		replaceAll bool
		want       string
		wantErr    bool
	}{
		{
			name:      "Simple exact match",
			content:   "hello world",
			oldString: "world",
			newString: "golang",
			want:      "hello golang",
		},
		{
			name: "Line trimmed match",
			content: `func main() {
    fmt.Println("hello")
}`,
			oldString: `    fmt.Println("hello")`,
			newString: `    fmt.Println("hi")`,
			want: `func main() {
    fmt.Println("hi")
}`,
		},
		{
			name: "Indentation flexible match",
			content: `func main() {
	fmt.Println("hello")
}`,
			oldString: `    fmt.Println("hello")`,
			newString: `    fmt.Println("hi")`,
			want: `func main() {
    fmt.Println("hi")
}`,
		},
		{
			name:      "Whitespace normalized match",
			content:   `func main() {  fmt.Println("hello")  }`,
			oldString: `func main() { fmt.Println("hello") }`,
			newString: `func main() { fmt.Println("hi") }`,
			want:      `func main() { fmt.Println("hi") }`,
		},
		{
			name:       "Replace all",
			content:    "a b a c",
			oldString:  "a",
			newString:  "z",
			replaceAll: true,
			want:       "z b z c",
		},
		{
			name:      "Ambiguous match error",
			content:   "a b a c",
			oldString: "a",
			newString: "z",
			wantErr:   true,
		},
		{
			name:      "Text not found error",
			content:   "hello world",
			oldString: "missing",
			newString: "found",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReplaceInContent(tt.content, tt.oldString, tt.newString, tt.replaceAll)
			if (err != nil) != tt.wantErr {
				t.Errorf("ReplaceInContent() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ReplaceInContent() got = %v, want %v", got, tt.want)
			}
		})
	}
}
