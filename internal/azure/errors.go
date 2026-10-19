package azure

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// ErrorMessage condenses verbose SDK errors into a single readable line.
func ErrorMessage(err error) string {
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		var body struct {
			Error struct {
				Code, Message string
			} `json:"error"`
		}
		if raw, ok := rawBody(re); ok && json.Unmarshal(raw, &body) == nil && body.Error.Message != "" {
			return body.Error.Code + ": " + body.Error.Message
		}
		if re.ErrorCode != "" {
			return re.ErrorCode
		}
		if re.RawResponse != nil {
			return re.RawResponse.Status
		}
	}
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return s
}

// rawBody extracts the JSON error body that ResponseError.Error() embeds.
func rawBody(re *azcore.ResponseError) ([]byte, bool) {
	s := re.Error()
	i := strings.Index(s, "{")
	j := strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return nil, false
	}
	return []byte(s[i : j+1]), true
}
