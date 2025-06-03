// Package gobencode implements encoding and decoding of Bencode data.
package gobencode // Promenili smo ime paketa

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// ErrMalformedData is returned when the bencoded data is not in the correct format.
var ErrMalformedData = errors.New("gobencode: malformed data") // Promenili smo ime u poruci


// Decode parses bencoded data from the reader and returns its Go representation.
// The returned interface{} will be one of:
// int64, string, []interface{}, map[string]interface{}
func Decode(r io.Reader) (interface{}, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return decodeRecursive(br)
}

// decodeRecursive is the internal recursive decoding function.
func decodeRecursive(br *bufio.Reader) (interface{}, error) {
	firstByte, err := br.ReadByte()
	if err != nil {
		if err == io.EOF {
			// Ako je EOF na samom početku čitanja vrednosti, to je malformed.
			// Ako je EOF unutar npr. liste nakon nekoliko elemenata, to će obraditi parseList.
			return nil, fmt.Errorf("%w: unexpected EOF while expecting a value", ErrMalformedData)
		}
		return nil, err // Other I/O error
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
		if err := br.UnreadByte(); err != nil { // Put the first byte back
			return nil, err
		}
		return parseString(br)
	default:
		return nil, fmt.Errorf("%w: unexpected token '%c' (0x%x) at start of value", ErrMalformedData, firstByte, firstByte)
	}
}

// parseInteger parses a bencoded integer from the reader.
// It assumes the initial 'i' has already been consumed.
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

		if b == 'e' { // End of integer
			if len(numBytes) == 0 {
				return 0, fmt.Errorf("%w: integer 'ie' is invalid", ErrMalformedData)
			}
			break
		}
		numBytes = append(numBytes, b)
	}

	numStr := string(numBytes)

	// Validate integer format:
	// - "i-0e" is invalid
	// - "i03e" is invalid (leading zeros for non-zero numbers)
	// - "i0e" is valid
	if len(numStr) > 1 && numStr[0] == '0' {
		return 0, fmt.Errorf("%w: invalid integer format (leading zero for non-zero number '%s')", ErrMalformedData, numStr)
	}
	if len(numStr) > 2 && numStr[0] == '-' && numStr[1] == '0' {
		return 0, fmt.Errorf("%w: invalid integer format (leading zero for negative number '%s')", ErrMalformedData, numStr)
	}
    if numStr == "-" { // "i-e"
        return 0, fmt.Errorf("%w: invalid integer format (just a hyphen)", ErrMalformedData)
    }


	val, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: could not parse integer string '%s': %v", ErrMalformedData, numStr, err)
	}
	return val, nil
}

// parseString parses a bencoded string from the reader.
// It assumes the first digit of the length has already been peeked (and unread).
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

	// Bencode spec disallows leading zeros in string length, unless the length is 0 itself.
	if len(lenBytes) > 1 && lenBytes[0] == '0' {
		return "", fmt.Errorf("%w: string length has leading zero ('%s')", ErrMalformedData, string(lenBytes))
	}


	length, err := strconv.ParseInt(string(lenBytes), 10, 64)
	if err != nil {
		// This should ideally not happen if previous checks are correct, but as a safeguard:
		return "", fmt.Errorf("%w: could not parse string length '%s': %v", ErrMalformedData, string(lenBytes), err)
	}

	if length < 0 {
		// Theoretical, as ParseInt above with base 10 on digits '0'-'9' won't produce negative.
		// But good for completeness if the spec allowed negative lengths (it doesn't).
		return "", fmt.Errorf("%w: negative string length (%d) is invalid", ErrMalformedData, length)
	}
    
    // Zaštita od ekstremno velikih dužina stringova koje bi mogle da izazovu DoS
    // Možeš podesiti ovaj limit po potrebi. 1GB je verovatno previše za torrent metainfo.
    const maxStringLength = 64 * 1024 * 1024 // 64 MB
    if length > maxStringLength {
        return "", fmt.Errorf("%w: string length %d exceeds maximum allowed %d", ErrMalformedData, length, maxStringLength)
    }


	strBytes := make([]byte, length)
	n, err := io.ReadFull(br, strBytes)
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return "", fmt.Errorf("%w: unexpected EOF while reading string content (expected %d bytes, got %d)", ErrMalformedData, length, n)
		}
		return "", err // Other I/O error
	}

	return string(strBytes), nil
}

func parseList(br *bufio.Reader) ([]interface{}, error) {
	list := make([]interface{}, 0)
	for {
		// Peek at the next byte to check for the end-of-list marker 'e'
		// without consuming it yet.
		firstByte, err := br.Peek(1)
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("%w: unexpected EOF while expecting list element or 'e'", ErrMalformedData)
			}
			return nil, err // Other I/O error
		}

		if firstByte[0] == 'e' {
			_, err = br.ReadByte() // Consume the 'e'
			if err != nil {        // Should not happen if Peek succeeded, but good practice
				return nil, err
			}
			break // End of list
		}

		// If not 'e', then it must be an element.
		// Decode the element recursively.
		element, err := decodeRecursive(br) // Ključno: rekurzivni poziv
		if err != nil {
			// Greška iz rekurzivnog poziva će već biti formatirana,
			// ali možemo dodati kontekst da je nastala unutar liste.
			return nil, fmt.Errorf("gobencode: error parsing list element: %w", err)
		}
		list = append(list, element)
	}
	return list, nil
}

func parseDictionary(br *bufio.Reader) (map[string]interface{}, error) {
	dict := make(map[string]interface{})
	var lastKey string // For checking lexicographical order (optional for decoder robustness)
	firstKeyProcessed := false

	for {
		// Peek at the next byte for 'e' or a key (which must be a string)
		firstByte, err := br.Peek(1)
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("%w: unexpected EOF while expecting dict key or 'e'", ErrMalformedData)
			}
			return nil, err
		}

		if firstByte[0] == 'e' {
			_, err = br.ReadByte() // Consume 'e'
			if err != nil {
				return nil, err
			}
			break // End of dictionary
		}

		// Keys in a bencoded dictionary MUST be strings.
		// We can call parseString directly here, but parseString expects
		// the first digit to be unread. So, we'll call decodeRecursive
		// and then type-assert.
		keyInterface, err := decodeRecursive(br) // Parsiraj ključ
		if err != nil {
			return nil, fmt.Errorf("gobencode: error parsing dictionary key: %w", err)
		}

		key, ok := keyInterface.(string)
		if !ok {
			return nil, fmt.Errorf("%w: dictionary key is not a string (got %T)", ErrMalformedData, keyInterface)
		}

		// Optional: Check if keys are in lexicographical order
		if firstKeyProcessed && key <= lastKey {
			// The spec requires keys to be sorted. Some decoders are lenient.
			// For robustness, we might choose to error out or just log a warning.
			// For now, let's be strict as per spec.
			return nil, fmt.Errorf("%w: dictionary keys not in lexicographical order ('%s' after '%s')", ErrMalformedData, key, lastKey)
		}
		lastKey = key
		firstKeyProcessed = true


		// Parse the value associated with the key
		value, err := decodeRecursive(br) // Parsiraj vrednost
		if err != nil {
			return nil, fmt.Errorf("gobencode: error parsing dictionary value for key '%s': %w", key, err)
		}

		dict[key] = value
	}
	return dict, nil
}

