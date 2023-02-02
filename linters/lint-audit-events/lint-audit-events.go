package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
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
	requiredFieldType types.Type

	// names of the fields within requiredFieldType that must be populated
	fieldTypeMustPopulateFields []string

	// slice of "key=value" environment variable assignments to use when
	// loading packages. Examples include:
	// - "GOPATH=/my/path/go"
	// - "GO111MODULE=off"
	envPairs []string
}

// A value spec declaration found in another package
type valueSpecFact struct {
	// The first name found for the value spec
	name string
	// The text of the value spec's godoc
	doc string
	// Position where the ValueSpec was declared. Used for reporting
	// diagnostics.
	pos token.Pos
	// The name of the packae where the value spec originated.
	pkg string
}

// newValueSpecFact generates a *valueIdentifierFact using the *ast.GenSpec
// provided in s and the package name provided in in. This makes it possible for
// one pass of the analyzer to look up value specs declared in other passes.
// This assumes that the *ast.GenDecl has a single *ast.ValueSpec, and returns
// an error otherwise.
func newValueSpecFact(n string, s *ast.GenDecl) (*valueSpecFact, error) {

	if len(s.Specs) != 1 {
		return nil, errors.New("expected a GenDecl with a single ValueSpec, but got multiple")
	}

	vs, ok := s.Specs[0].(*ast.ValueSpec)

	if !ok {
		return nil, errors.New("the GenDecl does not contain a ValueSpec")
	}

	var m string
	if len(vs.Names) > 0 {
		m = vs.Names[0].Name
	}

	return &valueSpecFact{
		name: m,
		doc:  s.Doc.Text(),
		pos:  s.Pos(),
		pkg:  n,
	}, nil
}

func (f *valueSpecFact) String() string {
	return f.name
}

// Required to implement analysis.Fact
func (*valueSpecFact) AFact() {}

// checkValuesOfRequiredFields traverses the children of n and ensures that any
// values of the required field type populate certain required fields, specified
// in i.fieldTypeMustPopulateFields.
//
// It also makes sure that any values of the required field type are identifiers
// (rather than, say, string literals) that refer to a value spec, and that the
// value spec has a godoc. It uses the provided map[string]*valueSpecFact to
// look up value specs by the name of the identifier.

// The return value is an analysis.Diagnostic indicating the first error
// encountered while checking field values.
func checkValuesOfRequiredFields(ti *types.Info, i RequiredFieldInfo, n ast.Node, vsm map[string]*valueSpecFact) analysis.Diagnostic {

	var diag analysis.Diagnostic

	astutil.Apply(n, func(c *astutil.Cursor) bool {

		expr, ok := c.Node().(ast.Expr)

		// We're only looking at expressions
		if !ok {
			return true
		}

		typ := ti.TypeOf(expr)

		if typ == nil {
			return true
		}

		// This is a dirty hack, but we can't use types.Identical or the
		// "==" operator with the required field type and the given
		// expression (typ). This is because these operate on pointers,
		// and i.requiredFieldType and typ exist at different locations
		// in memory, since we assigned i.requiredFieldType before
		// generating the Analyzer function.
		if typ.String() != i.requiredFieldType.String() {
			return true
		}

		l, ok := c.Node().(*ast.CompositeLit)

		// The cursor's node must be a struct, which is a composite
		// literal
		if !ok {
			return true
		}

		targetFields := make(map[string]*ast.KeyValueExpr)

		for _, e := range l.Elts {
			kv, ok := e.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			id, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			targetFields[id.Name] = kv
		}

		for _, e := range i.fieldTypeMustPopulateFields {

			kv, ok := targetFields[e]

			if !ok {
				diag = analysis.Diagnostic{
					Pos: c.Node().Pos(),
					Message: fmt.Sprintf(
						"required field %v is missing in a declaration of %v.%v",
						e,
						i.requiredFieldPackageName,
						i.requiredFieldTypeName,
					),
				}
				return false
			}

			// We've found a field with the required key, so we'll
			// check the type.
			var id *ast.Ident
			switch t := kv.Value.(type) {
			case *ast.Ident:
				id = t
			case *ast.SelectorExpr:
				id = t.Sel
			default:
				diag = analysis.Diagnostic{
					Pos: c.Node().Pos(),
					Message: fmt.Sprintf(
						"the field %v in composite literal %v.%v must have a value that is a variable or constant",
						e,
						i.requiredFieldPackageName,
						i.requiredFieldTypeName,
					),
				}

				return false
			}

			// Now that we know that the required field's value is a
			// variable or constant, look up the identifier's value
			// spec to see if it is properly formatted.
			vs, ok := vsm[id.Name]

			// analysis.Analyzers look up packages in a dependency
			// tree from leaf to root, so any value specs we use as
			// the values of the reqiured fields must already have
			// been exported as package facts.
			if !ok {
				diag = analysis.Diagnostic{
					Pos: c.Node().Pos(),
					Message: fmt.Sprintf(
						"the value of field %v in composite literal %v.%v is an identifier that isn't declared anywhere",
						e,
						i.requiredFieldPackageName,
						i.requiredFieldTypeName,
					),
				}

				return false
			}

			if vs.doc == "" {
				diag = analysis.Diagnostic{
					Pos: vs.pos,
					Message: fmt.Sprintf(
						"%v.%v needs a comment since it is used when emitting an audit event",
						vs.pkg,
						vs.name,
					),
				}

				return false
			}

			// TODO: Check that the doc begins with the name of the
			// identifier

		}

		return true
	}, nil)

	return diag
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

		// Check if this package declares any value specs and export these as facts
		for _, f := range p.Files {

			astutil.Apply(f, func(r *astutil.Cursor) bool {
				gd, ok := r.Node().(*ast.GenDecl)
				if !ok {
					return true
				}
				vf, err := newValueSpecFact(p.Pkg.Path(), gd)

				// This GenDecl cannot be a valueSpecFact, so
				// try the next node
				if err != nil {
					return true
				}
				p.ExportPackageFact(vf)

				return true

			}, nil)

		}

		if strings.Contains(p.Pkg.Path(), "my-project") {
			fmt.Println("current package name: ", p.Pkg.Name())
		}

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

		vsm := make(map[string]*valueSpecFact)

		for _, fact := range p.AllPackageFacts() {
			if vs, ok := fact.Fact.(*valueSpecFact); ok {
				vsm[vs.name] = vs
			}
		}

		for _, f := range p.Files {
			d := checkValuesOfRequiredFields(p.TypesInfo, c, f, vsm)
			if d.Message != "" {
				p.Report(d)
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
			fieldTypeMustPopulateFields: []string{
				"Type",
			},
		})

	if err != nil {
		panic(err)
	}

	var f valueSpecFact

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
