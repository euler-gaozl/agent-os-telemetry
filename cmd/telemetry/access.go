package main

import (
	"fmt"
	"io"
)

// Reporting never loads deployment configuration or opens a connection.
func reportExecutionAccess(args []string, output io.Writer) bool {
	if len(args) != 1 || args[0] != "--execution-access-profile" {
		return false
	}
	fmt.Fprintln(output, `{"format":1,"role":"telemetry","profile":"telemetry-http-v1"}`)
	return true
}
