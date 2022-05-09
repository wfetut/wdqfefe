package protocol

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLBatchWalk(t *testing.T) {
	filepath.WalkDir("/Users/marek/packets", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_pkg.bin") {
			return nil

		}
		buff, err := os.ReadFile(path)
		require.NoError(t, err)

		p, err := ReadPacket(bytes.NewReader(buff))
		require.NoError(t, err)
		r, err := ConvPacket(p)
		require.NoError(t, err)
		switch o := r.(type) {
		case *SQLBatch:
			o = o
			require.NotEmpty(t, o.SQLText)
			t.Log(d.Name())
			t.Log(o.SQLText)
			return nil
		case *RPCRequest:
			require.NotEmpty(t, o.Query)
			t.Log(d.Name())
			t.Log(o.Query)
			return nil
		}
		return nil
	})
}

func TestRPCRequestWalk(t *testing.T) {
	filepath.WalkDir("/Users/marek/packetsrpc/", func(path string, d fs.DirEntry, err error) error {
		if false {
			if d.Name() != "11_pkg.bin" {
				return nil
			}
		}

		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_pkg.bin") {
			return nil

		}
		buff, err := os.ReadFile(path)
		require.NoError(t, err)

		p, err := ReadPacket(bytes.NewReader(buff))
		require.NoError(t, err)
		r, err := ConvPacket(p)
		require.NoError(t, err)
		switch o := r.(type) {
		case *SQLBatch:
			require.NotEmpty(t, o.SQLText)
			t.Log(d.Name())
			t.Log(o.SQLText)
			return nil

		case *RPCRequest:
			require.NotEmpty(t, o.Query)
			t.Log(d.Name())
			t.Log(o.Query)
			return nil
		}
		return nil
	})
}
