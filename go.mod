module github.com/NextendoNetwork/super-mario-bros-35

go 1.23.0

require github.com/NextendoNetwork/nextendo-nex v0.1.2

// Local dev: build the core from the neighboring checkout instead of the frozen
// v0.1.2 module-cache copy, so eagle.go/notification.go/matchmaking_handlers.go
// changes on the smb35-eagle-support branch are visible here. Same pattern as
// arms-mythrax on the VPS. Remove this line once nextendo-nex publishes a
// release that includes Eagle support and bump the require above to match.
replace github.com/NextendoNetwork/nextendo-nex => ../nextendo-nex

require (
	github.com/klauspost/compress v1.17.9 // indirect
	github.com/lxzan/gws v1.10.0 // indirect
)
