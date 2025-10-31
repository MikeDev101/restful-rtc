# RESTful RTC
An extremely dumb HTTP REST proxy over WebRTC.

# Usage
On one end, run the endpoint: `go build . && ./endpoint` (or `go run .`). Give the endpoint server a name.
On the other end, run the gateway: `go build . && ./gateway` (or `go run .`). Enter the endpoint server's name to connect to.

The gateway will spawn a HTTP REST server on port 8000, which will then proxy all traffic over WebRTC to the endpoint server.
The endpoint server will then send the traffic to the target REST server (i.e. localhost:8000) and return responses to the gateway client.

Think of it like the VSCode share server feature. 
