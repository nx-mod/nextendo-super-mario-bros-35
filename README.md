<h1 align="center">super-mario-bros-35</h1>

<p align="center">
  <b>Nextendo Network game server for Super Mario Bros. 35.</b>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/license-PolyForm%20Shield%201.0.0-orange" alt="License: PolyForm Shield 1.0.0">
  <img src="https://img.shields.io/badge/go-1.23%2B-00ADD8" alt="Go 1.23+">
</p>

---

## What is this?

The NEX game server for **Super Mario Bros. 35** on [Nextendo Network](https://nextendo.network),
whose retail online service Nintendo shut down in April 2021. It handles authentication and
matchmaking, speaking the same NEX protocol the retail servers did — access key `0a69c592`, NEX
version `4.6.0`, per [kinnay/NintendoClients](https://github.com/kinnay/NintendoClients)' Game
Server List.

It is built on the [**nextendo-nex**](https://github.com/NextendoNetwork/nextendo-nex) core (PRUDP
transport, RMC layer, common service protocols), plus this repo's own implementation of the three
protocols the core doesn't provide generically: **Ranking2** (0x7A), **MatchmakeReferee** (0x78),
and **MessageDelivery** (0x1B) — see [`smb35_stubs.go`](smb35_stubs.go).

### Not peer-to-peer

Unlike ARMS/MK8/Splatoon 2/SSBU, SMB35 does **not** play over Pia P2P. NEX matchmaking only forms
the up-to-35-player lobby; the actual gameplay traffic goes through a separate **Eagle** relay
session this server spawns per gathering and hands each joiner via a notification event (see
`nextendo-nex`'s `eagle.go` for the relay itself, and this repo's `main.go` for the handoff). Eagle
is a from-scratch Go port of the protocol documented on
[NintendoClients' Eagle Protocol wiki page](https://github.com/kinnay/NintendoClients/wiki/Eagle-Protocol)
— the same relay Tetris 99, PAC-MAN 99, and F-Zero 99 use, so it's shared core code, not
SMB35-specific.

**Status:** implemented against the documented wire protocol and
[kinnay/SMB35](https://github.com/kinnay/SMB35)'s archived reference server (read for behavior, no
code copied — see that repo's own AGPL-3.0 license); not yet verified against a real SMB35 client.
Needs a real multi-client test (matchmaking lobby fill, Eagle handoff, in-game relay) before this is
fully confirmed end-to-end — see the main Nextendo deployment for how that's tracked.

## Running

```sh
cp example.env .env    # then edit .env
go run .
```

Configuration is entirely through environment variables — see [`example.env`](example.env). No
secrets are baked into the source.

**Build note:** the Eagle relay support this server uses isn't in the latest published
[`nextendo-nex`](https://github.com/NextendoNetwork/nextendo-nex) release yet — `go.mod` points at
a local sibling checkout (`replace ... => ../nextendo-nex`) until a release includes it. Clone
`nextendo-nex` alongside this repo to build from source in the meantime.

## What this is not

This server ships **no** Nintendo code, keys, or copyrighted assets. It is an independent
reimplementation for use with a community-run replacement service, not affiliated with, endorsed by,
or associated with Nintendo. The NEX access key it uses is a well-known per-title value derivable
from the game itself, not a secret.

## License

Released under the **[PolyForm Shield License 1.0.0](LICENSE.md)** — source-available: read, use,
modify, and self-host, but do not use it to provide a product that competes with Nextendo Network.
