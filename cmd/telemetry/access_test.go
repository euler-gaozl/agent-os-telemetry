package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestExecutionAccessReportMatchesRelease(t *testing.T) {
	var output bytes.Buffer
	if !reportExecutionAccess([]string{"--execution-access-profile"}, &output) {
		t.Fatal("capability report not handled")
	}
	var report, release map[string]any
	if e := json.Unmarshal(output.Bytes(), &report); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("../../deploy/release-contract.json")
	if e != nil || json.Unmarshal(raw, &release) != nil {
		t.Fatal("release metadata invalid")
	}
	capability := release["execution_access"].(map[string]any)
	if report["format"] != capability["format"] || report["profile"] != capability["profile"] || report["role"] != release["role"] || len(report) != 3 {
		t.Fatal("binary and release access profiles differ")
	}
	for _, args := range [][]string{nil, {"--config", "fixture.json"}, {"--execution-access-profile", "--config", "fixture.json"}} {
		output.Reset()
		if reportExecutionAccess(args, &output) || output.Len() != 0 {
			t.Fatal("capability report intercepted normal or mixed invocation")
		}
	}
}
