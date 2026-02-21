package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
)

func main() {
	cfg, err := ottercli.LoadConfig()
	if err != nil {
		panic(err)
	}
	client, err := ottercli.NewClient(cfg, strings.TrimSpace(cfg.DefaultOrg))
	if err != nil {
		panic(err)
	}

	projects, err := client.ListProjectsWithWorkflow(false)
	if err != nil {
		panic(err)
	}
	type row struct {
		Name       string
		ID         string
		Total      int
		Open       int
		Closed     int
		Queued     int
		InProgress int
		Blocked    int
		Review     int
		Done       int
		Cancelled  int
	}
	rows := make([]row, 0, len(projects))
	var totalIssues int
	for _, p := range projects {
		issues, err := client.ListIssues(p.ID, map[string]string{"limit": "500"})
		if err != nil {
			fmt.Printf("%s (%s): ERROR %v\n", p.Name, p.ID, err)
			continue
		}
		r := row{Name: p.Name, ID: p.ID, Total: len(issues)}
		for _, is := range issues {
			if strings.EqualFold(strings.TrimSpace(is.State), "closed") {
				r.Closed++
			} else {
				r.Open++
			}
			switch strings.ToLower(strings.TrimSpace(is.WorkStatus)) {
			case "queued", "planning", "ready", "ready_for_work":
				r.Queued++
			case "in_progress":
				r.InProgress++
			case "blocked", "flagged":
				r.Blocked++
			case "review":
				r.Review++
			case "done":
				r.Done++
			case "cancelled":
				r.Cancelled++
			}
		}
		totalIssues += r.Total
		if r.Total > 0 {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Total > rows[j].Total })
	fmt.Printf("projects_with_issues=%d total_issues=%d\n", len(rows), totalIssues)
	for _, r := range rows {
		fmt.Printf("- %s (%s): total=%d open=%d closed=%d queued=%d in_progress=%d blocked=%d review=%d done=%d cancelled=%d\n",
			r.Name, r.ID[:8], r.Total, r.Open, r.Closed, r.Queued, r.InProgress, r.Blocked, r.Review, r.Done, r.Cancelled)
	}
}
