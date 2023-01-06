package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
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
	requiredFieldType *types.Struct

	// names of the fields within requiredFieldType that must be populated
	fieldTypeMustPopulateFields []string

	// slice of "key=value" environment variable assignments to use when
	// loading packages. Examples include:
	// - "GOPATH=/my/path/go"
	// - "GO111MODULE=off"
	envPairs []string
}

// An identifier used to provide the value of a struct field
type valueIdentifierFact string

func (f *valueIdentifierFact) String() string {
	return string(*f)
}

// Required to implement analysis.Fact
func (*valueIdentifierFact) AFact() {}

// checkValuesOfRequiredFields traverses the children of n and ensures that any
// values of the required field type populate certain required fields, specified
// in i.fieldTypeMustPopulateFields.
//
// checkValuesOfRequiredFields acts on an AST with no type information, so
// callers will need to ensure that n includes a declaration of a struct and the
// struct has the required type.
//
// The return values are:
// - an analysis.Diagnostic indicating the first error encountered while
//   checking field values
// - a slice of valueIdentifierFact representing the identifiers used as values
//   for the required fields.
func checkValuesOfRequiredFields(i RequiredFieldInfo, n ast.Node) (analysis.Diagnostic, []*valueIdentifierFact) {

	// We'll use this to determine whether all declarations of the target struct
	// include all required fields. Keys are the selector expressions that
	// declare a struct with the required type. Each value is a map where
	// keys are strings representing field names and values are the original
	// KeyValueExprs in the struct correspondign to those names.
	targetFields := make(map[*ast.SelectorExpr]map[string]*ast.KeyValueExpr)

	astutil.Apply(n, func(c *astutil.Cursor) bool {

		// We're checking a field, so it must have a parent
		if c.Parent() == nil {
			return true
		}

		l, ok := c.Parent().(*ast.CompositeLit)

		// The parent must be a struct, which is a composite literal
		if !ok {
			return true
		}

		s, ok := l.Type.(*ast.SelectorExpr)
		// The parent's type must be a selector expression, e.g., events.Metadata
		if !ok {
			return true
		}

		// This composite literal doesn't have the name of the required
		// field, so skip it.
		if s.Sel.Name != i.requiredFieldTypeName {
			return true
		}

		kv, ok := c.Node().(*ast.KeyValueExpr)
		// The node is not a key/value expression like:
		//
		// { Type: myValue, }
		if !ok {
			return true
		}

		// Now that we know that the cursor's Node is a key/value
		// expression, add the parent to the map so we can ensure later
		// that the parent has all required fields.
		if _, ok := targetFields[s]; !ok {
			targetFields[s] = make(map[string]*ast.KeyValueExpr)
		}

		id, ok := kv.Key.(*ast.Ident)

		// The key isn't an identifier for some reason, so skip it
		if !ok {
			return true
		}

		targetFields[s][id.Name] = kv

		return true
	}, nil)

	var facts []*valueIdentifierFact

	// Range through each struct in targetFields. For each struct, range
	// through the fields we expect to be populated and ensure that those
	// fields are present and populated within the struct.
	for c, m := range targetFields {
		for _, e := range i.fieldTypeMustPopulateFields {

			kv, ok := m[e]

			if !ok {

				return analysis.Diagnostic{
					Pos: c.Pos(),
					Message: fmt.Sprintf(
						"required field %v is missing in a declaration of %v.%v",
						e,
						i.requiredFieldPackageName,
						i.requiredFieldTypeName,
					),
				}, nil

			}

			id, ok := kv.Value.(*ast.Ident)

			if !ok {
				return analysis.Diagnostic{
					Pos: c.Pos(),
					Message: fmt.Sprintf(
						"the field %v in composite literal %v.%v must have a value that is a variable or constant",
						e,
						i.requiredFieldPackageName,
						i.requiredFieldTypeName,
					),
				}, nil

			}

			fact := valueIdentifierFact(id.Name)

			facts = append(facts, &fact)

		}
	}

	return analysis.Diagnostic{}, facts
}

// loadPackage loads the package named n using the PrintfRequiredFieldInfo r.
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
			Mode: packages.NeedName | packages.NeedFiles |
				packages.NeedCompiledGoFiles | packages.NeedImports |
				packages.NeedTypes | packages.NeedTypesSizes |
				packages.NeedSyntax | packages.NeedTypesInfo |
				packages.NeedDeps,
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
			s, ok := d.Type().Underlying().(*types.Struct)
			if !ok {
				return nil, fmt.Errorf("required field type %v is not a struct", c.requiredFieldTypeName)
			}
			c.requiredFieldType = s
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

		// Check each type definition in the package for correct
		// implementations of the target interface
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

	var f valueIdentifierFact

	var auditEventDeclarationLinter = &analysis.Analyzer{
		Name: "lint-audit-event-declarations",
		Doc:  "ensure that Teleport audit events follow the structure required",
		Run:  fn,
		FactTypes: []analysis.Fact{
			&f,
		},
	}

	return []*analysis.Analyzer{
		auditEventDeclarationLinter,
	}
}

var AnalyzerPlugin analyzerPlugin
