package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
)

// MirrorSecuritySchemes promotes OpenAI's tool securitySchemes declaration from
// _meta.securitySchemes (which the Go MCP SDK can represent today) to the
// descriptor's top-level securitySchemes field while preserving the _meta copy
// for backwards compatibility.
//
// The official Go SDK v1.8.0 does not yet expose a first-class Tool.SecuritySchemes
// field, but ChatGPT's plugin contract expects the top-level field.
func MirrorSecuritySchemes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := newBufferedResponseWriter()
		next.ServeHTTP(rec, r)

		body := rec.body.Bytes()
		contentType := rec.header.Get("Content-Type")
		if len(body) > 0 && isJSONContentType(contentType) {
			var value any
			if err := json.Unmarshal(body, &value); err == nil {
				if mirrorToolSecuritySchemes(value) {
					if encoded, err := json.Marshal(value); err == nil {
						body = encoded
						rec.header.Set("Content-Length", "")
						rec.header.Del("Content-Length")
					}
				}
			}
		}

		copyHeaders(w.Header(), rec.header)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}

func mirrorToolSecuritySchemes(value any) bool {
	changed := false

	var walk func(any)
	walk = func(current any) {
		switch v := current.(type) {
		case map[string]any:
			if _, hasName := v["name"]; hasName {
				if _, hasInputSchema := v["inputSchema"]; hasInputSchema {
					if meta, ok := v["_meta"].(map[string]any); ok {
						if schemes, ok := meta["securitySchemes"]; ok {
							if _, exists := v["securitySchemes"]; !exists {
								v["securitySchemes"] = schemes
								changed = true
							}
						}
					}
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}

	walk(value)
	return changed
}

func isJSONContentType(contentType string) bool {
	if contentType == "" {
		return true
	}
	for i := 0; i < len(contentType); i++ {
		if contentType[i] == ';' {
			contentType = contentType[:i]
			break
		}
	}
	return contentType == "application/json" || contentType == "application/json-rpc"
}

type bufferedResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{header: make(http.Header)}
}

func (w *bufferedResponseWriter) Header() http.Header {
	return w.header
}

func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *bufferedResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}

func (w *bufferedResponseWriter) Flush() {}

func copyHeaders(dst, src http.Header) {
	for key := range dst {
		dst.Del(key)
	}
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
