package main

import (
	"os"

	"github.com/pipepie/pipepie/cmd"
	"github.com/charmbracelet/lipgloss"
)

func main() {
	// Support NO_COLOR standard (https://no-color.org/)
	if os.Getenv("NO_COLOR") != "" {
		lipgloss.SetHasDarkBackground(false)
		os.Setenv("TERM", "dumb")
	}
	cmd.Execute()
}
