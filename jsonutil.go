package main

import (
	"encoding/json"
	"io"
)

func jsonEncode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func jsonDecode(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	return dec.Decode(v)
}
