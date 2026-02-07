package chi

import "net/http"

type Mux struct {
	mux         *http.ServeMux
	middlewares []func(http.Handler) http.Handler
}

func NewRouter() *Mux {
	return &Mux{mux: http.NewServeMux()}
}

func (m *Mux) Use(middlewares ...func(http.Handler) http.Handler) {
	m.middlewares = append(m.middlewares, middlewares...)
}

func (m *Mux) Get(pattern string, handler http.HandlerFunc) {
	m.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		handler(w, r)
	})
}

func (m *Mux) Post(pattern string, handler http.HandlerFunc) {
	m.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		handler(w, r)
	})
}

func (m *Mux) Patch(pattern string, handler http.HandlerFunc) {
	m.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		handler(w, r)
	})
}

func (m *Mux) Handle(pattern string, handler http.Handler) {
	m.mux.Handle(pattern, handler)
}

func URLParam(r *http.Request, key string) string {
	// Stub: extract from path. Real chi uses context.
	// For now return empty - would need proper path parsing
	return r.PathValue(key)
}

func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var handler http.Handler = m.mux
	for i := len(m.middlewares) - 1; i >= 0; i-- {
		handler = m.middlewares[i](handler)
	}
	handler.ServeHTTP(w, r)
}
