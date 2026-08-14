package admin

import _ "embed"

//go:embed index.html
var indexHTML string

// IndexHTML returns the embedded Gateway management console.
func IndexHTML() string {
	return indexHTML
}
