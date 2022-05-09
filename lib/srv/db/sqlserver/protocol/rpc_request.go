package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"

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

	var headersLength uint32
	if err := binary.Read(bytes.NewReader(data), binary.LittleEndian, &headersLength); err != nil {
		return nil, trace.Wrap(err)
	}

	if len(data) < int(headersLength+2) {
		return nil, trace.BadParameter("data invalid size")
	}

	var length uint16

	if headersLength > headersLength+2 {
		return nil, trace.BadParameter("data invalid size")
	}
	if err := binary.Read(bytes.NewReader(data[headersLength:headersLength+2]), binary.LittleEndian, &length); err != nil {
		return nil, trace.Wrap(err)
	}

	if length != 0xFFFF {
		if len(data) < 2*int(length) {
			return nil, trace.BadParameter("bad parameter")
		}
		procName, err := readUcs2(bytes.NewReader(data[headersLength+2:]), 2*int(length))
		if err != nil {
			return nil, trace.Wrap(err)
		}
		return &RPCRequest{
			Packet:   p,
			ProcName: procName,
		}, nil
	}

	var procID uint16
	if err := binary.Read(bytes.NewReader(data[headersLength+2:headersLength+4]), binary.LittleEndian, &procID); err != nil {
		return nil, trace.Wrap(err)
	}

	fmt.Println(hex.Dump(data[headersLength:]))
	kk := data[headersLength+2:]
	rr := bytes.NewReader(kk)

	var flags uint16
	if err := binary.Read(rr, binary.LittleEndian, &flags); err != nil {
		return nil, trace.Wrap(err)
	}

	fmt.Println(hex.Dump(kk[2:]))
	i := 6
	if len(kk) < 6 {
		return nil, trace.BadParameter("bad parameter")
	}
	fmt.Println(hex.Dump(kk[i:]))

	tds := mssql.NewTdsBuffer(kk[i:], len(kk[i:]))
	ti := mssql.ReadTypeInfo(tds)
	val := ti.Reader(&ti, tds)

	return &RPCRequest{
		Packet: p,
		Query:  fmt.Sprintf("%v", val),
	}, nil
}

type Collation struct {
	LcidAndFlags uint32
	SortId       uint8
}

type TypeInfo struct {
	NameLength  uint8
	FlagsParam  uint8
	T           uint8
	MaxLength   uint16
	Collocation uint32
	SortID      uint8
}

type RPCContent struct {
	IDSwitch uint16
	Flags    uint16
}
