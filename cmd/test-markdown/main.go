package main

import (
	"fmt"
	"coding-agent/pkg/markdown"
)

func main() {
	input := "```go\nfunc main() {\n\tfmt.Println(\"Hello\")\n}\n```"
	rendered, err := markdown.Render(input)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Println("--- Rendered Output ---")
	fmt.Print(rendered)
	fmt.Println("--- End Output ---")
}

