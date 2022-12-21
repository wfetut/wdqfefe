package main

import (
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/packages/packagestest"
)

func TestCheckMetadataInAuditEventImplementations(t *testing.T) {

	e := packagestest.Export(t, packagestest.GOPATH, []packagestest.Module{})
	defer e.Cleanup()

	p, err := packages.Load(e.Config, filepath.Dir(e.File("fakemod", "a/a.go")))
	if err != nil {
		t.Fatalf("failed to load the test packages: %v", err)
	}

	checkMetadataInAuditEventImplementations(p[0].Syntax, p[0].TypesInfo)

}
