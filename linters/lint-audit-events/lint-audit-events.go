package main

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// This package implements a custom golangci-lint linter. See
// the golangci-lint documentation for the conventions we use here:
//
// https://golangci-lint.run/contributing/new-linters/#how-to-add-a-private-linter-to-g

func checkMetadataInAuditEventImplementations(files []*ast.File, info *types.Info) error

func lintAuditEventDeclarations(p *analysis.Pass) (interface{}, error) {
	return nil, checkMetadataInAuditEventImplementations(p.Files, p.TypesInfo)
}

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
