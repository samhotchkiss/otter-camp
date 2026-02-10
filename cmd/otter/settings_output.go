package main

import (
	"encoding/json"
	"fmt"
	"io"
)

func printJSONTo(out io.Writer, value interface{}) {
	payload, _ := json.MarshalIndent(value, "", "  ")
	fmt.Fprintln(out, string(payload))
}
