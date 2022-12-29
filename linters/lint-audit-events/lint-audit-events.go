package main

import (
	"fmt"

	"golang.org/x/tools/go/analysis"
)

// This package implements a custom golangci-lint linter. See
// the golangci-lint documentation for the conventions we use here:
//
// https://golangci-lint.run/contributing/new-linters/#how-to-add-a-private-linter-to-g

func lintAuditEventDeclarations(p *analysis.Pass) (interface{}, error) {

	fmt.Println("PACKAGE:", p.Pkg.Name())

	if p.Pkg.Name() != "badimpl" {
		return nil, nil
	}

	fmt.Println("current package: ", p.Pkg.Name())

	for _, v := range p.TypesInfo.Types {
		if v.Type != nil {
			fmt.Printf("here's a type with string %v\n", v.Type.String())
		}
	}

	for _, v := range p.TypesInfo.Defs {
		if v != nil {
			fmt.Printf("here's a type def with name %v\n", v.Name())
		} else {
			continue
		}

		if v.Name() == "BadAuditEventImplementation" {

			fmt.Printf("here is BadAuditEventImplementation's type: %+v\n", v.Type())
			fmt.Printf("here is BadAuditEventImplementation's underlying type: %+v\n", v.Type().Underlying())
		}

	}

	for _, v := range p.TypesInfo.Instances {
		if v.Type != nil {
			fmt.Printf("here's a type instance with string %v\n", v.Type.String())
		}
	}

	for _, v := range p.TypesInfo.Uses {
		if v != nil {
			fmt.Printf("here's a type use: %v\n", v.Name())
		}
	}

	return nil, nil
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
