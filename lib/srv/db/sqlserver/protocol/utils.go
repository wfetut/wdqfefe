package protocol

import (
	"encoding/binary"
	"io"

	mssql "github.com/denisenkom/go-mssqldb"
)

func readBVarChar(r io.Reader) (res string, err error) {
	var numchars uint16
	if err := binary.Read(r, binary.LittleEndian, &numchars); err != nil {
		return "", err
	}
	if numchars == 0 {
		return "", nil
	}
	return readUcs2(r, int(numchars))
}

func readUcs2(r io.Reader, numchars int) (res string, err error) {
	buf := make([]byte, numchars)
	_, err = io.ReadFull(r, buf)
	if err != nil {
		return "", err
	}
	return mssql.ParseUCS2String(buf)
}
