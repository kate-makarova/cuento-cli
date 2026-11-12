package main

import (
	"embed"
	"strings"
)

//go:embed pipelines
var pipelinesFS embed.FS

func mustReadPipeline(path string) string {
	data, err := pipelinesFS.ReadFile(path)
	if err != nil {
		panic("missing embedded pipeline: " + path)
	}
	return string(data)
}

func mustReadPipelineTemplate(path, projectName string) string {
	return strings.ReplaceAll(mustReadPipeline(path), "__PROJECT_NAME__", projectName)
}

// ─── Deploy workflows ─────────────────────────────────────────────────────────

func backendWorkflow(projectName string) string {
	return mustReadPipelineTemplate("pipelines/backend/deploy.yml", projectName)
}

var devWorkflowDisabled = mustReadPipeline("pipelines/deploy-dev.yml")

func frontendWorkflow(projectName string) string {
	return mustReadPipelineTemplate("pipelines/frontend/deploy.yml", projectName)
}

func frontendMonitorWorkflow(projectName string) string {
	return mustReadPipelineTemplate("pipelines/frontend/monitor.yml", projectName)
}

// ─── Sonic workflow ───────────────────────────────────────────────────────────

var (
	sonicWorkflow    = mustReadPipeline("pipelines/sonic/install.yml")
	sonicCfgFile     = mustReadPipeline("pipelines/sonic/sonic.cfg")
	sonicServiceFile = mustReadPipeline("pipelines/sonic/sonic.service")
)

// ─── Qdrant workflow ──────────────────────────────────────────────────────────

var (
	qdrantWorkflow    = mustReadPipeline("pipelines/qdrant/install.yml")
	qdrantServiceFile = mustReadPipeline("pipelines/qdrant/qdrant.service")
)

// ─── Nginx image hosting workflow ─────────────────────────────────────────────

var (
	nginxImageHostingWorkflow = mustReadPipeline("pipelines/nginx-image-hosting/install.yml")
	nginxMainConf             = mustReadPipeline("pipelines/nginx-image-hosting/nginx.conf")
	nginxSiteConf             = mustReadPipeline("pipelines/nginx-image-hosting/image-hosting.conf")
	nginxServiceFile          = mustReadPipeline("pipelines/nginx-image-hosting/nginx-image-hosting.service")
)
