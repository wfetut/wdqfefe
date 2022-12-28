package main

import (
	"testing"

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

type GoodAuditEventImplementation struct{
  Type string
  Metadata Metadata
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

  "badimpl"
  "goodimpl"
  "events"

events.Emit(goodimpl.GoodAuditEventImplementation{
  Type: "good audit event",
  Metadata: events.Metadata{
    Name: "my metadata",
  },
})

events.Emit(badimpl.BadAuditEventImplementation{
  Type: "bad audit event",
})
`,
	})

	defer cleanup()

	if err != nil {
		t.Fatalf("could not write test files: %v", err)
	}

	analysistest.Run(
		t,
		dir,
		auditEventDeclarationLinter,
	)

}
