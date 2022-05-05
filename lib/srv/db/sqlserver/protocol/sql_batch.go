package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	mssql "github.com/denisenkom/go-mssqldb"
	"github.com/gravitational/trace"
)

var once sync.Once
var pkgWritterCounter uint32
var dir string

func DumpToFile(p Packet) error {
	topDIR := os.Getenv("SQLSERVER_DUMP_PATH")
	if topDIR == "" {
		return nil

	}
	once.Do(func() {
		dir = fmt.Sprintf("%s/%d", topDIR, time.Now().Nanosecond())
		err := os.MkdirAll(dir, 0700)
		if err != nil {
			panic(err)
		}
	})

	n := atomic.AddUint32(&pkgWritterCounter, 1)
	name := fmt.Sprintf("%d_packet.bin", n)
	err := os.WriteFile(filepath.Join(dir, name), p.Bytes(), 0700)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

type SQLBatch struct {
	Packet
	SQLText string
}

func toSQLBatch(p Packet) (*SQLBatch, error) {
	if p.Type() != PacketTypeSQLBatch {
		return nil, trace.BadParameter("expected SQLBatch packet, got: %#v", p.Type())
	}

	var headersLength uint32
	if int(p.Header().PacketID) != 1 {
		headersLength = 0
	} else {
		if err := binary.Read(bytes.NewReader(p.Data()), binary.LittleEndian, &headersLength); err != nil {
			return nil, trace.Wrap(err)
		}
	}

	if int(headersLength) > len(p.Data()) {
		// TODO debug this case
		if err := DumpToFile(p); err != nil {
			fmt.Println(err)
		}
		return nil, trace.BadParameter("invalid headersLength size")

	}

	s, err := mssql.ParseUCS2String(p.Data()[headersLength:])
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return &SQLBatch{
		Packet:  p,
		SQLText: s,
	}, nil
}
