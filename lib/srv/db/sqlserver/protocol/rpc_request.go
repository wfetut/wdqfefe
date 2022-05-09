package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	mssql "github.com/denisenkom/go-mssqldb"
	"github.com/gravitational/trace"
)

type RPCRequest struct {
	Packet
	ProcName string
	Query    string
}

type PP struct {
	*bytes.Buffer
}

func (k *PP) Close() error {
	return nil
}

func toRPCRequest(p Packet) (*RPCRequest, error) {
	if p.Type() != PacketTypeRPCRequest {
		return nil, trace.BadParameter("expected SQLBatch packet, got: %#v", p.Type())
	}
	data := p.Data()
	r := bytes.NewReader(p.Data())

	var headersLength uint32
	if err := binary.Read(r, binary.LittleEndian, &headersLength); err != nil {
		return nil, trace.Wrap(err)
	}

	if _, err := r.Seek(int64(headersLength), io.SeekStart); err != nil {
		return nil, trace.ConvertSystemError(err)
	}

	var length uint16
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return nil, trace.Wrap(err)
	}

	if length != 0xFFFF {
		procName, err := readUcs2(r, 2*int(length))
		if err != nil {
			return nil, trace.Wrap(err)
		}
		return &RPCRequest{
			Packet:   p,
			ProcName: procName,
		}, nil
	}

	var procID uint16
	if err := binary.Read(r, binary.LittleEndian, &procID); err != nil {
		return nil, trace.Wrap(err)
	}

	var flags uint16
	if err := binary.Read(r, binary.LittleEndian, &flags); err != nil {
		return nil, trace.Wrap(err)
	}

	if _, err := r.Seek(2, io.SeekCurrent); err != nil {
		return nil, trace.ConvertSystemError(err)
	}

	tds := mssql.NewTdsBuffer(data[int(r.Size())-r.Len():], r.Len())
	ti := mssql.ReadTypeInfo(tds)
	val := ti.Reader(&ti, tds)

	return &RPCRequest{
		Packet: p,
		Query:  fmt.Sprintf("%v", val),
	}, nil
}
