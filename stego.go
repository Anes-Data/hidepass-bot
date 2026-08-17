package main

import (
	"bytes"
	"encoding/binary"
	"errors"
)

var marker = []byte("::STEGO_BOT_DATA::")

const maxEmbeddedDataSize = MaxFileSize

func embedData(fileData []byte, encryptedData []byte) []byte {
	var buf bytes.Buffer
	buf.Write(fileData)
	buf.Write(encryptedData)

	dataLen := uint32(len(encryptedData))
	lenBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBytes, dataLen)

	buf.Write(lenBytes)
	buf.Write(marker)

	return buf.Bytes()
}

func extractData(fileData []byte) ([]byte, error) {
	markerLen := len(marker)
	if len(fileData) < markerLen+4 {
		return nil, errors.New("file is too small to contain embedded payload")
	}

	if !bytes.HasSuffix(fileData, marker) {
		return nil, errors.New("no hidden data payload found in this file")
	}

	dataWithoutMarker := fileData[:len(fileData)-markerLen]

	if len(dataWithoutMarker) < 4 {
		return nil, errors.New("invalid stego payload structure")
	}

	dataLenPos := len(dataWithoutMarker) - 4
	dataLen := binary.BigEndian.Uint32(dataWithoutMarker[dataLenPos:])

	if dataLen == 0 || dataLen > maxEmbeddedDataSize {
		return nil, errors.New("corrupted or tampered stego payload size")
	}

	if dataLen > uint32(dataLenPos) {
		return nil, errors.New("corrupted or tampered stego payload structure")
	}

	encryptedDataPos := dataLenPos - int(dataLen)
	encryptedData := dataWithoutMarker[encryptedDataPos:dataLenPos]

	return encryptedData, nil
}