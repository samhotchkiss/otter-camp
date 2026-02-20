package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
)

func main() {
	cfg, err := ottercli.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	client, err := ottercli.NewClient(cfg, strings.TrimSpace(cfg.DefaultOrg))
	if err != nil {
		log.Fatal(err)
	}

	runResp, err := client.RunOpenClawMigration(ottercli.OpenClawMigrationRunRequest{
		StartPhase:        "project_discovery",
		ForceResumePaused: true,
	})
	if err != nil {
		log.Fatalf("run openclaw migration: %v", err)
	}
	payload, _ := json.MarshalIndent(runResp, "", "  ")
	fmt.Println("RUN RESPONSE:")
	fmt.Println(string(payload))

	for i := 0; i < 20; i++ {
		status, err := client.GetOpenClawMigrationStatus()
		if err != nil {
			log.Fatalf("status: %v", err)
		}
		fmt.Printf("\nSTATUS POLL %d (active=%v)\n", i+1, status.Active)
		for _, phase := range status.Phases {
			if phase.MigrationType == "project_discovery" || phase.MigrationType == "project_docs_scanning" {
				total := 0
				if phase.TotalItems != nil {
					total = *phase.TotalItems
				}
				fmt.Printf("  %s: status=%s processed=%d total=%d failed=%d label=%q\n",
					phase.MigrationType,
					phase.Status,
					phase.ProcessedItems,
					total,
					phase.FailedItems,
					phase.CurrentLabel,
				)
			}
		}
		if !status.Active {
			break
		}
		time.Sleep(3 * time.Second)
	}
}
