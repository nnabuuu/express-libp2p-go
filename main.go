package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p-core/peer"
	 "github.com/multiformats/go-multihash"
	"github.com/libp2p/go-libp2p-kad-dht"
	"github.com/mr-tron/base58"
)

type Keypair struct {
	Seed      []byte `json:"seed"`
	CreatedAt string `json:"createdAt"`
	LastUsed  string `json:"lastUsed"`
	PublicKey []byte `json:"publicKey,omitempty"`
}

func main() {
	// Load or generate keypair
	keypair := LoadOrGenerateKeypair()

	// Get environment variables (with defaults)
	isGateway := os.Getenv("IS_GATEWAY") == "1"
	nodePort := getEnvInt("NODE_PORT", 15050)
	libp2pPort := getEnvInt("LIBP2P_PORT", 4010)
	bootstrap := strings.Split(os.Getenv("BOOTSTRAP_ADDRS"), ",")
	tunnelAPI := "http://localhost:" + os.Getenv("API_PORT") + "/libp2p/message"

	// Create the Libp2p service
	service := NewLibp2pNodeService(keypair, nodePort, tunnelAPI, isGateway, bootstrap)
	service.InitNode()

	// Create the controller
	controller := NewLibp2pNodeController(service)

	// Set up router
	router := mux.NewRouter()
	router.HandleFunc("/libp2p/send", controller.SendHandler).Methods("POST")

	// Start the HTTP server
	srv := &http.Server{
		Handler: router,
		Addr:    ":" + strconv.Itoa(libp2pPort),
	}

	// Run server in a goroutine
	go func() {
		log.Printf("HTTP server started on :%d", libp2pPort)
		if err := srv.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
	log.Println("Shutting down...")
	service.Stop()
	srv.Shutdown(context.Background())
}

// LoadOrGenerateKeypair function for keypair generation or loading
func LoadOrGenerateKeypair() Keypair {
	keyDir := getDataDir()
	keyFile := keyDir + "/device-keypair.json"

	// Check if the keypair file exists
	if _, err := os.Stat(keyFile); err == nil {
		// Read keypair from the file
		kpStr, err := os.ReadFile(keyFile)
		if err != nil {
			log.Fatal("Error reading keypair: ", err)
		}
		var kp Keypair
		err = json.Unmarshal(kpStr, &kp)
		if err != nil {
			log.Fatal("Error unmarshalling keypair: ", err)
		}
		kp.LastUsed = time.Now().Format(time.RFC3339)
		kpStr, err = json.Marshal(kp)
		if err != nil {
			log.Fatal("Error marshalling updated keypair: ", err)
		}
		err = os.WriteFile(keyFile, kpStr, 0644)
		if err != nil {
			log.Fatal("Error writing updated keypair: ", err)
		}
		log.Printf("[KeyPair] Loaded from %s", keyFile)
		return kp
	} else {
		// Generate a new keypair
		priv, pub, err := crypto.GenerateKeyPair(crypto.Ed25519, 0)
		if err != nil {
			log.Fatal("Error generating keypair: ", err)
		}
		kp := Keypair{
			Seed:      priv[:],
			CreatedAt: time.Now().Format(time.RFC3339),
			LastUsed:  time.Now().Format(time.RFC3339),
			PublicKey: pub[:],
		}
		_ = os.MkdirAll(keyDir, os.ModePerm)
		kpStr, err := json.Marshal(kp)
		if err != nil {
			log.Fatal("Error marshalling keypair: ", err)
		}
		err = os.WriteFile(keyFile, kpStr, 0644)
		if err != nil {
			log.Fatal("Error writing keypair to file: ", err)
		}
		log.Printf("[KeyPair] Generated new and saved to %s", keyFile)
		return kp
	}
}

// getEnvInt fetches an integer environment variable with a default fallback
func getEnvInt(key string, defaultVal int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultVal
	}
	intVal, err := strconv.Atoi(value)
	if err != nil {
		return defaultVal
	}
	return intVal
}

// NewLibp2pNodeService function initializes the Libp2p service
func NewLibp2pNodeService(kp Keypair, port int, tunnelAPI string, isGateway bool, bootstrap []string) *Libp2pNodeService {
	did := "gateway"
	if !isGateway {
		did = ToSightDID(kp.PublicKey)
	}
	return &Libp2pNodeService{
		keypair:   kp,
		did:       did,
		tunnelAPI: tunnelAPI,
		isGateway: isGateway,
		nodePort:  port,
		bootstrap: bootstrap,
	}
}

// NewLibp2pNodeController function initializes the controller
func NewLibp2pNodeController(service *Libp2pNodeService) *Libp2pNodeController {
	return &Libp2pNodeController{service}
}

// ToSightDID generates a DID for the node from the public key
func ToSightDID(publicKey []byte) string {
	multicodec := append([]byte{0xed, 0x01}, publicKey...)
	return "did:sight:hoster:" + base58.Encode(multicodec)
}

// Libp2pNodeService represents the libp2p node service
type Libp2pNodeService struct {
	did         string
	keypair     Keypair
	tunnelAPI   string
	isGateway   bool
	node        *libp2p.Host
	pubsub      *pubsub.PubSub
	subscribed  *pubsub.Subscription
	topic       *pubsub.Topic
	bootstrap   []string
	nodePort    int
}

// Libp2pNodeController represents the HTTP controller for the service
type Libp2pNodeController struct {
	service *Libp2pNodeService
}

// SendHandler handles the "send" HTTP request
func (c *Libp2pNodeController) SendHandler(w http.ResponseWriter, r *http.Request) {
	var tunnelMsg map[string]interface{}
	err := json.NewDecoder(r.Body).Decode(&tunnelMsg)
	if err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}
	libp2pMsg := map[string]interface{}{
		"to":      tunnelMsg["to"],
		"payload": tunnelMsg,
	}
	c.service.HandleOutgoingMessage(libp2pMsg)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleIncomingMessages processes incoming pubsub messages
func (s *Libp2pNodeService) handleIncomingMessages(ctx context.Context) {
	for {
		msg, err := s.subscribed.Next(ctx)
		if err != nil {
			log.Println("PubSub error:", err)
			return
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(msg.Data, &payload); err != nil {
			log.Println("Invalid message:", err)
			continue
		}
		if payload["to"] != s.did {
			continue
		}
		buf, _ := json.Marshal(payload["payload"])
		_, err = http.Post(s.tunnelAPI, "application/json", bytes.NewBuffer(buf))
		if err != nil {
			log.Println("Forward error:", err)
		}
	}
}

// HandleOutgoingMessage publishes outgoing messages via pubsub
func (s *Libp2pNodeService) HandleOutgoingMessage(msg map[string]interface{}) {
	data, _ := json.Marshal(msg)
	s.topic.Publish(context.Background(), data)
}

// Stop gracefully stops the service
func (s *Libp2pNodeService) Stop() {
	(*s.node).Close()
}

// getDataDir gets the directory where the configuration is stored
func getDataDir() string {
	if dir := os.Getenv("SIGHTAI_DATA_DIR"); dir != "" {
		return dir + "/config"
	}
	return os.Getenv("HOME") + "/.sightai/config"
}
