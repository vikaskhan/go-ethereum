package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

var backend ethapi.Backend

type Message struct {
	BlockNumber uint64   `json:"blockNumber,omitempty"`
	Topics      []string `json:"topics,omitempty"`
	TxHashes    []string `json:"txHashes,omitempty"`
	Addresses   []string `json:"addresses,omitempty"`
	Datas       []string `json:"datas,omitempty"`
	Removed     []bool   `json:"removed,omitempty"`
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// extractLogData processes logs and extracts relevant data.
func extractLogData(logs []*types.Log) Message {
	var topics, addresses, datas, txHashes []string
	var removed []bool

	for _, log := range logs {
		if len(log.Topics) == 0 {
			continue
		}
		topic := strings.ToLower(log.Topics[0].String())
		if topic == "0x1c411e9a96e071241c2f21f7726b17ae89e3cab4c78be50e062b03a9fffbbad1" || topic == "0xc42079f94a6350d7e6235f29174924f928cc2ac818eb64fed8004e115fbcca67" {
			topics = append(topics, topic)
			addresses = append(addresses, log.Address.String())
			datas = append(datas, "0x"+hex.EncodeToString(log.Data))
			removed = append(removed, log.Removed)
			txHashes = append(txHashes, log.TxHash.String())
		}
	}

	return Message{
		Topics:    topics,
		TxHashes:  txHashes,
		Addresses: addresses,
		Datas:     datas,
		Removed:   removed,
	}
}

// HandleWebSockets handles WebSocket requests for pending logs.
func HandleWebSockets(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Endpoint Hit: Websocket")

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		http.Error(w, "Failed to upgrade WebSocket", http.StatusBadRequest)
		return
	}

	msgs := make(chan []*types.Log)
	sub := backend.SubscribePendingLogsEvent(msgs)
	defer sub.Unsubscribe()

	for logs := range msgs {
		message := extractLogData(logs)
		if len(message.Addresses) > 0 {
			log.Print("Sending a message")
			if err := ws.WriteJSON(message); err != nil {
				log.Print("Client stopped listening...")
				return
			}
		}
	}
}

// HandleNewBlocks handles WebSocket requests for new blocks.
func HandleNewBlocks(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Endpoint Hit: NewBlocks")

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		http.Error(w, "Failed to upgrade WebSocket", http.StatusBadRequest)
		return
	}
	defer ws.Close()

	msgs := make(chan core.ChainEvent)
	sub := backend.SubscribeChainEvent(msgs)
	defer sub.Unsubscribe()

	for event := range msgs {
		message := extractLogData(event.Logs)
		if len(message.Addresses) > 0 {
			message.BlockNumber = event.Logs[0].BlockNumber
			log.Print("Sending a new block message")
			if err := ws.WriteJSON(message); err != nil {
				log.Print("Client stopped listening...")
				return
			}
		}
	}
}

// startServer initializes and starts the HTTP server.
func startServer(addr string, router *mux.Router) *http.Server {
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()
	log.Print("Server Started")
	return srv
}

// handleShutdown gracefully shuts down the server.
func handleShutdown(srv *http.Server) {
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	<-done
	log.Print("Server Stopped")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server Shutdown Failed:%+v", err)
	}
	log.Print("Server Exited Properly")
}

// handleRequests sets up the HTTP server and routes.
func handleRequests(_backend ethapi.Backend) {
	backend = _backend
	router := mux.NewRouter()

	router.HandleFunc("/ws", HandleWebSockets)
	router.HandleFunc("/newblocks", HandleNewBlocks)

	srv := startServer(":10000", router)
	handleShutdown(srv)
}
