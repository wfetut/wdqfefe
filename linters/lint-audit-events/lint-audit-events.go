package main

import "golang.org/x/tools/go/analysis"

// This package implements a custom golangci-lint linter. See
// the golangci-lint documentation for the conventions we use here:
//
// https://golangci-lint.run/contributing/new-linters/#how-to-add-a-private-linter-to-g

func lintAuditEventDeclarations(*analysis.Pass) (interface{}, error)

var auditEventDeclarationLinter = &analysis.Analyzer{
	Name: "lint-audit-event-declarations",
	Doc:  "ensure that Teleport audit events follow the structure required",
	Run:  lintAuditEventDeclarations,
}

type analyzerPlugin struct{}

func (a analyzerPlugin) GetAnalyzers() []*analysis.Analyzer {
	return []*analysis.Analyzer{
		auditEventDeclarationLinter,
	}
}
