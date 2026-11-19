package main

import (
	"strings"
	"testing"
)

func TestFrontendMonitorWorkflow_ManualOnly(t *testing.T) {
	t.Parallel()
	wf := frontendMonitorWorkflow("dominion")
	if strings.Contains(wf, "\n  push:") {
		t.Errorf("monitor workflow must not have a push trigger: any CLI commit to the monitor branch would deploy it to production")
	}
	if !strings.Contains(wf, "\n  workflow_dispatch:") {
		t.Errorf("monitor workflow must keep workflow_dispatch: the CLI's Deploy monitor dispatches it")
	}
}

func TestDeployWorkflows_Concurrency(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		wf    string
		group string
	}{
		{"frontend release", frontendWorkflow("dominion"), "group: frontend-deploy"},
		{"frontend monitor", frontendMonitorWorkflow("dominion"), "group: frontend-deploy"},
		{"backend", backendWorkflow("dominion"), "group: backend-deploy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(tt.wf, "\nconcurrency:\n  "+tt.group+"\n  cancel-in-progress: false\n") {
				t.Errorf("expected top-level concurrency %q without cancel-in-progress", tt.group)
			}
		})
	}
}
