package main

import (
	"go/parser"
	"go/token"
	"reflect"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestCheckMetadataInAuditEventImplementations(t *testing.T) {

	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"my-project/events/events.go": `package events

import "fmt"

// NewConnectionEvent is emitted when there is a new connection
var NewConnectionEvent string = "connection.new"

type Metadata struct {
  Type string
}

type AuditEvent interface{
  GetType() string
}

func Emit(e AuditEvent){
  fmt.Println(e.GetType())
}

		    `,
		"my-project/goodimpl/goodimpl.go": `package goodimpl

import "my-project/events"

type GoodAuditEventImplementation struct{
  Metadata events.Metadata
}

func (g GoodAuditEventImplementation) GetType() string{
  return g.Metadata.Type
}
		    `,

		"my-project/badimpl/badimpl.go": `package badimpl

type BadAuditEventImplementation struct{ // want "struct type my-project/badimpl.BadAuditEventImplementation implements AuditEvent but does not include the field Metadata of type my-project/events.Metadata"

  Type string
}

func (b BadAuditEventImplementation) GetType() string{
  return b.Type
}
`,
		"my-project/badmetadata/badmetadata.go": `package badmetadata

import (
  "my-project/events"
  "my-project/goodimpl"
)

func EmitAuditEvent(){
  
    events.Emit(goodimpl.GoodAuditEventImplementation{
        Metadata: events.Metadata{}, // want "Metadata struct does not specify a Type field"
    })
}
`,

		"my-project/main.go": `package main

import (
  "my-project/badimpl"
  "my-project/goodimpl"
  "my-project/events"
)

func main(){

    events.Emit(goodimpl.GoodAuditEventImplementation{
      Metadata: events.Metadata{
	Type: events.NewConnectionEvent,
      },
    })

    events.Emit(badimpl.BadAuditEventImplementation{
      Type: "bad audit event",
    })
}
`,
	})

	defer cleanup()

	if err != nil {
		t.Fatalf("could not write test files: %v", err)
	}

	cache := t.TempDir()

	fn, err := makeAuditEventDeclarationLinter(
		RequiredFieldInfo{
			workingDir:               dir,
			packageName:              "my-project/events",
			interfaceTypeName:        "AuditEvent",
			requiredFieldName:        "Metadata",
			requiredFieldPackageName: "my-project/events",
			requiredFieldTypeName:    "Metadata",
			envPairs: []string{
				"GOPATH=" + dir,
				"GO111MODULE=off",
				"GOCACHE=" + cache,
			},
		})

	if err != nil {
		t.Fatal(err)
	}

	var f valueIdentifierFact

	var auditEventDeclarationLinter = &analysis.Analyzer{
		Name:      "lint-audit-event-declarations",
		Doc:       "ensure that Teleport audit events follow the structure required",
		Run:       fn,
		FactTypes: []analysis.Fact{&f},
	}

	analysistest.Run(
		t,
		dir,
		auditEventDeclarationLinter,
		"./...",
	)

}

func TestCheckValuesOfRequiredFields(t *testing.T) {

	cases := []struct {
		description        string
		file               string
		expectedDiagnostic analysis.Diagnostic
		expectedFacts      []valueIdentifierFact
	}{
		{
			description: "Correct use of Metadata",
			file: `package goodmetadata

import (
  "my-project/events"
  "my-project/goodimpl"
)

func EmitAuditEvent(){
  
    events.Emit(goodimpl.AuditEventImplementation{
        Metadata: events.Metadata{
           Name: "My Metadata",
	   Type: auditEventEmitted,
	},
    })
}
`,
			expectedDiagnostic: analysis.Diagnostic{},
			expectedFacts: []valueIdentifierFact{
				valueIdentifierFact("auditEventEmitted"),
			},
		},
		{
			description: "Metadata with missing desired field",
			file: `package badmetadata

import (
  "my-project/events"
  "my-project/goodimpl"
)

func EmitAuditEvent(){
  
    events.Emit(goodimpl.AuditEventImplementation{
        Metadata: events.Metadata{
           Name: "My Metadata",
	},
    })
}
`,
			expectedDiagnostic: analysis.Diagnostic{
				Pos:     174,
				Message: "required field Type is missing in a declaration of my-project/events.Metadata",
			},
		},
		{
			description: "Metadata with empty string literal desired field",
			file: `package badmetadata

import (
  "my-project/events"
  "my-project/goodimpl"
)

func EmitAuditEvent(){
  
    events.Emit(goodimpl.GoodAuditEventImplementation{
        Metadata: events.Metadata{
           Name: "My Metadata",
	   Type: "",
	},
    })
}
`,
			expectedDiagnostic: analysis.Diagnostic{
				Pos:     178,
				Message: "the field Type in composite literal my-project/events.Metadata must have a value that is a variable or constant",
			},
		},
		{
			description: "Metadata with nonempty string literal desired field",
			file: `package badmetadata

import (
  "my-project/events"
  "my-project/goodimpl"
)

func EmitAuditEvent(){
  
    events.Emit(goodimpl.GoodAuditEventImplementation{
        Metadata: events.Metadata{
           Name: "My Metadata",
	   Type: "auditEventType",
	},
    })
}
`,
			expectedDiagnostic: analysis.Diagnostic{
				Pos:     178,
				Message: "the field Type in composite literal my-project/events.Metadata must have a value that is a variable or constant",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.description, func(t *testing.T) {

			fset := token.FileSet{}

			i := RequiredFieldInfo{
				workingDir:                  "",
				packageName:                 "my-project/events",
				interfaceTypeName:           "AuditEvent",
				requiredFieldName:           "Metadata",
				requiredFieldPackageName:    "my-project/events",
				requiredFieldTypeName:       "Metadata",
				envPairs:                    []string{},
				fieldTypeMustPopulateFields: []string{"Type"},
			}
			f, err := parser.ParseFile(&fset, "badmetadata.go", c.file, parser.ParseComments)

			if err != nil {
				t.Fatalf("unexpected error parsing the fixture: %v", err)
			}

			d, s := checkValuesOfRequiredFields(i, f)
			if !reflect.DeepEqual(d, c.expectedDiagnostic) {
				t.Fatalf("expected to receive diagnostic: %+v\nbut got: %+v", c.expectedDiagnostic, d)
			}
			if c.expectedFacts != nil {
				var actualFacts []valueIdentifierFact
				for _, fact := range s {
					actualFacts = append(actualFacts, *fact)
				}
				if !reflect.DeepEqual(c.expectedFacts, actualFacts) {
					t.Fatalf("expected facts: %v\ngot: %v", c.expectedFacts, actualFacts)
				}
			}
		})
	}
}
