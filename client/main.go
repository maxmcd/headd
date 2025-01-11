package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/maxmcd/tund"
)

func main() {
	client := tund.NewClient("localhost:4442")

	if len(os.Args) > 1 && os.Args[1] == "pty" {
		if err := client.Pty(); err != nil {
			fmt.Fprintf(os.Stderr, "Error starting pty: %v\n", err)
			return
		}
		return
	}

	// Create a reader for stdin
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("> ")
		// Read the command from stdin
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
			continue
		}

		// Trim whitespace and newline
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Execute the command and stream output
		if err := client.Exec(tund.Command{
			Command: "bash",
			Args:    []string{"-c", line},
		}, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "Error executing command: %v\n", err)
		}
	}
}
