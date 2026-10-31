package httpx

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
)

// discardLogger returns a logger that writes nothing (for tests).
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stringReader wraps a string in an io.Reader (for tests).
func stringReader(s string) io.Reader {
	return strings.NewReader(s)
}

// jsonUnmarshal decodes JSON bytes into v (for tests).
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
