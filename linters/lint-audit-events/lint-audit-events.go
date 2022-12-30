package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"strings"

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
	interfaceType *types.Interface

	requiredFieldName string

	// package to search for the required field and its type
	requiredFieldPackageName string

	requiredFieldTypeName string

	// type of the field required in structs that implement the interface in
	// interfaceType.
	requiredFieldType types.Type

	// slice of "key=value" environment variable assignments to use when
	// loading packages. Examples include:
	// - "GOPATH=/my/path/go"
	// - "GO111MODULE=off"
	envPairs []string
}

// loadPackage loads the package named n using the RequiredFieldInfo r.
func loadPackage(name string, r RequiredFieldInfo) (*packages.Package, error) {
	var env []string
	if r.envPairs != nil {
		env = r.envPairs
	} else {
		env = os.Environ()
	}

	pkg, err := packages.Load(
		&packages.Config{
			Dir: r.workingDir,
			// TODO: Trim down the mode after debugging the test
			Mode: packages.NeedName | packages.NeedFiles |
				packages.NeedSyntax | packages.NeedTypes |
				packages.NeedCompiledGoFiles | packages.NeedDeps |
				packages.NeedEmbedFiles | packages.NeedEmbedPatterns |
				packages.NeedExportFile | packages.NeedExportsFile |
				packages.NeedFiles | packages.NeedImports |
				packages.NeedTypesSizes | packages.NeedTypesInfo,
			Env: env,
		},
		name)

	if err != nil {
		return nil, fmt.Errorf("could not load the package with pattern %v: %v", name, err)
	}

	if len(pkg) != 1 {
		return nil, fmt.Errorf("expected one package named %v, but found %v",
			name,
			len(pkg))
	}

	if len(pkg[0].Errors) > 0 {
		errstr := make([]string, len(pkg[0].Errors))
		for i, e := range pkg[0].Errors {
			errstr[i] = e.Error()
		}
		return nil, fmt.Errorf("encountered errors parsing package %v: %v",
			pkg[0].ID,
			strings.Join(errstr, ", "),
		)
	}

	if pkg[0].TypesInfo == nil || pkg[0].TypesInfo.Defs == nil {
		return nil, fmt.Errorf("found no type information or definitions in package %v", pkg[0].ID)
	}
	return pkg[0], nil
}

// makeAuditEventDeclarationLinter looks up type information in the target
// package, then uses that type information to generate an analysis.Analzer. The
// analysis.Analyzer ensures that all structs that implement a particular
// interface include a field with a specific type.
func makeAuditEventDeclarationLinter(c RequiredFieldInfo) (func(*analysis.Pass) (interface{}, error), error) {

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
		return nil, errors.New("the required field's package name must not be blank")
	}

	if c.requiredFieldTypeName == "" {
		return nil, errors.New("the required field type's name must not be blank")
	}

	// Zero out the types so we can fill them in without worry
	c.interfaceType = nil
	c.requiredFieldType = nil

	// Look up the interface. We look up packages here instead of using the
	// analysis.Pass provided to the analysis.Analyzer function. This is so
	// we don't need to predict the order that the analysis.Analzer walks
	// the package dependency tree.
	pkg, err := loadPackage(c.packageName, c)

	if err != nil {
		return nil, err
	}

	for _, d := range pkg.TypesInfo.Defs {

		if d == nil || d.Type() == nil || d.Type().Underlying() == nil {
			continue
		}

		// Skip any non-interfaces
		i, ok := d.Type().Underlying().(*types.Interface)
		if !ok {
			continue
		}

		if d.Name() == c.interfaceTypeName {
			if c.interfaceType == nil {
				c.interfaceType = i
			} else {
				return nil, fmt.Errorf("expected only one occurrence of interface %v, but got multiple", c.interfaceTypeName)
			}
		}
	}

	// Look up the required field type
	pkg2, err := loadPackage(c.requiredFieldPackageName, c)

	if err != nil {
		return nil, err
	}

	for _, d := range pkg2.TypesInfo.Defs {

		if d == nil || d.Type() == nil {
			continue
		}

		// The Go compiler only allows us to declare a type once per
		// package, so use the first instance of the expected type.
		if d.Name() == c.requiredFieldTypeName && c.requiredFieldType == nil {
			c.requiredFieldType = d.Type()
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
	fn := func(p *analysis.Pass) (interface{}, error) {

		for a, d := range p.TypesInfo.Defs {

			if d == nil {
				continue
			}

			if d.Type() == nil || d.Type().Underlying() == nil {
				continue
			}

			// We're only evaluating type declarations, not, for
			// example, the types of function param declarations.
			if a.Obj == nil || a.Obj.Kind != ast.Typ {
				continue
			}

			t, ok := d.Type().Underlying().(*types.Struct)

			if !ok {
				continue
			}

			if !types.Implements(d.Type(), c.interfaceType) {
				continue
			}

			n := t.NumFields()

			var m bool // Is there a Metadata field with the required type?
			for i := 0; i < n; i++ {
				f := t.Field(i)
				if f.Name() != c.requiredFieldName ||
					// Use the underlying types to check whether the
					// types are identical, otherwise the named type
					// of a field will not be identical to the
					// named type of a type declaration.
					!types.Identical(
						f.Type().Underlying(),
						c.requiredFieldType.Underlying()) {
					continue
				}
				m = true
			}
			if !m {
				// The struct implements the target interface but
				// does not have the required field.
				p.Report(analysis.Diagnostic{
					Pos: a.Pos(),
					Message: fmt.Sprintf(
						"struct type %v implements %v but does not include the field %v of type %v.%v",
						d.Type().String(),
						c.interfaceTypeName,
						c.requiredFieldName,
						c.requiredFieldPackageName,
						c.requiredFieldTypeName,
					),
				})
			}

		}
		return nil, nil
	}

	return fn, nil
}

type analyzerPlugin struct{}

func (a analyzerPlugin) GetAnalyzers() []*analysis.Analyzer {

	pwd, err := os.Getwd()
	if err != nil {
		panic(fmt.Errorf("unable to get current working directory: %v", err))
	}

	fn, err := makeAuditEventDeclarationLinter(
		RequiredFieldInfo{
			workingDir:               pwd,
			packageName:              "github.com/gravitational/teleport/api/types/events",
			interfaceTypeName:        "AuditEvent",
			requiredFieldName:        "Metadata",
			requiredFieldPackageName: "github.com/gravitational/teleport/api/types/events",
			requiredFieldTypeName:    "Metadata",
		})

	if err != nil {
		panic(err)
	}

	var auditEventDeclarationLinter = &analysis.Analyzer{
		Name: "lint-audit-event-declarations",
		Doc:  "ensure that Teleport audit events follow the structure required",
		Run:  fn,
	}

	return []*analysis.Analyzer{
		auditEventDeclarationLinter,
	}
}

var AnalyzerPlugin analyzerPlugin
