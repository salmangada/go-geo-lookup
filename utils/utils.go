package utils

import (
	"bytes"
	"encoding/binary"
	"strconv"
)

func IntToBytes(n int) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, n)
	return buf.Bytes()
}

func GenerateUniqueKey(latitude, longitude int) string {

	key := (latitude << 16) + longitude
	return strconv.Itoa(key)
}
