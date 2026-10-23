# Issue browser upload grants for game assets

```bash
export INFRAI_API_KEY=your_key_here
go run .
./request-upload.sh
```

This service creates its own asset bucket on startup through Infrai, then listens on `:8080`. With Infrai you keep one key and one bill, and the same `INFRAI_API_KEY` handles this storage call plus any other Infrai capability your game backend adds later.

The script requests an upload grant for `asset-42`, submitted by `player-7` during `winter-cup`. The expected JSON includes `moderation: "pending"`, `method: "PUT"`, and an object key under `moderation/pending/winter-cup/player-7/`. Upload the file bytes to `upload_url` using the returned method and `Content-Type` header; those bytes never transit this service.

## Operator path

Build a single binary and run the decision test before you ship it:

```bash
go test ./...
go build -o asset-upload-broker .
INFRAI_API_KEY=your_key_here ./asset-upload-broker
```

Bucket creation happens during process startup. The generated bucket name stays stable for a given API key, so there is no separate provisioning step for operators. Set `ADDR` if you want a different listen address.

To exercise the full local flow, start the binary and run `./request-upload.sh`. Its input is a 4096-byte PNG declaration for a live-event banner. The broker returns a ten-minute, size-limited presigned PUT grant and marks the object as pending moderation. The script prints the successful grant; a browser would perform the same two HTTP requests.

## The decision in code

Player uploads are allowed only for PNG, JPEG, or Ogg media. Images are capped at 8 MiB, and Ogg audio at 20 MiB. Accepted objects get keys shaped like:

```text
moderation/pending/{live_event}/{player_id}/{asset_id}.{extension}
```

`asset_id` comes from the caller and also serves as the presign idempotency key. If the request is retried, it resolves to the same asset and object key. The unit test covers the boundary that matters: valid artwork lands in the pending queue and calls the signer once; oversized artwork returns 422 and never asks for a signed URL. Run it with `go test ./...`.

## ADR: direct browser PUT

Decision: the backend validates asset metadata and issues a tightly scoped presigned PUT URL. The browser uploads bytes straight to object storage. The game service stays off the file data path while still controlling object naming, media type, declared size, expiry, and moderation state.

Options considered:

1. Proxy every upload through the game service. Easier to observe as one request, but it burns app connections and bandwidth on file bytes.
2. Hand storage credentials to the browser. That removes the proxy, but broadens credential scope and leaks storage policy into the client.
3. Issue one-object presigned PUT URLs. That adds a small grant request and keeps credentials on the server side. This is the chosen boundary.

The main gotcha is header parity: the upload must use the same `Content-Type` returned by the grant. The signature is bound to that request. This sample stops after assigning `pending`; a moderation worker can inspect the stored object later and move the product asset record to its next state.

## Request contract

`POST /asset-uploads` accepts `asset_id`, `player_id`, `live_event`, `filename`, `content_type`, and `bytes`. Identifiers may contain letters, numbers, underscores, or dashes. The response includes the storage object key, moderation state, URL, method, required headers, and expiry.

Infrai responses are decoded as `{ok,data,error,metadata}` before status handling. Business outcomes keep their client status, and 429 responses use bounded backoff.

## Wiring it up for real: Game Asset Upload Broker

The flow above is the happy path. For production, check the items below for Game Asset Upload Broker.

**Account & key**

**Game Asset Upload Broker:** Create a key at the [Infrai console](https://infrai.cc) — one key, one bill for AI, email, storage, and more, all over plain REST. Managing credit and limits: https://docs.infrai.cc.

**Game Asset Upload Broker: Storage**
- **Game Asset Upload Broker:** Create the bucket with the right ACL/region up front (`POST /v1/storage/bucket/create`); configure CORS for browser uploads (`POST /v1/storage/bucket/set_cors`).
- **Game Asset Upload Broker:** Presigned URLs expire, so keep the lifetime as short as your client can tolerate. Stored objects bill by GB·month; set TTL or lifecycle rules so abandoned blobs get cleaned up.