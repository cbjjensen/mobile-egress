package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	fmt.Fprintln(os.Stdout, "Funnel is not enabled on your tailnet.")
	fmt.Fprintln(os.Stdout, "https://login.tailscale.com/f/funnel?node=test-node")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(os.Args[1]); err == nil {
			fmt.Fprintln(os.Stdout, "Funnel approved")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(3)
}
