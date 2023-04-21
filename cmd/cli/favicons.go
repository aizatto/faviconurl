package main

import (
	"fmt"
	"os"

	"github.com/aizatto/faviconurl/internal"
)

func main() {
	urls := os.Args[1:]

	err := internal.ParseArgs(urls)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
