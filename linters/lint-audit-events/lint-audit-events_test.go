package main

import (
	"fmt"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestCheckMetadataInAuditEventImplementations(t *testing.T) {

	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"my-project/events/events.go": `package events

import "fmt"

type Metadata struct {
  Name string
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
  Type string
  Metadata events.Metadata
}

func (g GoodAuditEventImplementation) GetType() string{
  return g.Type
}
		    `,

		"my-project/badimpl/badimpl.go": `package badimpl

// want "Event implementation does not include a Metadata field"
type BadAuditEventImplementation struct{
  Type string
}

func (b BadAuditEventImplementation) GetType() string{
  return b.Type
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
      Type: "good audit event",
      Metadata: events.Metadata{
	Name: "my metadata",
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

	var auditEventDeclarationLinter = &analysis.Analyzer{
		Name: "lint-audit-event-declarations",
		Doc:  "ensure that Teleport audit events follow the structure required",
		Run:  fn,
	}

	res := analysistest.Run(
		t,
		dir,
		auditEventDeclarationLinter,
		"./...",
	)

	for _, r := range res {
		for _, d := range r.Diagnostics {
			fmt.Println(d)
		}
	}

}
