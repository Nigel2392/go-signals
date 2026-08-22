package encoder

import (
	"encoding/json"
	"io"
)

type Encoder interface {
	EncodeBytes(obj any) ([]byte, error)
	Decode(data io.Reader, dst any) error
}

func NewJSONEncoder() Encoder {
	return jsonEncoder{}
}

type jsonEncoder struct{}

func (e jsonEncoder) EncodeBytes(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (e jsonEncoder) Decode(data io.Reader, v any) error {
	dec := json.NewDecoder(data)
	return dec.Decode(v)
}
