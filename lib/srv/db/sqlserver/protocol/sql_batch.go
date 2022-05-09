package protocol

import (
	"bytes"
	"encoding/binary"
	"io"

	mssql "github.com/denisenkom/go-mssqldb"
	"github.com/gravitational/trace"
)

type SQLBatch struct {
	Packet
	SQLText string
}

func toSQLBatch(p Packet) (*SQLBatch, error) {
	if p.Type() != PacketTypeSQLBatch {
		return nil, trace.BadParameter("expected SQLBatch packet, got: %#v", p.Type())
	}
	r := bytes.NewReader(p.Data())

	var headersLength uint32
	if int(p.Header().PacketID) == 1 {
		if err := binary.Read(r, binary.LittleEndian, &headersLength); err != nil {
			return nil, trace.Wrap(err)
		}
	}
	if _, err := r.Seek(int64(headersLength), io.SeekStart); err != nil {
		return nil, trace.ConvertSystemError(err)
	}

	s, err := mssql.ParseUCS2String(p.Data()[r.Size()-int64(r.Len()):])
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &SQLBatch{
		Packet:  p,
		SQLText: s,
	}, nil
}
