package main

import (
	"bufio"
	"flag"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

type ClientInfo struct {
	uid  uint32
	name string
}

var client_file = flag.String("client_file", "client.txt", "Client info file")
var max_sender = flag.Uint("max_sender", 1, "Max sender number")

func loadSenderUser() ([]ClientInfo, error) {
	f, err := os.Open(*client_file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	client_list := make([]ClientInfo, 0, 100)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		items := strings.Split(line, "\t")
		if len(items) != 2 {
			log.Printf("Invalid client line: %s\n", line)
			continue
		}
		uid, err := strconv.ParseUint(items[0], 10, 32)
		if err != nil {
			log.Printf("Invalid client line : %s\n", line)
			continue
		}
		client_list = append(client_list, ClientInfo{uid: uint32(uid), name: items[1]})

		if len(client_list) >= int(*max_sender) {
			break
		}
	}

	return client_list, nil
}

func main() {
	flag.Parse()

	client_items, err := loadSenderUser()
	if err != nil {
		log.Printf("Failed to load client list: %s\n", err.Error())
		os.Exit(1)
	}

	log.SetFlags(log.Lshortfile | log.Ldate)

	sync_chan := make(chan int, 10000)

	client_list := make([]*LetterClient, 0, 1000)
	for _, client_info := range client_items {
		letter_client := newLetterClient(client_info.uid, client_info.name, sync_chan)
		client_list = append(client_list, letter_client)
		go letter_client.runClient()
	}

	for {
		log.Printf("Put one message to pool")

		for i := 0; i < 1; i++ {
			sync_chan <- 1
		}
		time.Sleep(time.Second)
	}
}
