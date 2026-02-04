module github.com/samhotchkiss/otter-camp

go 1.25.6

require (
	github.com/go-chi/chi/v5 v5.0.0
	github.com/go-chi/cors v1.0.0
	github.com/golang-migrate/migrate/v4 v4.17.0
	github.com/gorilla/websocket v1.5.1
	github.com/stretchr/testify v1.8.3
	github.com/testcontainers/testcontainers-go v0.27.0
)

require (
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/lib/pq v1.10.9 // indirect
	go.uber.org/atomic v1.7.0 // indirect
)

replace github.com/go-chi/chi/v5 => ./third_party/chi

replace github.com/go-chi/cors => ./third_party/cors
