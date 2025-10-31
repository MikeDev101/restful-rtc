# RESTful RTC
An extremely dumb HTTP REST proxy over WebRTC.

# Usage
`./restful-rtc -mode=... -host=... -id=... -target=... -port=...`

> `-host` (string)
> Target to forward requests in endpoint mode. Example: `http://localhost:8000`.

> `-id` (string)
> Client ID for endpoint mode. Example: -id=your_endpoint_id

> `-mode` (string)
> Run in 'gateway' or 'endpoint' mode

> `-port` (uint)
> Gateway server's host port. Example: If your endpoint server is forwarding `http://localhost:8000`, use `-port=8000`.

> `-target` (string)
> Client ID to connect to in gateway mode. Example: `-target=your_endpoint_id`

# Building
Use `go mod tidy` and `go build .`. It's not that hard...

# Limitations
* Has no support for WebSockets (yet).
* Has no security checks; it's just a really dumb forwarder...
