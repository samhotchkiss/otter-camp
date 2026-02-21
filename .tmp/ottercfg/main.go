package main

import (
	"fmt"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
)

func main() {
	cfg, err := ottercli.LoadConfig()
	if err != nil {
		panic(err)
	}
	fmt.Printf("api=%s\norg=%s\ntoken_len=%d\n", cfg.APIBaseURL, cfg.DefaultOrg, len(cfg.Token))
}
