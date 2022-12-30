package main

import (
	"errors"
	"fmt"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"
)

// This package implements a custom golangci-lint linter. See
// the golangci-lint documentation for the conventions we use here:
//
// https://golangci-lint.run/contributing/new-linters/#how-to-add-a-private-linter-to-g

type RequiredFieldInfo struct {
	// path to the directory in which to search for packages
	workingDir string

	// name of the Go package where we can find the target type
	packageName string

	// name of the interface type to check implementations of
	interfaceTypeName string

	// type of the interface to compare implementations against. Used to
	// check that a given struct is an implementation of the type.
	interfaceType types.Type

	requiredFieldName string

	// package to search for the required field and its type
	requiredFieldPackageName string

	requiredFieldTypeName string

	// type of the field required in structs that implement the interface in
	// interfaceType.
	requiredFieldType types.Type
}

// makeAuditEventDeclarationLinter looks up type information in the target
// package, then uses that type information to generate an analysis.Analzer. The
// analysis.Analyzer ensures that all structs that implement a particular
// interface include a field with a specific type.
func makeAuditEventDeclarationLinter(c RequiredFieldInfo) (analysis.Analyzer, error) {

	if c.workingDir == "" {
		return nil, errors.New("the directory path for looking up packages must not be empty")
	}

	if c.packageName == "" {
		return nil, errors.New("cannot load a package with an empty name")
	}

	if c.interfaceTypeName == "" {
		return nil, errors.New("the interface type name must not be blank")
	}

	if c.requiredFieldName == "" {
		return nil, errors.New("the required field name must not be blank")
	}

	if c.requiredFieldPackageName == "" {
		return nil, errors.new("the rquired field's package name must not be blank")
	}

	if c.requiredFieldTypeName == "" {
		return nil, errors.New("the required field type's name must not be blank")
	}

	// Zero out the types so we can fill them in without worry
	c.interfaceType = nil
	c.requiredFieldType = nil

	// Look up the interface
	pkg, err := packages.Load(
		packages.Config{
			Dir: c.workingDir,
		},
		c.packageName,
	)

	if err != nil {
		return nil, fmt.Errorf("could not load the package with pattern %v: %v", c.packageName, err)
	}

	if len(pkg) != 1 {
		return nil, fmt.Errorf("there must be one package for the target interface, but found %v", len(pkg))
	}

	for _, d := range pkg[0].TypesInfo.Defs {

		// Skip any non-interfaces
		if _, ok := d.Type().Underlying().(types.Interface); !ok {
			continue
		}

		if d.Name() == c.interfaceTypeName {
			if c.interfaceType == nil {
				c.interfaceType = d.Type
			} else {
				return nil, fmt.Errorf("expected only one occurrence of interface %v, but got multiple", c.interfaceTypeName)
			}
		}
	}

	// Look up the required field type
	pkg2, err := packages.Load(
		packages.Config{
			Dir: c.workingDir,
		},
		c.requiredFieldPackageName,
	)

	if err != nil {
		return nil, fmt.Errorf("could not load the package with pattern %v: %v", c.packageName, err)
	}

	if len(pkg2) != 1 {
		return nil, fmt.Errorf("there must be one package for the target field name, but found %v", len(pkg))
	}

	for _, d := range pkg2[0].TypesInfo.Defs {

		if d.Name() == c.requiredFieldTypeName {
			if c.requiredFieldType == nil {
				c.requiredFieldType = d.Type
			} else {
				return nil, fmt.Errorf("expected only one occurrence of required field type %v, but got multiple", c.requiredFieldType)
			}
		}
	}

	// Now we know:
	//
	// - which interface type we want to check implementations of
	// - which field type we want to ensure implementations contain
	//
	// The next step is to generate an analysis.Analyzer that looks up all
	// structs in a set of packages, checks if they implement the target
	// interface, and if so, ensures that they contain the required field.

	// TODO: move lintAuditEventDeclarations into an anonymous function that's
	// returned from this function.
}

func lintAuditEventDeclarations(p *analysis.Pass) (interface{}, error) {

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

var AnalyzerPlugin analyzerPlugin
