package main

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestCheckMetadataInAuditEventImplementations(t *testing.T) {

	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"events/events.go": `package events

type Metadata struct {
  Name string
}

type AuditEvent interface{
  GetType() string
}

		    `,
		"goodimpl/goodimpl.go": `package goodimpl
type GoodAuditEventImplementation struct{
  Type string
  Metadata Metadata
}

func (g GoodAuditEventImplementation) GetType() string{
  return g.Type
}
		    `,

		"badimpl/badimpl.go": `package badimpl

// want "Event implementation does not include a Metadata field"
type BadAuditEventImplementation struct{
  Type string
}

func (b BadAuditEventImplementation) GetType() string{
  return b.Type
}
`})

	defer cleanup()

	if err != nil {
		t.Fatalf("could not write test files: %v", err)
	}

	analysistest.Run(
		t,
		dir,
		auditEventDeclarationLinter,
		"events", "goodimpl", "badimpl")

}
