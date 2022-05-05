package protocol

import (
	"bytes"
	"encoding/binary"

	"github.com/gravitational/trace"
)

type RPCRequest struct {
	Packet
	Query string
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

	if int(headersLength+2) >= len(data) {
		return nil, trace.BadParameter("data invalid size")
	}

	kk := data[headersLength+2:]
	rr := bytes.NewReader(kk)

	var rpcContent RPCContent
	if err := binary.Read(rr, binary.LittleEndian, &rpcContent); err != nil {
		return nil, trace.Wrap(err)
	}

	var typeInfo TypeInfo
	if err := binary.Read(rr, binary.LittleEndian, &typeInfo); err != nil {
		return nil, trace.Wrap(err)
	}
	s, err := readBVarChar(rr)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return &RPCRequest{
		Packet: p,
		Query:  s,
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
