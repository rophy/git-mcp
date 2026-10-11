package main

import (
	"fmt"
	"os"

	"github.com/rophy/git-mcp/internal/config"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "serve" {
		fmt.Fprintf(os.Stderr, "Usage: git-mcp serve --config <path>\n")
		os.Exit(1)
	}

	configPath := ""
	for i, arg := range os.Args {
		if arg == "--config" && i+1 < len(os.Args) {
			configPath = os.Args[i+1]
		}
	}

	if configPath == "" {
		fmt.Fprintf(os.Stderr, "Error: --config is required\n")
		os.Exit(1)
	}

	_, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Config loaded successfully")
}
