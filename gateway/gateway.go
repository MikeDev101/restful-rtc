// gateway_peerjs.go
package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
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
// For reassembling response packets
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
var responseBuffers sync.Map

// map[requestID] -> chan *ForwardedResponse
var responseChannels sync.Map
var dataConnection *peerjs.DataConnection

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

func RunGateway(id string, port int) {
	opts := peerjs.NewOptions()
	opts.Host = "peerjs.mikedev101.cc" // <-- IMPORTANT: Change this!
	opts.Port = 443
	opts.Secure = true
	opts.Path = "/"
	opts.Debug = 3

	endpoint_id, _ := uuid.NewUUID()
	gatewayPeer, err := peerjs.NewPeer(endpoint_id.String(), opts)
	if err != nil {
		log.Fatal("Failed to create peer:", err)
	}
	defer gatewayPeer.Close()
	log.Printf("Gateway peer created with ID: %s", gatewayPeer.ID)

	if id == "" {
		log.Fatal("Endpoint ID cannot be empty.")
	}
	log.Printf("Attempting to connect to endpoint: %s", id)

	conn, err := gatewayPeer.Connect(id, nil)
	if err != nil {
		log.Fatal("Failed to connect:", err)
	}
	dataConnection = conn
	log.Println("Connection initiated...")

	// ==========================================================
	// 						REASSEMBLER (for Responses)
	// ==========================================================
	conn.On("data", func(data any) {
		var packet Packet
		if err := json.Unmarshal(data.([]byte), &packet); err != nil {
			log.Printf("Error unmarshaling packet: %v", err)
			return
		}

		// We only care about "response" packets here
		if packet.Type != PacketTypeResponse {
			return
		}

		// Get or create the buffer for this request ID
		buf, _ := responseBuffers.LoadOrStore(packet.ID, NewReassemblyBuffer())
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
			log.Printf("Reassembled response for %s", packet.ID)

			// Sort the keys (sequence numbers)
			keys := make([]int, 0, len(rb.packets))
			for k := range rb.packets {
				keys = append(keys, k)
			}
			sort.Ints(keys)

			// Concatenate the payloads in order
			var fullResponseData bytes.Buffer
			for _, k := range keys {
				fullResponseData.Write(rb.packets[k])
			}

			// Clean up the buffer
			responseBuffers.Delete(packet.ID)

			// Now, unmarshal the *full* response
			var resp ForwardedResponse
			if err := json.Unmarshal(fullResponseData.Bytes(), &resp); err != nil {
				log.Printf("Error unmarshaling full response: %v", err)
				return
			}

			// Find the waiting HTTP handler and send it the response
			if ch, ok := responseChannels.Load(resp.ID); ok {
				ch.(chan *ForwardedResponse) <- &resp
			}
		}
	})
	// ==========================================================

	conn.On("open", func(data any) {
		log.Println("Data channel open. Starting HTTP server on http://localhost:" + strconv.Itoa(port))
		http.HandleFunc("/", httpHandler)
		if err := http.ListenAndServe(":"+strconv.Itoa(port), nil); err != nil {
			log.Printf("HTTP server error: %v", err)
		}
	})

	select {}
}

func httpHandler(w http.ResponseWriter, r *http.Request) {
	if dataConnection == nil {
		http.Error(w, "Endpoint not connected", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Can't read body", http.StatusInternalServerError)
		return
	}

	// 1. Create the request struct
	req := ForwardedRequest{
		ID:      uuid.NewString(), // This ID is now critical
		Method:  r.Method,
		Path:    r.URL.Path,
		Query:   r.URL.RawQuery,
		Headers: r.Header,
		Body:    body,
	}

	// 2. Create response channel to wait on
	ch := make(chan *ForwardedResponse)
	responseChannels.Store(req.ID, ch)
	defer responseChannels.Delete(req.ID)

	// ==========================================================
	// 						SPLITTER (for Requests)
	// ==========================================================

	// 3. Marshal the *ForwardedRequest* struct
	reqBytes, err := json.Marshal(req)
	if err != nil {
		http.Error(w, "Failed to marshal request", http.StatusInternalServerError)
		return
	}

	// 4. Send it as split packets
	log.Printf("Sending request %s (%d bytes)", req.ID, len(reqBytes))
	if err := sendSplitPacket(dataConnection, req.ID, PacketTypeRequest, reqBytes); err != nil {
		http.Error(w, "Failed to forward request", http.StatusInternalServerError)
		return
	}
	// ==========================================================

	// 5. Wait for the reassembled response (with a timeout)
	select {
	case resp := <-ch:
		for k, v := range resp.Headers {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		w.Write(resp.Body)
	case <-time.After(30 * time.Second): // Increased timeout for chunking
		http.Error(w, "Request timed out", http.StatusGatewayTimeout)
	}
}
