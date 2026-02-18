package main

import (
	"fmt"
	"log"

	importer "github.com/samhotchkiss/otter-camp/internal/import"
)

func main() {
	install, err := importer.DetectOpenClawInstallation(importer.DetectOpenClawOptions{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Root: %s\n", install.RootDir)
	fmt.Printf("Sessions: %s\n", install.SessionsDir)
	fmt.Printf("Agents: %d\n", len(install.Agents))

	events, err := importer.ParseOpenClawSessionEvents(install)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Total parsed events: %d\n", len(events))
	
	byAgent := map[string]int{}
	for _, e := range events {
		byAgent[e.AgentSlug]++
	}
	for slug, count := range byAgent {
		fmt.Printf("  %s: %d\n", slug, count)
	}
}
