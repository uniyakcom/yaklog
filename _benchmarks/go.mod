module github.com/uniyakcom/yaklog/_benchmarks

go 1.25

require (
	github.com/rs/zerolog v1.34.0
	github.com/sirupsen/logrus v1.9.4
	github.com/uniyakcom/yaklog v0.0.0
	go.uber.org/zap v1.27.1
)

require (
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/uniyakcom/yakjson v1.4.4 // indirect
	github.com/uniyakcom/yakutil v1.3.1 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
)

replace github.com/uniyakcom/yaklog => ../
