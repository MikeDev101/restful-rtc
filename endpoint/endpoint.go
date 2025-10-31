// endpoint_peerjs.go
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	peerjs "github.com/muka/peerjs-go"
)

// --- SHARED STRUCTS ---
// We define our new packet wrapper
const (
	PacketTypeRequest  = "request"
	PacketTypeResponse = "response"
	MaxChunkSize       = 16 * 1024 // 16KB chunks (well under the 64KB limit)
)

type Packet struct {
	ID       string `json:"id"`       // The unique ID of the *full request/response*
	Type     string `json:"type"`     // "request" or "response"
	Sequence int    `json:"sequence"` // 0, 1, 2...
	IsLast   bool   `json:"is_last"`
	Payload  []byte `json:"payload"`
}

type ForwardedRequest struct {
	ID      string      `json:"id"`
	Method  string      `json:"method"`
	Path    string      `json:"path"`
	Query   string      `json:"query"`
	Headers http.Header `json:"headers"`
	Body    []byte      `json:"body"`
}
type ForwardedResponse struct {
	ID         string      `json:"id"`
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers"`
	Body       []byte      `json:"body"`
}

// --- REASSEMBLER ---
// For reassembling request packets
type ReassemblyBuffer struct {
	sync.Mutex
	packets      map[int][]byte
	lastSequence int
}

func NewReassemblyBuffer() *ReassemblyBuffer {
	return &ReassemblyBuffer{
		packets:      make(map[int][]byte),
		lastSequence: -1, // -1 means we haven't seen the last packet yet
	}
}

// map[requestID] -> *ReassemblyBuffer
var requestBuffers sync.Map

// This is the target service you are forwarding to.
const targetBaseURL = "http://localhost:8000"

// --- STDIN HELPER ---
func readFromStdin(prompt string) string {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
}

// --- SPLITTER ---
// sendSplitPacket handles chunking and sending our data
func sendSplitPacket(dc *peerjs.DataConnection, requestID, packetType string, data []byte) error {
	for i := 0; ; i++ {
		start := i * MaxChunkSize
		end := start + MaxChunkSize
		isLast := false

		if end >= len(data) {
			end = len(data)
			isLast = true
		}

		packet := Packet{
			ID:       requestID,
			Type:     packetType,
			Sequence: i,
			IsLast:   isLast,
			Payload:  data[start:end],
		}

		packetBytes, err := json.Marshal(packet)
		if err != nil {
			return fmt.Errorf("failed to marshal packet: %v", err)
		}

		// Send with chunking
		if err := dc.Send(packetBytes, true); err != nil {
			return fmt.Errorf("failed to send packet: %v", err)
		}

		if isLast {
			break
		}
	}
	return nil
}

func main() {
	opts := peerjs.NewOptions()
	opts.Host = "peerjs.mikedev101.cc" // <-- IMPORTANT: Change this!
	opts.Port = 443
	opts.Secure = true
	opts.Path = "/"
	opts.Debug = 3

	endpointID := readFromStdin("Enter the ID you want this endpoint to have: ")
	if endpointID == "" {
		log.Fatal("Endpoint ID cannot be empty.")
	}

	endpointPeer, err := peerjs.NewPeer(endpointID, opts)
	if err != nil {
		log.Fatal("Failed to create peer:", err)
	}
	defer endpointPeer.Close()

	log.Printf("Endpoint peer created with ID: %s", endpointPeer.ID)
	log.Println("Waiting for gateway to connect...")

	endpointPeer.On("connection", func(data interface{}) {
		conn := data.(*peerjs.DataConnection)
		log.Printf("Gateway '%s' connected!", conn.GetPeerID())

		conn.On("open", func(data interface{}) {
			log.Println("Data channel open! Ready to receive requests.")
		})

		// ==========================================================
		// 						REASSEMBLER (for Requests)
		// ==========================================================
		conn.On("data", func(data interface{}) {
			var packet Packet
			if err := json.Unmarshal(data.([]byte), &packet); err != nil {
				log.Printf("Error unmarshaling packet: %v", err)
				return
			}

			// We only care about "request" packets here
			if packet.Type != PacketTypeRequest {
				return
			}

			// Get or create the buffer for this request ID
			buf, _ := requestBuffers.LoadOrStore(packet.ID, NewReassemblyBuffer())
			rb := buf.(*ReassemblyBuffer)

			rb.Lock()
			// Store the packet payload
			rb.packets[packet.Sequence] = packet.Payload
			if packet.IsLast {
				rb.lastSequence = packet.Sequence
			}

			// Check if we have all the packets
			isComplete := rb.lastSequence != -1 && len(rb.packets) == rb.lastSequence+1
			rb.Unlock()

			if isComplete {
				// --- We have all packets, reassemble them ---
				log.Printf("Reassembled request for %s", packet.ID)

				// Sort the keys (sequence numbers)
				keys := make([]int, 0, len(rb.packets))
				for k := range rb.packets {
					keys = append(keys, k)
				}
				sort.Ints(keys)

				// Concatenate the payloads in order
				var fullRequestData bytes.Buffer
				for _, k := range keys {
					fullRequestData.Write(rb.packets[k])
				}

				// Clean up the buffer
				requestBuffers.Delete(packet.ID)

				// Process the assembled request in a new goroutine
				go handleAssembledRequest(fullRequestData.Bytes(), conn)
			}
		})
		// ==========================================================
	})

	select {}
}

// handleAssembledRequest processes the reassembled request and sends back a split response
func handleAssembledRequest(fullRequestData []byte, conn *peerjs.DataConnection) {
	var req ForwardedRequest
	if err := json.Unmarshal(fullRequestData, &req); err != nil {
		log.Printf("Error unmarshaling full request: %v", err)
		return
	}

	// 1. We got a request. Execute it.
	log.Printf("Received request %s: %s %s", req.ID, req.Method, req.Path)
	resp := executeRequest(req)

	// ==========================================================
	// 						SPLITTER (for Responses)
	// ==========================================================

	// 2. Marshal the *ForwardedResponse*
	respBytes, err := json.Marshal(resp)
	if err != nil {
		log.Printf("Error marshaling response: %v", err)
		return
	}

	// 3. Send the response back as split packets
	log.Printf("Sending response %s (%d bytes)", req.ID, len(respBytes))
	if err := sendSplitPacket(conn, req.ID, PacketTypeResponse, respBytes); err != nil {
		log.Printf("Error sending split response: %v", err)
	}
	// ==========================================================
}

// --- HTTP EXECUTION ---
func executeRequest(req ForwardedRequest) ForwardedResponse {
	url := targetBaseURL + req.Path
	if req.Query != "" {
		url += "?" + req.Query
	}

	clientReq, err := http.NewRequest(req.Method, url, bytes.NewReader(req.Body))
	if err != nil {
		return errorResponse(req.ID, 500, "Failed to create request")
	}
	clientReq.Header = req.Headers

	client := &http.Client{}
	clientResp, err := client.Do(clientReq)
	if err != nil {
		return errorResponse(req.ID, 502, "Failed to execute request")
	}
	defer clientResp.Body.Close()

	respBody, err := io.ReadAll(clientResp.Body)
	if err != nil {
		return errorResponse(req.ID, 500, "Failed to read response body")
	}

	return ForwardedResponse{
		ID:         req.ID,
		StatusCode: clientResp.StatusCode,
		Headers:    clientResp.Header,
		Body:       respBody,
	}
}

func errorResponse(id string, code int, message string) ForwardedResponse {
	return ForwardedResponse{
		ID:         id,
		StatusCode: code,
		Headers:    http.Header{"Content-Type": []string{"text/plain"}},
		Body:       []byte(message),
	}
}
