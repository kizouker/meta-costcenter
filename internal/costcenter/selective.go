package costcenter

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"encoding/json/jsontext"
)

// Session logs carry whole transcripts, but only a few usage fields are ever read. Decoding just
// those fields, and skipping the rest without building strings for it, makes a first import several
// times faster. Skipped values are still validated, so a malformed line is counted as before.

// keep names the members of an object to decode; a nil keep decodes the whole value.
type keep map[string]keep

var recordFields = keep{
	"payload_type": nil, "type": nil, "recorded_at": nil, "timestamp": nil, "sequence": nil, "session_id": nil, "id": nil,
	"stream": keep{"id": nil},
	"usage":  keep{"models": nil},
	"payload": keep{
		"event":  keep{"kind": nil, "model": nil, "usage": nil},
		"record": keep{"workspace_root": nil, "model_id": nil},
		// Turns: a completion's run, and the run a prompt started or joined. Never the prompt text.
		"run_id": nil, "intent_id": nil, "outcome": keep{"run_id": nil},
	},
}

var anchorFields = keep{"id": nil, "sequence": nil, "recorded_at": nil}

var decoders = sync.Pool{New: func() any { return jsontext.NewDecoder(bytes.NewReader(nil)) }}

// decodeSelective parses one JSON value, keeping only the members named by fields.
func decodeSelective(data []byte, fields keep) (any, error) {
	d := decoders.Get().(*jsontext.Decoder)
	defer decoders.Put(d)
	d.Reset(bytes.NewReader(data), jsontext.AllowDuplicateNames(true))
	value, err := readSelective(d, fields)
	if err != nil {
		return nil, err
	}
	// Python's json.loads rejects anything after the value other than whitespace.
	if _, err := d.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, errors.New("extra data after JSON value")
	}
	return value, nil
}

func readSelective(d *jsontext.Decoder, fields keep) (any, error) {
	kind := d.PeekKind()
	switch kind {
	case '"', 'n', 't', 'f':
		token, err := d.ReadToken()
		if err != nil {
			return nil, err
		}
		switch kind {
		case '"':
			return token.String(), nil
		case 'n':
			return nil, nil
		}
		return token.Bool(), nil
	case '0':
		raw, err := d.ReadValue()
		if err != nil {
			return nil, err
		}
		return json.Number(string(raw)), nil
	}
	if fields == nil || kind != '{' {
		raw, err := d.ReadValue()
		if err != nil {
			return nil, err
		}
		return decodeJSON(raw)
	}
	if _, err := d.ReadToken(); err != nil {
		return nil, err
	}
	object := map[string]any{}
	for d.PeekKind() != '}' {
		token, err := d.ReadToken()
		if err != nil {
			return nil, err
		}
		// A token is only valid until the next call on the decoder, so copy the name now.
		name := token.String()
		sub, wanted := fields[name]
		if !wanted {
			if err := d.SkipValue(); err != nil {
				return nil, err
			}
			continue
		}
		value, err := readSelective(d, sub)
		if err != nil {
			return nil, err
		}
		object[name] = value
	}
	if _, err := d.ReadToken(); err != nil {
		return nil, err
	}
	return object, nil
}
