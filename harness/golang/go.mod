module github.com/stevenpelley/should-I-async/harness/golang

go 1.22.0

require (
	github.com/stretchr/testify v1.8.4
	golang.org/x/sys v0.17.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-errors/errors v0.0.0-00010101000000-000000000000
	github.com/pmezard/go-difflib v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/go-errors/errors => github.com/stevenpelley/errors-fork/v2 v2.0.2
