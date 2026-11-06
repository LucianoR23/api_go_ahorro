package httpx

import "encoding/json"

// Optional is a JSON field for partial updates (PATCH) that tells apart the
// three cases a pointer cannot:
//
//	field absent        -> Set == false             (leave as is)
//	"field": null       -> Set == true, Null == true (clear)
//	"field": <value>    -> Set == true, Value        (replace)
//
// encoding/json only calls UnmarshalJSON when the key is present, so the zero
// value means "absent".
type Optional[T any] struct {
	Set   bool
	Null  bool
	Value T
}

// UnmarshalJSON implements json.Unmarshaler.
func (o *Optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Null = true
		return nil
	}
	return json.Unmarshal(b, &o.Value)
}
