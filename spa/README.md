Syrinx SPA (SvelteKit + TS + PWA)

This is a minimal SvelteKit SPA scaffold with TypeScript and PWA support. It includes three routes: `/`, `/login`, and `/signup`, basic API service, localStorage and IndexedDB services, a sample repository, and a simple session store.

Dev / Build

- Install Node.js 20+ and pnpm or npm
- Install deps: `pnpm i` or `npm i`
- Dev: `pnpm dev` or `npm run dev`
- Build: `pnpm build` or `npm run build`
  - Output: `build/` (static), suitable for serving via the Go server.

Type generation from Go → TypeScript

You have several options:

1) OpenAPI-based (recommended for REST)
- Annotate handlers with Swagger comments (e.g. `swaggo/swag`).
- Generate OpenAPI spec: `swag init` (creates `docs/swagger.json`).
- Generate TS types: `npx openapi-typescript ./docs/swagger.json -o src/lib/types/openapi.d.ts`.
- Optionally generate an API client: `npx openapi-fetch ./docs/swagger.json` or `orval`.

2) Direct Go struct → TS types
- Use `github.com/tkrajina/typescriptify-golang-structs` in a small Go generator:

```go
// cmd/tsgen/main.go
package main
import (
    "os"
    "github.com/tkrajina/typescriptify-golang-structs/tscriptify"
)

// import your Go types here, e.g. package main with User, Profile, PublicKey

func main() {
    conv := tscriptify.New()
    conv.AddType(User{})
    conv.AddType(Profile{})
    conv.AddType(PublicKey{})
    _ = os.MkdirAll("spa/src/lib/types", 0o755)
    _ = conv.ConvertToFile("spa/src/lib/types/generated.ts")
}
```

- Run: `go run ./cmd/tsgen`.

3) Protobuf → TypeScript (for WebSocket messages)
- You already have `proto/websocket.proto`.
- Use `ts-proto` plugin:
  - Install: `npm i -D ts-proto`.
  - Generate: `protoc --plugin=./node_modules/.bin/protoc-gen-ts_proto --ts_proto_out=spa/src/lib/types --ts_proto_opt=esModuleInterop=true,outputServices=generic-definitions proto/websocket.proto`.
- This yields `.ts` message types for the websocket payloads.

Pick one or mix: OpenAPI for REST routes, ts-proto for WebSockets. For quick mirroring of a few structs, the direct generator is simplest.

Structure

- `src/routes` — SvelteKit routes: `/`, `/login`, `/signup`
- `src/lib/services` — `api.ts`, `localstorage.ts`, `indexeddb.ts`
- `src/lib/repositories` — example `publicKey.ts`
- `src/lib/stores` — `session.ts`
- `src/lib/types` — `backend.ts` (handwritten mirrors) and potential generated files

