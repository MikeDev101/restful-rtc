package main

import (
	"flag"
	"log"

	"restful_rtc/endpoint"
	"restful_rtc/gateway"
)

func main() {
	// Define a command-line flag
	// Example: go run main.go -mode=gateway
	mode := flag.String("mode", "", "Run in 'gateway' or 'endpoint' mode")
	id := flag.String("id", "", "Client ID for endpoint mode. Example: -id=your_endpoint_id")
	target := flag.String("target", "", "Client ID to connect to in gateway mode. Example: -target=your_endpoint_id")
	host := flag.String("host", "", "Target server for endpoint mode. Example: http://localhost:8000.")
	port := flag.Uint("port", 0, "Gateway server's host port. Example: http://localhost:8000, use -port=8000.")

	// Parse the flags
	flag.Parse()

	// Run the appropriate function based on the flag
	switch *mode {
	case "gateway":
		if *port == 0 {
			log.Fatal("Error: You must specify a port. Example: -port=8000")
		}
		if *target == "" {
			log.Fatal("Error: You must specify a client ID to connect to. Example: -id=your_endpoint_id")
		}
		log.Println("Starting in Gateway mode...")
		gateway.RunGateway(*target, int(*port))
	case "endpoint":
		if *host == "" {
			log.Fatal("Error: You must specify a host. Example: -host=http://localhost:8000")
		}
		if *id == "" {
			log.Fatal("Error: You must specify a client ID to create. Example: -id=your_endpoint_id")
		}
		log.Println("Starting in Endpoint mode...")
		endpoint.RunEndpoint(*id, *host)
	default:
		log.Fatal("Error: You must specify a mode. Example: -mode=gateway or -mode=endpoint")
	}
}
