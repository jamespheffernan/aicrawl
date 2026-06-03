package jsonstream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"unicode"
)

func ForEachTopLevelArrayValue(r io.Reader, emit func(raw json.RawMessage, index int) error) (bool, error) {
	if emit == nil {
		return false, fmt.Errorf("JSON array callback is required")
	}
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	first, err := firstNonSpace(br)
	if err != nil {
		if err == io.EOF {
			return false, nil
		}
		return false, err
	}
	if first != '[' {
		return false, nil
	}
	dec := json.NewDecoder(br)
	token, err := dec.Token()
	if err != nil {
		return true, err
	}
	if token != json.Delim('[') {
		return false, nil
	}
	index := 0
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return true, fmt.Errorf("decode JSON array item %d: %w", index, err)
		}
		if err := emit(raw, index); err != nil {
			return true, err
		}
		index++
	}
	token, err = dec.Token()
	if err != nil {
		return true, err
	}
	if token != json.Delim(']') {
		return true, fmt.Errorf("JSON array did not close")
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return true, fmt.Errorf("JSON source contains trailing data")
		}
		return true, err
	}
	return true, nil
}

func firstNonSpace(r *bufio.Reader) (byte, error) {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		if !unicode.IsSpace(rune(b)) {
			if err := r.UnreadByte(); err != nil {
				return 0, err
			}
			return b, nil
		}
	}
}
