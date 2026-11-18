package main

import (
	"fmt"
	"os"

	"github.com/hongzio/tailchase/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "tailchase:", err)
		os.Exit(1)
	}
}
