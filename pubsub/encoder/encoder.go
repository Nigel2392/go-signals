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

//	func NewGobEncoder() Encoder {
//		return gobEncoder{}
//	}
//
//	type gobEncoder struct{}
//
//	func (e gobEncoder) EncodeBytes(v any) ([]byte, error) {
//		buf := new(bytes.Buffer)
//		enc := gob.NewEncoder(buf)
//		err := enc.Encode(v)
//		return buf.Bytes(), err
//	}
//
//	func (e gobEncoder) Decode(data io.Reader, v any) error {
//		dec := gob.NewDecoder(data)
//		return dec.Decode(v)
//	}
