//go:build !ios

package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/tui"
)

func main() {
	apiURL := flag.String("api", config.LocalAPIURL(config.DevServerPort), "traio API base URL")
	flag.Parse()

	_ = apiURL // wired in phase 2

	p := tea.NewProgram(tui.New(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tui: %v\n", err)
		os.Exit(1)
	}
}
