package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p-core/peer"
	maddr "github.com/multiformats/go-multiaddr"
	"github.com/mr-tron/base58" // Correct base58 package import
)

// Keypair struct to hold the keypair data
type Keypair struct {
	Seed      []byte `json:"seed"`
	CreatedAt string `json:"createdAt"`
	LastUsed  string `json:"lastUsed"`
	PublicKey []byte `json:"publicKey,omitempty"`
}

// LoadOrGenerateKeypair function for loading or generating a keypair
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

// getDataDir gets the directory where the configuration is stored
func getDataDir() string {
	if dir := os.Getenv("SIGHTAI_DATA_DIR"); dir != "" {
		return dir + "/config"
	}
	return os.Getenv("HOME") + "/.sightai/config"
}

// CreateLibp2pNode creates a libp2p node and returns the host and pubsub service
func CreateLibp2pNode(ctx context.Context, port int, bootstrapList []string) (libp2p.Host, *pubsub.PubSub) {
	priv, pub, err := crypto.GenerateKeyPair(crypto.Ed25519, 0)
	if err != nil {
		log.Fatal("Failed to generate keypair: ", err)
	}
	host, err := libp2p.New(
	    libp2p.DefaultMuxers,
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port)),
		libp2p.Identity(priv),
	)
	if err != nil {
		log.Fatal("Failed to create libp2p host: ", err)
	}
	log.Printf("Libp2p Host created with peer ID: %s", host.ID())

	pubsubService, err := pubsub.NewGossipSub(ctx, host)
	if err != nil {
		log.Fatal("Failed to create pubsub service: ", err)
	}

	// Optionally add bootstrap nodes
    if len(bootstrapList) > 0 {
    	var peerAddrs []peer.AddrInfo
    	for _, addr := range bootstrapList {
    		info, err := peer.AddrInfoFromString(addr)
    		if err != nil {
    			log.Printf("Invalid bootstrap addr: %s (%v)", addr, err)
    			continue
    		}
    		peerAddrs = append(peerAddrs, *info)
    	}
    	for _, info := range peerAddrs {
    		if err := host.Connect(ctx, info); err != nil {
    			log.Printf("Failed to connect to %s: %v", info.ID, err)
    		} else {
    			log.Printf("Connected to bootstrap peer: %s", info.ID)
    		}
    	}
    }
	return host, pubsubService
}

// ToSightDID generates a DID for the node from the public key
func ToSightDID(publicKey []byte) string {
	multicodec := append([]byte{0xed, 0x01}, publicKey...)
	return "did:sight:hoster:" + base58.Encode(multicodec)
}
