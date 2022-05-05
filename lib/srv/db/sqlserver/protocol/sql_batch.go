package protocol

import (
	"bytes"
	"encoding/binary"

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

	var headersLength uint32
	if int(p.Header().PacketID) != 1 {
		headersLength = 0
	} else {
		if err := binary.Read(bytes.NewReader(p.Data()), binary.LittleEndian, &headersLength); err != nil {
			return nil, trace.Wrap(err)
		}
	}

	if int(headersLength) > len(p.Data()) {
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
