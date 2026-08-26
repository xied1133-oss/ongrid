package biz

import "encoding/json"

// jsonEncode marshals v to JSON; any error value from the preceding call
// is propagated without eating it (see tunnel.Handler contract: err !=
// nil is sent to the peer as a SetError).
//
// Using a generic "pack (value, err)" helper keeps the handler call
// sites to one line each.
func jsonEncode[T any](v T, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// jsonDecode is the inverse for handler inputs.
func jsonDecode(body []byte, out any) error {
	return json.Unmarshal(body, out)
}
