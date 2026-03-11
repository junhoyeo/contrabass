package team

import (
	"encoding/json"
	"errors"
	"strings"
)

func isJSONUnmarshalError(err error) bool {
	if err == nil {
		return false
	}

	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return true
	}

	var unmarshalTypeErr *json.UnmarshalTypeError
	if errors.As(err, &unmarshalTypeErr) {
		return true
	}

	return strings.Contains(err.Error(), "unmarshal")
}
