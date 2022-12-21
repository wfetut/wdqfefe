package main

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/packages/packagestest"
)

func TestCheckMetadataInAuditEventImplementations(t *testing.T) {

	e := packagestest.Export(t, packagestest.GOPATH, []packagestest.Module{{
		Name: "example.com/fakemod",
		Files: map[string]interface{}{
			"events/events.go": `package events
type AuditEvent interface{
  GetType() string
}

		    `,
			"goodimpl/goodimpl.go": `package goodimpl
type GoodAuditEventImplementation struct{
  Type string
  Metadata string
}

func (g GoodAuditEventImplementation) GetType() string{
  return g.Type
}
		    `,

			"badimpl/badimpl.go": `package badimpl
type BadAuditEventImplementation struct{
  Type string
}

func (b BadAuditEventImplementation) GetType() string{
  return b.Type
}
`,
		}},
	})
	defer e.Cleanup()

	p, err := packages.Load(
		e.Config,
		filepath.Dir(e.File("example.com/fakemod", "events/events.go")),
		filepath.Dir(e.File("example.com/fakemod", "goodimpl/goodimpl.go")),
		filepath.Dir(e.File("example.com/fakemod", "badimpl/badimpl.go")),
	)
	if err != nil {
		t.Fatalf("failed to load the test packages: %v", err)
	}

	if err := checkMetadataInAuditEventImplementations(p[0].Syntax, p[0].TypesInfo); err == nil || !strings.Contains(err.Error(), "Metadata") {
		t.Fatalf("expected the error message to mention \"Metadata\", but got: %v", err)
	}

}
