module github.com/samhotchkiss/otter-camp

go 1.25.6

require (
	github.com/go-chi/chi/v5 v5.0.0
	github.com/go-chi/cors v1.0.0
	github.com/golang-migrate/migrate/v4 v4.17.0
)

replace github.com/go-chi/chi/v5 => ./third_party/chi
replace github.com/go-chi/cors => ./third_party/cors
