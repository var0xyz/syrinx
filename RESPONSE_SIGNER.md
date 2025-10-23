# Response Signer Middleware

## Overview

The `responseSigner` middleware automatically signs the complete HTTP response (headers + body) using the server's OpenPGP private key before sending it to the client. This provides cryptographic proof that the entire response originated from the server.

## Implementation

### Core Components

1. **responseSigner struct** - Wraps `http.ResponseWriter` to buffer and sign complete responses
2. **responseSignerMiddleware** - HTTP middleware that applies the signer to all responses
3. **buildCanonicalHeaderString** - Creates a consistent string representation of headers for signing
4. **signCompleteResponse** - Signs the entire response (headers + body) with detached signature

### How It Works

1. The middleware wraps the `http.ResponseWriter` with a `responseSigner`
2. The response is buffered (headers + body) without being sent to the client
3. When the handler completes, the `Flush()` method is called:
   - The complete response (headers + body) is signed using the server's private key
   - The signature is added as the `Signature` header
   - The complete response is sent to the client

### Key Features

- **Complete response signing**: Signs both headers and body for comprehensive verification
- **Automatic signing**: All API responses are signed without changes to handler code
- **Canonical format**: Headers are sorted and formatted consistently for reliable verification
- **Server key**: Uses the server's private key for all responses
- **Non-breaking**: If signing fails, the response still proceeds (error is logged)
- **CORS support**: The signature header is exposed via `Access-Control-Expose-Headers`

## Code Location

### Files Modified

- `middlewares.go` - Contains the `responseSigner` implementation and middleware
- `main.go` - Middleware is registered in the API router

### Key Code Sections

#### responseSigner struct (middlewares.go:39-46)
```go
type responseSigner struct {
	http.ResponseWriter
	statusCode     int
	headersCaptured bool
	wroteHeaders    bool
	cryptoService   *CryptoService
	dataService     *DataService
	userID          string
}
```

#### Middleware Registration (main.go:95)
```go
api.Use(h.responseSignerMiddleware)
```

## Usage

### Server-Side

The middleware is automatically applied to all `/api/*` routes. No changes needed in handlers.

### Client-Side Verification

To verify a response signature:

1. Capture all response headers (except `X-Syrinx-Signature`)
2. Build the canonical header string:
   - Sort header names alphabetically
   - For each header: `{name}: {comma-separated sorted values}`
   - Join with newlines
3. Get the server's public key from the database
4. Verify the PGP signature in `X-Syrinx-Signature` header

Example canonical header string:
```
Content-Length: 42
Content-Type: application/json
Date: Mon, 01 Jan 2024 00:00:00 GMT
X-Custom-Header: value1, value2
```

### Example Response

```
HTTP/1.1 200 OK
Content-Type: application/json
Content-Length: 42
Date: Mon, 01 Jan 2024 00:00:00 GMT
X-Syrinx-Signature: -----BEGIN PGP SIGNATURE-----
iQEzBAABCAAdFiEE...
-----END PGP SIGNATURE-----

{"message": "Success"}
```

## Configuration

### Environment Variables

No additional configuration required. Uses existing:
- Session management for user authentication
- `server_keys` table for private keys
- OpenPGP implementation from `CryptoService`

### File System

Requires a private key file at:
- `./keys/syrinx.private.pgp` - Armored PGP private key file

The server will use this single private key for signing all responses.

## Security Considerations

1. **Key Management**: Server private key is stored in a file on the filesystem
2. **Single Key**: All responses are signed with the same server private key
3. **Signature Coverage**: Complete response (headers + body) is signed
4. **Timing**: Signing happens after the complete response is buffered
5. **Complete Coverage**: All headers including Content-Length and Date are included
6. **Graceful Degradation**: If signing fails, response still proceeds

## Performance Impact

- **Minimal overhead**: Signing happens once per request
- **File I/O**: One file read per request (could be cached in memory)
- **Cryptographic cost**: One signature operation per response
- **Header ordering**: O(n log n) for sorting headers

## Testing

The middleware can be tested by:

1. Making API requests (authenticated or not)
2. Capturing the `Signature` header
3. Verifying the signature matches the canonical header string
4. Ensuring requests work even if the private key file is missing

## Future Enhancements

Potential improvements:

1. **Caching**: Cache private key in memory to avoid file reads
2. **Selective signing**: Only sign specific routes or response types
3. **Body signing**: Optionally include response body in signature
4. **Signature metadata**: Include timestamp, request ID in signature
5. **Multiple algorithms**: Support different signature algorithms
6. **Client verification helper**: Provide client library for easy verification

## Integration

The middleware integrates with existing systems:

- **File System**: Reads private key from `./keys/syrinx.private.pgp`
- **Crypto Service**: Uses existing `Sign()` method
- **Logging**: Logs errors using zerolog
- **CORS**: Signature header is exposed for cross-origin requests

## Example Integration Test

```go
func TestResponseSigning(t *testing.T) {
    // Create test server with middleware
    // Make authenticated request
    // Extract X-Syrinx-Signature header
    // Verify signature using server's public key
    // Assert signature is valid
}
```

## Troubleshooting

### No signature in response
- Check that `./keys/syrinx.private.pgp` file exists
- Verify the private key file is readable
- Check logs for signing errors

### Invalid signature
- Ensure canonical header format matches exactly
- Verify correct public key is used
- Check for header modification by proxies

### Performance issues
- Consider caching private key in memory
- Monitor file I/O performance
- Profile signature generation time

## References

- OpenPGP implementation: `github.com/ProtonMail/go-crypto/openpgp`
- HTTP middleware pattern: `github.com/gorilla/mux`
- Signature format: PGP detached signature format (armor delimiters stripped, newlines escaped for HTTP headers)

