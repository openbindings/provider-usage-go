module github.com/openbindings/provider-usage-go

go 1.24.0

toolchain go1.24.1

require (
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510
	github.com/openbindings/openbindings-go v0.0.0
	github.com/openbindings/usage-go v0.0.0
)

require github.com/sblinch/kdl-go v0.0.0-20260120205643-17a91a33fe63 // indirect

replace github.com/openbindings/openbindings-go => ../openbindings-go

replace github.com/openbindings/usage-go => ../usage-go
