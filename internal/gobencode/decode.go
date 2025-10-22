package gobencode

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
)

var ErrMalformedData = errors.New("gobencode: malformed data")

func Decode(r io.Reader) (interface{}, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return decodeRecursive(br)
}

func decodeRecursive(br *bufio.Reader) (interface{}, error) {
	firstByte, err := br.ReadByte()
	if err != nil {
		if err == io.EOF {

			return nil, fmt.Errorf("%w: unexpected EOF while expecting a value", ErrMalformedData)
		}
		return nil, err
	}

	switch {
	case firstByte == 'i':
		return parseInteger(br)
	case firstByte == 'l':
		return parseList(br)
		return nil, fmt.Errorf("gobencode: list parsing not yet implemented")
	case firstByte == 'd':
		return parseDictionary(br)
		return nil, fmt.Errorf("gobencode: dictionary parsing not yet implemented")
	case firstByte >= '0' && firstByte <= '9':
		if err := br.UnreadByte(); err != nil {
			return nil, err
		}
		return parseString(br)
	default:
		return nil, fmt.Errorf("%w: unexpected token '%c' (0x%x) at start of value", ErrMalformedData, firstByte, firstByte)
	}
}

func parseInteger(br *bufio.Reader) (int64, error) {
	var numBytes []byte
	for {
		b, err := br.ReadByte()
		if err != nil {
			if err == io.EOF {
				return 0, fmt.Errorf("%w: unexpected EOF while parsing integer", ErrMalformedData)
			}
			return 0, err
		}

		if b == 'e' {
			if len(numBytes) == 0 {
				return 0, fmt.Errorf("%w: integer 'ie' is invalid", ErrMalformedData)
			}
			break
		}
		numBytes = append(numBytes, b)
	}

	numStr := string(numBytes)

	if len(numStr) > 1 && numStr[0] == '0' {
		return 0, fmt.Errorf("%w: invalid integer format (leading zero for non-zero number '%s')", ErrMalformedData, numStr)
	}
	if len(numStr) > 2 && numStr[0] == '-' && numStr[1] == '0' {
		return 0, fmt.Errorf("%w: invalid integer format (leading zero for negative number '%s')", ErrMalformedData, numStr)
	}
	if numStr == "-" {
		return 0, fmt.Errorf("%w: invalid integer format (just a hyphen)", ErrMalformedData)
	}

	val, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: could not parse integer string '%s': %v", ErrMalformedData, numStr, err)
	}
	return val, nil
}

func parseString(br *bufio.Reader) (string, error) {
	var lenBytes []byte
	for {
		b, err := br.ReadByte()
		if err != nil {
			if err == io.EOF {
				return "", fmt.Errorf("%w: unexpected EOF while parsing string length", ErrMalformedData)
			}
			return "", err
		}

		if b == ':' {
			if len(lenBytes) == 0 {
				return "", fmt.Errorf("%w: string length delimiter ':' found without preceding digits", ErrMalformedData)
			}
			break
		}

		if b < '0' || b > '9' {
			return "", fmt.Errorf("%w: non-digit character '%c' found in string length", ErrMalformedData, b)
		}
		lenBytes = append(lenBytes, b)
	}

	if len(lenBytes) > 1 && lenBytes[0] == '0' {
		return "", fmt.Errorf("%w: string length has leading zero ('%s')", ErrMalformedData, string(lenBytes))
	}

	length, err := strconv.ParseInt(string(lenBytes), 10, 64)
	if err != nil {

		return "", fmt.Errorf("%w: could not parse string length '%s': %v", ErrMalformedData, string(lenBytes), err)
	}

	if length < 0 {

		return "", fmt.Errorf("%w: negative string length (%d) is invalid", ErrMalformedData, length)
	}

	const maxStringLength = 64 * 1024 * 1024
	if length > maxStringLength {
		return "", fmt.Errorf("%w: string length %d exceeds maximum allowed %d", ErrMalformedData, length, maxStringLength)
	}

	strBytes := make([]byte, length)
	n, err := io.ReadFull(br, strBytes)
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return "", fmt.Errorf("%w: unexpected EOF while reading string content (expected %d bytes, got %d)", ErrMalformedData, length, n)
		}
		return "", err
	}

	return string(strBytes), nil
}

func parseList(br *bufio.Reader) ([]interface{}, error) {
	list := make([]interface{}, 0)
	for {

		firstByte, err := br.Peek(1)
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("%w: unexpected EOF while expecting list element or 'e'", ErrMalformedData)
			}
			return nil, err
		}

		if firstByte[0] == 'e' {
			_, err = br.ReadByte()
			if err != nil {
				return nil, err
			}
			break
		}

		element, err := decodeRecursive(br)
		if err != nil {

			return nil, fmt.Errorf("gobencode: error parsing list element: %w", err)
		}
		list = append(list, element)
	}
	return list, nil
}

func parseDictionary(br *bufio.Reader) (map[string]interface{}, error) {
	dict := make(map[string]interface{})
	var lastKey string
	firstKeyProcessed := false

	for {

		firstByte, err := br.Peek(1)
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("%w: unexpected EOF while expecting dict key or 'e'", ErrMalformedData)
			}
			return nil, err
		}

		if firstByte[0] == 'e' {
			_, err = br.ReadByte()
			if err != nil {
				return nil, err
			}
			break
		}

		keyInterface, err := decodeRecursive(br)
		if err != nil {
			return nil, fmt.Errorf("gobencode: error parsing dictionary key: %w", err)
		}

		key, ok := keyInterface.(string)
		if !ok {
			return nil, fmt.Errorf("%w: dictionary key is not a string (got %T)", ErrMalformedData, keyInterface)
		}

		if firstKeyProcessed && key <= lastKey {

			return nil, fmt.Errorf("%w: dictionary keys not in lexicographical order ('%s' after '%s')", ErrMalformedData, key, lastKey)
		}
		lastKey = key
		firstKeyProcessed = true

		value, err := decodeRecursive(br)
		if err != nil {
			return nil, fmt.Errorf("gobencode: error parsing dictionary value for key '%s': %w", key, err)
		}

		dict[key] = value
	}
	return dict, nil
}
