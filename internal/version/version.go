package version

import "runtime"

var (
	Version   = "dev"
	Commit    = "unknown"
	BuiltAt   = "unknown"
	GoVersion = runtime.Version()
)
