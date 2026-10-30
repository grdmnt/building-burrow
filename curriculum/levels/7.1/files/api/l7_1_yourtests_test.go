package api_test

import (
	"testing"

	"burrow/internal/testkit"
)

// mutantWriter is shared by the mutants below. It lets a mutant rewrite the
// status code or headers your handler sends.
const mutantWriter = `
type mutantWriter struct {
	http.ResponseWriter
	fix   func(code int, h http.Header) int
	wrote bool
}

func (m *mutantWriter) WriteHeader(code int) {
	if !m.wrote {
		m.wrote = true
		code = m.fix(code, m.ResponseWriter.Header())
	}
	m.ResponseWriter.WriteHeader(code)
}

func (m *mutantWriter) Write(b []byte) (int, error) {
	if !m.wrote {
		m.WriteHeader(http.StatusOK)
	}
	return m.ResponseWriter.Write(b)
}

func mutantWrap(h http.Handler, fix func(r *http.Request, code int, hdr http.Header) int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(&mutantWriter{ResponseWriter: w, fix: func(code int, hdr http.Header) int {
			return fix(r, code, hdr)
		}}, r)
	})
}
`

// Your own tests in api/ are graded here. Each mutant is your handler with one
// bug planted in it; at least one of your tests must fail for each. Use
// net/http/httptest: NewRecorder for one handler call, NewServer for a real
// HTTP round trip.
func TestL7_1_YourTestsCatchBugs(t *testing.T) {
	testkit.CheckYourTests(t, testkit.MutationCheck{
		Dir: "api",
		Mutants: []testkit.Mutant{
			{
				Bug:    "a failed If-Match answers 404 instead of 412",
				Target: "Server.Handler",
				Source: `import "net/http"

func (s *Server) Handler() http.Handler {
	return mutantWrap(s.OrigHandler(), func(r *http.Request, code int, h http.Header) int {
		if code == http.StatusPreconditionFailed {
			return http.StatusNotFound
		}
		return code
	})
}
` + mutantWriter,
			},
			{
				Bug:    "GET doesn't send the ETag header",
				Target: "Server.Handler",
				Source: `import "net/http"

func (s *Server) Handler() http.Handler {
	return mutantWrap(s.OrigHandler(), func(r *http.Request, code int, h http.Header) int {
		if r.Method == http.MethodGet {
			h.Del("ETag")
		}
		return code
	})
}
` + mutantWriter,
			},
			{
				Bug:    "PUT ignores ?ttl=",
				Target: "Server.Handler",
				Source: `import "net/http"

func (s *Server) Handler() http.Handler {
	orig := s.OrigHandler()
	return mutantWrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			q := r.URL.Query()
			q.Del("ttl")
			r.URL.RawQuery = q.Encode()
		}
		orig.ServeHTTP(w, r)
	}), func(r *http.Request, code int, h http.Header) int { return code })
}
` + mutantWriter,
			},
			{
				Bug:    "an unconditional DELETE of a missing key answers 204 instead of 404",
				Target: "Server.Handler",
				Source: `import "net/http"

func (s *Server) Handler() http.Handler {
	return mutantWrap(s.OrigHandler(), func(r *http.Request, code int, h http.Header) int {
		if r.Method == http.MethodDelete && r.Header.Get("If-Match") == "" && code == http.StatusNotFound {
			return http.StatusNoContent
		}
		return code
	})
}
` + mutantWriter,
			},
		},
	})
}
